// Package debug provides HTTP trace recording for LLM provider calls.
package debug

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// TraceID is a unique identifier for an HTTP trace.
type TraceID string

// HTTPTrace records a single LLM provider HTTP request/response.
type HTTPTrace struct {
	ID              TraceID             `json:"id"`
	Timestamp       time.Time           `json:"timestamp"`
	SessionID       string              `json:"session_id"`
	ProviderID      string              `json:"provider_id"`
	Method          string              `json:"method"`
	URL             string              `json:"url"` // path only
	Status          int                 `json:"status"`
	DurationMs      int64               `json:"duration_ms"`
	RequestBytes    int                 `json:"request_bytes"`
	RequestBody     string              `json:"request_body"`
	ResponseBytes   int                 `json:"response_bytes"`
	ResponseBody    string              `json:"response_body"`
	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`
	Error           string              `json:"error,omitempty"`
}

const (
	// DefaultCapacity is the default number of completed traces to retain.
	DefaultCapacity = 20
	// MaxBodyBytes is the maximum body size to record per trace (256KB).
	MaxBodyBytes = 256 * 1024
)

// Recorder is a thread-safe, bounded recorder for HTTP traces.
// It stores completed traces in a ring buffer and tracks in-flight traces separately.
// The last non-2xx response is preserved in a dedicated slot that is never evicted.
type Recorder struct {
	mu               sync.Mutex
	traces           []*HTTPTrace // ordered oldest-first
	capacity         int
	inFlight         map[TraceID]*HTTPTrace
	nextID           uint64
	lastFailingTrace *HTTPTrace // most recent non-2xx trace, never evicted
}

// NewRecorder creates a Recorder with the given capacity.
func NewRecorder(capacity int) *Recorder {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Recorder{
		traces:   make([]*HTTPTrace, 0, capacity),
		capacity: capacity,
		inFlight: make(map[TraceID]*HTTPTrace),
	}
}

func (r *Recorder) nextTraceID() TraceID {
	r.nextID++
	return TraceID(fmt.Sprintf("trace_%d", r.nextID))
}

// startTrace creates an in-flight trace. Returns the trace ID.
func (r *Recorder) startTrace(sessionID, providerID, method, url string, reqBody []byte) TraceID {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := r.nextTraceID()
	truncated := truncateBody(reqBody)

	trace := &HTTPTrace{
		ID:           id,
		Timestamp:    time.Now(),
		SessionID:    sessionID,
		ProviderID:   providerID,
		Method:       method,
		URL:          url,
		Status:       0, // in-flight
		RequestBytes: len(reqBody),
		RequestBody:  string(truncated),
	}

	r.inFlight[id] = trace
	return id
}

// completeTrace moves an in-flight trace to completed storage.
func (r *Recorder) completeTrace(id TraceID, respBody []byte, status int, duration time.Duration, errMsg string, respHeaders map[string][]string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	trace, ok := r.inFlight[id]
	if !ok {
		return
	}

	trace.Status = status
	trace.DurationMs = duration.Milliseconds()
	trace.Error = errMsg
	trace.ResponseHeaders = respHeaders

	truncated := truncateBody(respBody)
	trace.ResponseBytes = len(respBody)
	trace.ResponseBody = string(truncated)

	delete(r.inFlight, id)

	// Append to ring buffer, drop oldest if at capacity
	if len(r.traces) >= r.capacity {
		r.traces = r.traces[1:]
	}
	r.traces = append(r.traces, trace)

	// Preserve non-2xx traces in a dedicated slot that is never evicted.
	if !isSuccess(status) || errMsg != "" {
		cp := *trace
		r.lastFailingTrace = &cp
	}
}

// isSuccess returns true for HTTP 2xx status codes.
func isSuccess(status int) bool {
	return status >= 200 && status < 300
}

// truncateBody truncates body to MaxBodyBytes and appends a truncation indicator.
func truncateBody(body []byte) []byte {
	if len(body) > MaxBodyBytes {
		n := len(body)
		suffix := fmt.Sprintf("... [truncated %d bytes]", n-MaxBodyBytes)
		result := make([]byte, MaxBodyBytes+len(suffix))
		copy(result, body[:MaxBodyBytes])
		copy(result[MaxBodyBytes:], suffix)
		return result
	}
	return body
}

// Record records a complete (non-streaming) HTTP trace.
func (r *Recorder) Record(sessionID, providerID, method, url string, reqBody, respBody []byte, status int, duration time.Duration, errMsg string, respHeaders map[string][]string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := r.nextTraceID()

	reqTruncated := truncateBody(reqBody)
	respTruncated := truncateBody(respBody)

	trace := &HTTPTrace{
		ID:              id,
		Timestamp:       time.Now(),
		SessionID:       sessionID,
		ProviderID:      providerID,
		Method:          method,
		URL:             url,
		Status:          status,
		DurationMs:      duration.Milliseconds(),
		RequestBytes:    len(reqBody),
		RequestBody:     string(reqTruncated),
		ResponseBytes:   len(respBody),
		ResponseBody:    string(respTruncated),
		ResponseHeaders: respHeaders,
		Error:           errMsg,
	}

	if len(r.traces) >= r.capacity {
		r.traces = r.traces[1:]
	}
	r.traces = append(r.traces, trace)

	// Preserve non-2xx traces in a dedicated slot that is never evicted.
	if !isSuccess(status) || errMsg != "" {
		cp := *trace
		r.lastFailingTrace = &cp
	}
}

// List returns completed traces, optionally filtered.
// limit: max results (0 = use capacity). sessionID/providerID: empty = no filter.
func (r *Recorder) List(limit int, sessionID, providerID string) []*HTTPTrace {
	r.mu.Lock()
	defer r.mu.Unlock()

	if limit <= 0 || limit > r.capacity {
		limit = r.capacity
	}

	var filtered []*HTTPTrace
	for _, t := range r.traces {
		if sessionID != "" && t.SessionID != sessionID {
			continue
		}
		if providerID != "" && t.ProviderID != providerID {
			continue
		}
		filtered = append(filtered, t)
		if len(filtered) >= limit {
			break
		}
	}

	if filtered == nil {
		return []*HTTPTrace{}
	}
	return filtered
}

// InFlight returns all in-flight traces with updated duration.
func (r *Recorder) InFlight() []*HTTPTrace {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	result := make([]*HTTPTrace, 0, len(r.inFlight))
	for _, t := range r.inFlight {
		cp := *t
		cp.DurationMs = now.Sub(cp.Timestamp).Milliseconds()
		result = append(result, &cp)
	}
	return result
}

// Get returns a single trace by ID (searches completed then in-flight).
func (r *Recorder) Get(id TraceID) *HTTPTrace {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check completed
	for _, t := range r.traces {
		if t.ID == id {
			cp := *t
			return &cp
		}
	}

	// Check in-flight
	if t, ok := r.inFlight[id]; ok {
		cp := *t
		cp.DurationMs = time.Since(cp.Timestamp).Milliseconds()
		return &cp
	}

	return nil
}

// Count returns the number of completed traces.
func (r *Recorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.traces)
}

// LastFailingTrace returns the most recent non-2xx HTTP trace (or errored request),
// or nil if there were no failing traces. This trace is never evicted by the ring buffer.
func (r *Recorder) LastFailingTrace() *HTTPTrace {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.lastFailingTrace == nil {
		return nil
	}
	cp := *r.lastFailingTrace
	return &cp
}

// ————— RoundTripper —————

// RecordingRoundTripper wraps an http.RoundTripper and records all
// requests/responses through the given Recorder.
type RecordingRoundTripper struct {
	inner      http.RoundTripper
	recorder   *Recorder
	sessionID  string
	providerID string
}

// NewRecordingRoundTripper creates a RoundTripper that records HTTP traces.
// If inner is nil, http.DefaultTransport is used.
func NewRecordingRoundTripper(inner http.RoundTripper, recorder *Recorder, sessionID, providerID string) *RecordingRoundTripper {
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &RecordingRoundTripper{
		inner:      inner,
		recorder:   recorder,
		sessionID:  sessionID,
		providerID: providerID,
	}
}

func (rt *RecordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Read and buffer request body for recording
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}

	start := time.Now()
	traceID := rt.recorder.startTrace(rt.sessionID, rt.providerID, req.Method, req.URL.Path, reqBody)

	resp, err := rt.inner.RoundTrip(req)

	if err != nil {
		duration := time.Since(start)
		rt.recorder.completeTrace(traceID, nil, 0, duration, err.Error(), nil)
		return nil, err
	}

	// Wrap response body to capture content and complete trace when fully read
	resp.Body = &traceBody{
		ReadCloser:  resp.Body,
		recorder:    rt.recorder,
		traceID:     traceID,
		startTime:   start,
		status:      resp.StatusCode,
		respHeaders: resp.Header,
	}

	return resp, nil
}

// traceBody wraps an io.ReadCloser, captures up to MaxBodyBytes of the response,
// and completes the trace when Close() is called.
type traceBody struct {
	io.ReadCloser
	recorder    *Recorder
	traceID     TraceID
	startTime   time.Time
	status      int
	respHeaders map[string][]string

	mu       sync.Mutex
	buf      bytes.Buffer
	done     bool
	closeErr error
}

func (tb *traceBody) Read(p []byte) (int, error) {
	n, err := tb.ReadCloser.Read(p)
	if n > 0 {
		tb.mu.Lock()
		if tb.buf.Len() < MaxBodyBytes {
			remaining := MaxBodyBytes - tb.buf.Len()
			writeLen := n
			if writeLen > remaining {
				writeLen = remaining
			}
			tb.buf.Write(p[:writeLen])
		}
		tb.mu.Unlock()
	}
	return n, err
}

func (tb *traceBody) Close() error {
	tb.mu.Lock()
	if tb.done {
		tb.mu.Unlock()
		return tb.closeErr
	}
	tb.done = true
	tb.mu.Unlock()

	duration := time.Since(tb.startTime)
	errStr := ""

	err := tb.ReadCloser.Close()
	if err != nil {
		errStr = err.Error()
		tb.mu.Lock()
		tb.closeErr = err
		tb.mu.Unlock()
	}

	tb.recorder.completeTrace(tb.traceID, tb.buf.Bytes(), tb.status, duration, errStr, tb.respHeaders)
	return err
}

// LastN returns the most recent N completed traces for a session, in chronological order.
func (r *Recorder) LastN(sessionID string, n int) []*HTTPTrace {
	r.mu.Lock()
	defer r.mu.Unlock()

	var result []*HTTPTrace
	for i := len(r.traces) - 1; i >= 0 && len(result) < n; i-- {
		if r.traces[i].SessionID == sessionID {
			result = append(result, r.traces[i])
		}
	}
	// Reverse to chronological order
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	if result == nil {
		return []*HTTPTrace{}
	}
	return result
}
