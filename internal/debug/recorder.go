// Package debug provides HTTP trace recording for LLM provider calls.
package debug

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
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
	// Correlation IDs: RunID identifies the run this LLM call belongs to and
	// Turn is the 1-based turn number within that run, stamped from the run
	// loop's TraceMeta bridge. Together with Attempt they group the traces of
	// one turn (retries produce one trace per attempt).
	RunID string `json:"run_id,omitempty"`
	Turn  int    `json:"turn,omitempty"`
	// Enriched per-call measurements. Model is extracted from the request body
	// at capture time; Usage and FinishReason come from the response body
	// (including the stream tail) or the run loop's TraceMeta bridge; Attempt
	// is the zero-based retry attempt number; ErrorClass is the structured
	// capture-time classification of a failed call; TTFBMs is the time to the
	// first response byte and TTFTMs the time to the first content token.
	Model        string       `json:"model,omitempty"`
	Attempt      int          `json:"attempt,omitempty"`
	FinishReason string       `json:"finish_reason,omitempty"`
	Usage        *UsageTotals `json:"usage,omitempty"`
	ErrorClass   ErrorClass   `json:"error_class,omitempty"`
	TTFBMs       int64        `json:"ttfb_ms,omitempty"` // ms from request start to first response byte
	TTFTMs       int64        `json:"ttft_ms,omitempty"` // ms from request start to first content token
}

const (
	// DefaultCapacity is the default number of completed traces to retain.
	DefaultCapacity = 20
	// MaxBodyBytes is the maximum body size to record per trace (256KB).
	MaxBodyBytes = 256 * 1024
	// DefaultMaxInFlightTraces is the default cap on concurrently tracked
	// in-flight traces. When the cap is reached the oldest in-flight trace is
	// evicted (fail-safe) so the in-flight map stays bounded even when a
	// response body is never read or closed.
	DefaultMaxInFlightTraces = 64
)

// Recorder is a thread-safe, bounded recorder for HTTP traces.
// It stores completed traces in a ring buffer and tracks in-flight traces separately.
// The last non-2xx response is preserved in a dedicated slot that is never evicted.
type Recorder struct {
	mu               sync.Mutex
	traces           []*HTTPTrace // ordered oldest-first
	capacity         int
	inFlight         map[TraceID]*HTTPTrace
	maxInFlight      int
	nextID           uint64
	lastFailingTrace *HTTPTrace // most recent non-2xx trace, never evicted
	metrics          map[providerModelKey]*providerModelMetrics

	// OnComplete, if non-nil, is called after every completed trace (from
	// completeTrace or Record) with the fully populated trace. It is always
	// fired after the recorder mutex is released, so the callback can perform
	// blocking work (e.g. disk persistence) without stalling trace recording
	// or the request path. Set at startup to persist traces to disk via a
	// Persister.
	OnComplete func(trace *HTTPTrace)
}

// NewRecorder creates a Recorder with the given capacity.
func NewRecorder(capacity int) *Recorder {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Recorder{
		traces:      make([]*HTTPTrace, 0, capacity),
		capacity:    capacity,
		inFlight:    make(map[TraceID]*HTTPTrace),
		maxInFlight: DefaultMaxInFlightTraces,
		metrics:     make(map[providerModelKey]*providerModelMetrics),
	}
}

func (r *Recorder) nextTraceID() TraceID {
	r.nextID++
	return TraceID(fmt.Sprintf("trace_%d", r.nextID))
}

// startTrace creates an in-flight trace. Returns the trace ID.
//
// In-flight tracking is bounded: if the in-flight cap is reached (e.g. by
// response bodies that are never read and never closed), the oldest in-flight
// trace is evicted (fail-safe) so the map cannot grow without bound. The
// evicted trace is moved to completed storage with an eviction marker and its
// OnComplete fires after the lock is released.
func (r *Recorder) startTrace(sessionID, providerID, method, url string, reqBody []byte, attempt int, runID string, turn int) TraceID {
	r.mu.Lock()

	var evicted *HTTPTrace
	if len(r.inFlight) >= r.maxInFlight {
		evicted = r.evictOldestLocked()
	}

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
		Model:        extractModel(reqBody),
		Attempt:      attempt,
		RunID:        runID,
		Turn:         turn,
	}

	r.inFlight[id] = trace
	r.mu.Unlock()

	// Fire OnComplete for the evicted trace outside the recorder lock.
	if evicted != nil && r.OnComplete != nil {
		r.OnComplete(evicted)
	}
	return id
}

// completeTrace moves an in-flight trace to completed storage.
func (r *Recorder) completeTrace(id TraceID, respBody []byte, status int, duration time.Duration, errMsg string, respHeaders map[string][]string, usage *UsageTotals, finishReason string) {
	r.completeTraceWithMeta(id, respBody, status, duration, errMsg, respHeaders, usage, finishReason, nil)
}

// completeTraceWithMeta is completeTrace with an optional *TraceMeta bridge.
// When meta is non-nil, the per-call measurements the run loop recorded on it
// (retry attempt, time-to-first-byte, provider usage, finish reason, model)
// override whatever the raw body parsing inferred.
func (r *Recorder) completeTraceWithMeta(id TraceID, respBody []byte, status int, duration time.Duration, errMsg string, respHeaders map[string][]string, usage *UsageTotals, finishReason string, meta *TraceMeta) {
	r.mu.Lock()
	trace := r.finalizeLocked(id, respBody, status, duration, errMsg, respHeaders, usage, finishReason, meta)
	r.mu.Unlock()

	// Fire OnComplete outside the recorder lock so persistence (disk I/O)
	// never blocks trace recording or the request path.
	if trace != nil && r.OnComplete != nil {
		r.OnComplete(trace)
	}
}

// finalizeLocked moves an in-flight trace to completed storage and returns it,
// or nil if the trace is unknown (e.g. already evicted). Assumes r.mu is held.
func (r *Recorder) finalizeLocked(id TraceID, respBody []byte, status int, duration time.Duration, errMsg string, respHeaders map[string][]string, usage *UsageTotals, finishReason string, meta *TraceMeta) *HTTPTrace {
	trace, ok := r.inFlight[id]
	if !ok {
		return nil
	}

	trace.Status = status
	trace.DurationMs = duration.Milliseconds()
	trace.Error = errMsg
	trace.ResponseHeaders = respHeaders
	trace.Usage = usage
	trace.FinishReason = finishReason

	truncated := truncateBody(respBody)
	trace.ResponseBytes = len(respBody)
	trace.ResponseBody = string(truncated)
	trace.ErrorClass = classifyTrace(status, errMsg, trace.ResponseBody)

	// TraceMeta bridge overrides the body-parsed values with the authoritative
	// per-call measurements recorded by the run loop (retry attempt, TTFB,
	// TTFT, provider usage, finish reason, model, run/turn correlation IDs).
	// Applied before aggregation so the interaction metrics count the real values.
	if meta != nil {
		trace.Attempt = meta.Attempt()
		trace.TTFBMs = meta.TTFBMs()
		trace.TTFTMs = meta.TTFTMs()
		if u := meta.Usage(); u != nil {
			trace.Usage = u
		}
		if fr := meta.FinishReason(); fr != "" {
			trace.FinishReason = fr
		}
		if m := meta.Model(); m != "" {
			trace.Model = m
		}
		if rid := meta.RunID(); rid != "" {
			trace.RunID = rid
		}
		if turn := meta.Turn(); turn > 0 {
			trace.Turn = turn
		}
	}

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

	r.aggregateLocked(trace)

	return trace
}

// classifyTrace derives the capture-time error class for a finalized trace.
// Successful calls (2xx, no transport error) get no class. The message comes
// from the transport error string (transport failures) or the response body
// (HTTP failures).
func classifyTrace(status int, errMsg, respBody string) ErrorClass {
	if isSuccess(status) && errMsg == "" {
		return ""
	}
	msg := errMsg
	if msg == "" && respBody != "" && !isSuccess(status) {
		msg = respBody
	}
	return ClassifyError(status, msg)
}

// aggregateLocked folds a completed trace into the per-provider-per-model
// metrics. Assumes r.mu is held.
func (r *Recorder) aggregateLocked(trace *HTTPTrace) {
	if trace == nil {
		return
	}
	key := providerModelKey{ProviderID: trace.ProviderID, Model: trace.Model}
	m := r.metrics[key]
	if m == nil {
		m = newProviderModelMetrics()
		r.metrics[key] = m
	}

	m.calls++
	if trace.Attempt > 0 {
		m.retries++
	}
	if !isSuccess(trace.Status) || trace.Error != "" {
		class := trace.ErrorClass
		if class == "" {
			class = ErrorClassOther
		}
		m.errors[class]++
		m.lastErrorClass = class
	}
	m.latency[latencyBucketFor(trace.DurationMs)]++
	if u := trace.Usage; u != nil && u.HasTokens() {
		m.promptTokens += u.PromptTokens
		m.completionTokens += u.CompletionTokens
		m.cacheReadTokens += u.CacheReadTokens
		m.cacheWriteTokens += u.CacheWriteTokens
		if u.CacheReadTokens > 0 {
			m.cacheHits++
		} else {
			m.cacheMisses++
		}
	}
	if !trace.Timestamp.IsZero() {
		m.lastCalled = trace.Timestamp
	}
}

// latencyBucketFor returns the index into latencyBuckets for a duration in ms.
func latencyBucketFor(ms int64) int {
	for i, b := range latencyBuckets {
		if b.Upper == 0 { // +inf bucket
			return i
		}
		if ms <= b.Upper {
			return i
		}
	}
	return len(latencyBuckets) - 1
}

// evictOldestLocked removes the oldest in-flight trace (by start timestamp)
// from the in-flight map and moves it to completed storage with an eviction
// marker, returning it so the caller can fire OnComplete after releasing the
// lock. Evicted traces do not update lastFailingTrace — they are a resource
// bound, not an LLM failure. Returns nil if there are no in-flight traces.
// Assumes r.mu is held.
func (r *Recorder) evictOldestLocked() *HTTPTrace {
	var oldest *HTTPTrace
	for _, t := range r.inFlight {
		if oldest == nil || t.Timestamp.Before(oldest.Timestamp) {
			oldest = t
		}
	}
	if oldest == nil {
		return nil
	}

	oldest.DurationMs = time.Since(oldest.Timestamp).Milliseconds()
	oldest.Error = "evicted: in-flight trace cap reached"
	delete(r.inFlight, oldest.ID)

	if len(r.traces) >= r.capacity {
		r.traces = r.traces[1:]
	}
	r.traces = append(r.traces, oldest)
	return oldest
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

// extractModel pulls the model name from an LLM request body. All supported
// provider wire formats (OpenAI /chat/completions, Anthropic /v1/messages,
// /responses) carry the model as a top-level JSON "model" field.
func extractModel(reqBody []byte) string {
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(reqBody, &payload); err != nil {
		return ""
	}
	return payload.Model
}

// RecordMeta carries optional per-call metadata for RecordWithMeta.
type RecordMeta struct {
	// Attempt is the zero-based retry attempt number of this call.
	Attempt int
	// RunID identifies the run this LLM call belongs to.
	RunID string
	// Turn is the 1-based turn number of this call within its run.
	Turn int
}

// Record records a complete (non-streaming) HTTP trace.
func (r *Recorder) Record(sessionID, providerID, method, url string, reqBody, respBody []byte, status int, duration time.Duration, errMsg string, respHeaders map[string][]string) {
	r.RecordWithMeta(sessionID, providerID, method, url, reqBody, respBody, status, duration, errMsg, respHeaders, RecordMeta{})
}

// RecordWithMeta records a complete (non-streaming) HTTP trace with optional
// per-call metadata (e.g. the retry attempt number).
func (r *Recorder) RecordWithMeta(sessionID, providerID, method, url string, reqBody, respBody []byte, status int, duration time.Duration, errMsg string, respHeaders map[string][]string, meta RecordMeta) {
	r.mu.Lock()

	id := r.nextTraceID()

	reqTruncated := truncateBody(reqBody)
	respTruncated := truncateBody(respBody)

	usage, finishReason, model := parseResponseEnrichment(respBody)

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
		Model:           extractModel(reqBody),
		Attempt:         meta.Attempt,
		RunID:           meta.RunID,
		Turn:            meta.Turn,
		Usage:           usage,
		FinishReason:    finishReason,
	}
	if model != "" && trace.Model == "" {
		trace.Model = model
	}
	trace.ErrorClass = classifyTrace(status, errMsg, trace.ResponseBody)

	if len(r.traces) >= r.capacity {
		r.traces = r.traces[1:]
	}
	r.traces = append(r.traces, trace)

	// Preserve non-2xx traces in a dedicated slot that is never evicted.
	if !isSuccess(status) || errMsg != "" {
		cp := *trace
		r.lastFailingTrace = &cp
	}
	r.aggregateLocked(trace)
	r.mu.Unlock()

	// Fire OnComplete outside the recorder lock so persistence (disk I/O)
	// never blocks trace recording or the request path.
	if r.OnComplete != nil {
		r.OnComplete(trace)
	}
}

// LoadAll bulk-inserts completed traces into the recorder.
// Used for restoring persisted traces on startup. Each trace is appended
// directly without calling OnComplete. Traces beyond capacity evict oldest.
// Restored traces also feed the interaction metrics, so the metrics endpoint
// reflects archived calls after a restart.
func (r *Recorder) LoadAll(traces []*HTTPTrace) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, trace := range traces {
		if len(r.traces) >= r.capacity {
			// Evict oldest
			r.traces = r.traces[1:]
		}
		r.traces = append(r.traces, trace)

		// Track last failing trace
		if trace.Status >= 300 && trace.Status != 0 {
			r.lastFailingTrace = trace
		}
		r.aggregateLocked(trace)
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

// Metrics returns a point-in-time snapshot of the per-provider-per-model
// interaction counters accumulated by this recorder. Providers and models are
// sorted by name so the JSON output is deterministic.
func (r *Recorder) Metrics() MetricsSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	snap := MetricsSnapshot{GeneratedAt: time.Now()}
	if len(r.metrics) == 0 {
		snap.Providers = []ProviderMetrics{}
		return snap
	}

	providers := make(map[string]*ProviderMetrics, len(r.metrics))
	for key, m := range r.metrics {
		pm := providers[key.ProviderID]
		if pm == nil {
			pm = &ProviderMetrics{ProviderID: key.ProviderID}
			providers[key.ProviderID] = pm
		}

		latency := make(map[string]int64, len(latencyBuckets))
		for i, b := range latencyBuckets {
			latency[b.Label] = m.latency[i]
		}
		errors := make(map[ErrorClass]int, len(m.errors))
		errorTotal := 0
		for _, class := range allErrorClasses {
			n := m.errors[class]
			errors[class] = n
			errorTotal += n
		}

		mm := ModelMetrics{
			Model:   key.Model,
			Calls:   m.calls,
			Retries: m.retries,
			Errors:  errors,
			Latency: latency,
			Tokens: UsageTotals{
				PromptTokens:     m.promptTokens,
				CompletionTokens: m.completionTokens,
				CacheReadTokens:  m.cacheReadTokens,
				CacheWriteTokens: m.cacheWriteTokens,
				TotalTokens:      m.promptTokens + m.completionTokens + m.cacheReadTokens + m.cacheWriteTokens,
			},
			Cache:      CacheCounts{Hits: m.cacheHits, Misses: m.cacheMisses},
			LastCalled: m.lastCalled,
			LastError:  m.lastErrorClass,
		}
		pm.Models = append(pm.Models, mm)
		pm.TotalCalls += m.calls
		snap.TotalCalls += m.calls
		snap.TotalErrors += errorTotal
	}

	snap.Providers = make([]ProviderMetrics, 0, len(providers))
	for _, pm := range providers {
		sort.Slice(pm.Models, func(i, j int) bool { return pm.Models[i].Model < pm.Models[j].Model })
		snap.Providers = append(snap.Providers, *pm)
	}
	sort.Slice(snap.Providers, func(i, j int) bool { return snap.Providers[i].ProviderID < snap.Providers[j].ProviderID })
	return snap
}

// ————— RoundTripper —————

// attemptContextKey is the context key carrying the zero-based retry attempt
// number of the current LLM call. The agent loop stamps it per attempt so the
// recorder can distinguish initial calls from retries.
type attemptContextKey struct{}

// WithAttempt returns a context that carries the retry attempt number. The
// attempt is stamped onto the trace at capture time and feeds the retry
// counter in the interaction metrics.
func WithAttempt(ctx context.Context, attempt int) context.Context {
	return context.WithValue(ctx, attemptContextKey{}, attempt)
}

// AttemptFromContext extracts the retry attempt number from a context,
// returning 0 when absent.
func AttemptFromContext(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	if v, ok := ctx.Value(attemptContextKey{}).(int); ok {
		return v
	}
	return 0
}

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

// metaRunID returns the run ID recorded on a TraceMeta, or "" when nil.
func metaRunID(meta *TraceMeta) string {
	if meta == nil {
		return ""
	}
	return meta.RunID()
}

// metaTurn returns the 1-based turn number recorded on a TraceMeta, or 0 when nil.
func metaTurn(meta *TraceMeta) int {
	if meta == nil {
		return 0
	}
	return meta.Turn()
}

func (rt *RecordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Read and buffer request body for recording
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}

	// The run loop attaches a TraceMeta to the request context so the recorded
	// trace can be enriched with per-call measurements (retry attempt, TTFB,
	// TTFT, run/turn correlation IDs, provider-parsed usage/finish_reason/model).
	meta := TraceMetaFromContext(req.Context())

	start := time.Now()
	if meta != nil {
		meta.SetRequestStart(start)
	}
	// Correlation IDs: the run loop stamps the run ID and 1-based turn number
	// on the TraceMeta for every turn, so the trace can later be joined back to
	// its turn by ID (issue #988).
	traceID := rt.recorder.startTrace(rt.sessionID, rt.providerID, req.Method, req.URL.Path, reqBody, AttemptFromContext(req.Context()), metaRunID(meta), metaTurn(meta))
	if meta != nil {
		meta.SetTraceID(traceID)
	}

	resp, err := rt.inner.RoundTrip(req)

	if err != nil {
		duration := time.Since(start)
		rt.recorder.completeTraceWithMeta(traceID, nil, 0, duration, err.Error(), nil, nil, "", meta)
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
		meta:        meta,
	}

	return resp, nil
}

// traceBody wraps an io.ReadCloser, captures up to MaxBodyBytes of the response
// plus a rolling tail window (for stream tails), measures time-to-first-byte,
// and completes the trace when Close() is called.
type traceBody struct {
	io.ReadCloser
	recorder    *Recorder
	traceID     TraceID
	startTime   time.Time
	status      int
	respHeaders map[string][]string
	meta        *TraceMeta

	mu        sync.Mutex
	buf       bytes.Buffer
	total     int64
	tail      []byte
	done      bool
	closeErr  error
	firstByte bool
}

func (tb *traceBody) Read(p []byte) (int, error) {
	n, err := tb.ReadCloser.Read(p)
	if n > 0 {
		tb.mu.Lock()
		if !tb.firstByte {
			tb.firstByte = true
			if tb.meta != nil {
				tb.meta.SetTTFBMs(time.Since(tb.startTime).Milliseconds())
			}
		}
		tb.total += int64(n)
		if tb.buf.Len() < MaxBodyBytes {
			remaining := MaxBodyBytes - tb.buf.Len()
			writeLen := n
			if writeLen > remaining {
				writeLen = remaining
			}
			tb.buf.Write(p[:writeLen])
		}
		tb.tail = append(tb.tail, p[:n]...)
		if len(tb.tail) > maxTailBytes {
			tb.tail = append([]byte(nil), tb.tail[len(tb.tail)-maxTailBytes:]...)
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
	// Parse usage/finish_reason from the head plus the stream tail, so
	// streaming responses longer than the body cap still yield their tail
	// enrichment (usage and finish_reason live in the final SSE chunks). The
	// TraceMeta bridge then overrides with the run loop's authoritative values.
	var parseSrc []byte
	if tb.total <= int64(MaxBodyBytes) {
		parseSrc = tb.buf.Bytes()
	} else {
		parseSrc = append(truncateBody(tb.buf.Bytes()), tb.tail...)
	}
	usage, finishReason, _ := parseResponseEnrichment(parseSrc)

	tb.recorder.completeTraceWithMeta(tb.traceID, tb.buf.Bytes(), tb.status, duration, errStr, tb.respHeaders, usage, finishReason, tb.meta)
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
