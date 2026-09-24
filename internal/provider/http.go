package provider

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

// providerMaxRetries is the number of retry attempts after the first failed
// provider request, so a transient failure is tried up to
// providerMaxRetries+1 times in total.
var providerMaxRetries = 3

// providerRetryDelay is the pause between retry attempts.
var providerRetryDelay = 10 * time.Second

func resolveClient(c *http.Client) *http.Client {
	if c == nil {
		return http.DefaultClient
	}
	return c
}

// doWithRetry issues req, retrying transient failures before giving up. It
// retries on transport errors (connection refused/reset, timeouts, DNS) and on
// 5xx provider responses, pausing providerRetryDelay between attempts. Success
// (2xx) and client errors (4xx) are returned without retry so callers keep
// handling them. A cancelled or expired context stops retries immediately and
// returns the context error.
//
// Between attempts req.Body is replayed from req.GetBody; http.NewRequest*
// populates GetBody for the *bytes.Reader bodies this package sends, so each
// retry replays the full request.
func doWithRetry(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= providerMaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(providerRetryDelay):
			}
			if req.GetBody != nil {
				if b, err := req.GetBody(); err == nil {
					req.Body = b
				}
			}
		}
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode < 500 {
			return resp, nil
		}
		if resp != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if err == nil {
				err = &HTTPError{Code: resp.StatusCode, Body: string(body)}
			}
		}
		lastErr = err
	}
	return nil, lastErr
}

// HTTPTraceSink receives raw provider HTTP bodies. It is intended for explicit
// debug capture: payloads can contain prompts, tool output, and provider data.
type HTTPTraceSink interface {
	TraceRequest(body []byte)
	TraceResponse(body []byte)
}

// NewTraceTransport returns a transport that copies raw request and response
// bodies to sink. A nil base uses http.DefaultTransport.
func NewTraceTransport(base http.RoundTripper, sink HTTPTraceSink) http.RoundTripper {
	if sink == nil {
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return traceTransport{base: base, sink: sink}
}

type traceTransport struct {
	base http.RoundTripper
	sink HTTPTraceSink
}

func (t traceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
		t.sink.TraceRequest(body)
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.Body == nil {
		return resp, err
	}
	resp.Body = &traceReadCloser{ReadCloser: resp.Body, sink: t.sink}
	return resp, nil
}

type traceReadCloser struct {
	io.ReadCloser
	sink HTTPTraceSink

	readMu     sync.Mutex
	mu         sync.Mutex
	body       bytes.Buffer
	reads      int
	readsDone  chan struct{}
	closing    bool
	closeDone  chan struct{}
	recorded   bool
	recordDone chan struct{}
}

func (r *traceReadCloser) Read(p []byte) (int, error) {
	r.readMu.Lock()
	defer r.readMu.Unlock()

	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	if r.reads == 0 {
		r.readsDone = make(chan struct{})
	}
	r.reads++
	r.mu.Unlock()

	n, err := r.ReadCloser.Read(p)

	r.mu.Lock()
	if n > 0 {
		_, _ = r.body.Write(p[:n])
	}
	r.reads--
	if r.reads == 0 {
		close(r.readsDone)
	}
	body := []byte(nil)
	var recordDone chan struct{}
	if err == io.EOF && !r.closing && r.reads == 0 {
		body, recordDone = r.recordLocked()
	}
	r.mu.Unlock()
	r.trace(body, recordDone)
	return n, err
}

func (r *traceReadCloser) Close() error {
	r.mu.Lock()
	if r.closing {
		done := r.closeDone
		r.mu.Unlock()
		<-done
		return nil
	}
	r.closing = true
	r.closeDone = make(chan struct{})
	readsDone := r.readsDone
	r.mu.Unlock()

	err := r.ReadCloser.Close()
	if readsDone != nil {
		<-readsDone
	}

	r.mu.Lock()
	body, recordDone := r.recordLocked()
	r.mu.Unlock()
	r.trace(body, recordDone)

	r.mu.Lock()
	close(r.closeDone)
	r.mu.Unlock()
	return err
}

func (r *traceReadCloser) recordLocked() ([]byte, chan struct{}) {
	if r.recorded {
		return nil, r.recordDone
	}
	r.recorded = true
	r.recordDone = make(chan struct{})
	return append([]byte(nil), r.body.Bytes()...), r.recordDone
}

func (r *traceReadCloser) trace(body []byte, done chan struct{}) {
	if done == nil {
		return
	}
	if body == nil {
		<-done
		return
	}
	r.sink.TraceResponse(body)
	close(done)
}
