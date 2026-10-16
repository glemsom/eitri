package provider

import (
	"bytes"
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
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
// retries on transport errors (connection refused/reset, timeouts, DNS), on 5xx
// provider responses, and on 429 rate limits, pausing between attempts. Other
// 4xx responses are returned without retry so callers keep handling them. The
// pause is providerRetryDelay widened by jitter, unless the server sent a
// Retry-After header, whose value is honored verbatim. A Retry-After larger than
// the remaining retry budget stops the loop rather than stalling the run. A
// cancelled or expired context stops retries immediately and returns the
// context error.
//
// Between attempts req.Body is replayed from req.GetBody; http.NewRequest*
// populates GetBody for the *bytes.Reader bodies this package sends, so each
// retry replays the full request.
func doWithRetry(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	var lastErr error
	var wait time.Duration
	for attempt := 0; attempt <= providerMaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			if req.GetBody != nil {
				if b, err := req.GetBody(); err == nil {
					req.Body = b
				}
			}
		}
		resp, err := client.Do(req)
		if err == nil && !retryableStatus(resp.StatusCode) {
			return resp, nil
		}
		var retryAfter time.Duration
		var hasRetryAfter bool
		if resp != nil {
			retryAfter, hasRetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if err == nil {
				err = &HTTPError{Code: resp.StatusCode, Body: string(body)}
			}
		}
		lastErr = err

		if attempt == providerMaxRetries {
			break
		}
		next, ok := retryWait(attempt, retryAfter, hasRetryAfter)
		if !ok {
			return nil, lastErr
		}
		wait = next
	}
	return nil, lastErr
}

// retryableStatus reports whether a provider response status warrants a retry:
// server-side failures and rate limits, but not other client errors.
func retryableStatus(code int) bool {
	return code >= http.StatusInternalServerError || code == http.StatusTooManyRequests
}

// parseRetryAfter interprets a Retry-After header value, which per HTTP is
// either a number of seconds to wait or an HTTP-date at which to retry. It
// reports ok=false for an absent or unparseable value so the caller falls back
// to the base backoff; a delta-seconds value or a date already past yields a
// zero wait.
func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(value); err == nil {
		if secs <= 0 {
			return 0, true
		}
		return time.Duration(secs) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	if d := when.Sub(now); d > 0 {
		return d, true
	}
	return 0, true
}

// retryWait returns the pause before the next attempt. An honored Retry-After is
// used verbatim, unless it would spend more than the time the remaining attempts
// are budgeted for (each worth providerRetryDelay), in which case ok=false tells
// the caller to stop rather than sleep past the budget. Without a header the
// base delay is widened by jitter.
func retryWait(attempt int, retryAfter time.Duration, hasRetryAfter bool) (time.Duration, bool) {
	if hasRetryAfter {
		if remaining := time.Duration(providerMaxRetries-attempt) * providerRetryDelay; retryAfter > remaining {
			return 0, false
		}
		return retryAfter, true
	}
	return jitteredDelay(providerRetryDelay), true
}

// jitteredDelay spreads a retry pause across the second half of base so
// concurrent runs sharing a rate limit do not retry in lockstep.
func jitteredDelay(base time.Duration) time.Duration {
	half := base / 2
	if half <= 0 {
		return base
	}
	return half + time.Duration(rand.Int64N(int64(half)))
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
