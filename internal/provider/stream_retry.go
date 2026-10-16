package provider

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

// retryingStream re-issues a streaming request when the underlying stream dies
// with a retryable transport error before it has delivered any content. A
// provider or intermediary that drops an HTTP/2 stream mid-flight (a peer CANCEL
// or REFUSED_STREAM) or resets the connection is transient, so replaying the
// request usually completes and the turn resumes instead of failing. Once any
// content has been yielded a retry would duplicate output, so the error is
// surfaced unchanged.
type retryingStream struct {
	ctx      context.Context
	open     func() (Stream, error)
	cur      Stream
	attempts int
	yielded  bool
}

func (s *retryingStream) Next() (Chunk, error) {
	for {
		c, err := s.cur.Next()
		if err == nil {
			s.yielded = s.yielded || carriesContent(c)
			return c, nil
		}
		if s.yielded || s.attempts >= providerMaxRetries || !retryableStreamError(err) {
			return Chunk{}, err
		}
		select {
		case <-s.ctx.Done():
			return Chunk{}, s.ctx.Err()
		case <-time.After(jitteredDelay(providerRetryDelay)):
		}
		next, oerr := s.open()
		if oerr != nil {
			return Chunk{}, oerr
		}
		s.cur = next
		s.attempts++
	}
}

// carriesContent reports whether a chunk held output the engine appends to the
// turn, so a retry after it would duplicate assistant text, reasoning, or tool
// calls.
func carriesContent(c Chunk) bool {
	return c.Content != "" || c.ReasoningContent != "" || len(c.ToolCalls) > 0
}

// retryableStreamError reports whether err from a streaming read warrants
// re-issuing the request: a stream reset by the peer, a connection dropped
// mid-body, or a net error. A normal end (io.EOF), the caller's cancellation,
// and a stalled-but-open connection are not retried.
func retryableStreamError(err error) bool {
	if err == nil || errors.Is(err, io.EOF) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, ErrStreamStalled) {
		return false
	}
	var se peerStreamError
	if errors.As(err, &se) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne)
}

// peerStreamError mirrors net/http's internal HTTP/2 StreamError. The bundled
// HTTP/2 transport exposes that type to callers only through an As method that
// copies it into any struct with matching fields, so this local mirror
// classifies a peer-dropped stream ("stream error: stream ID N; CANCEL; received
// from peer") without depending on golang.org/x/net/http2.
type peerStreamError struct {
	StreamID uint32
	Code     uint32
	Cause    error
}

func (peerStreamError) Error() string { return "provider stream reset by peer" }

// openStream performs one streaming attempt: it sends the request built by
// build, retrying transport and status failures via doWithRetry, rejects a
// non-2xx response as an HTTPError, and returns the stream the dialect parses
// from the response body. build is called per attempt so every replay issues a
// fresh request whose body is unread.
func openStream(ctx context.Context, client *http.Client, build func() (*http.Request, error), stream func(io.Reader) Stream) (Stream, error) {
	httpReq, err := build()
	if err != nil {
		return nil, err
	}
	resp, err := doWithRetry(ctx, client, httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // cap error payloads to a sane size
		resp.Body.Close()
		return nil, &HTTPError{Code: resp.StatusCode, Body: string(body)}
	}
	streamBody := watchForIdle(resp.Body)
	return closeBodyOnDone(stream(streamBody), streamBody), nil
}

// beginStream opens the first attempt eagerly so a request-level failure (an
// HTTPError, a context overflow) still surfaces from Provider.Stream, then wraps
// the result so a mid-stream reset after the headers is retried.
func beginStream(ctx context.Context, open func() (Stream, error)) (Stream, error) {
	s, err := open()
	if err != nil {
		return nil, err
	}
	return &retryingStream{ctx: ctx, open: open, cur: s}, nil
}
