package debug

import (
	"context"
	"sync"
	"time"
)

// traceMetaContextKey is the context key under which a *TraceMeta is carried.
type traceMetaContextKey struct{}

// TraceMeta is a per-LLM-call bridge between the run loop and the HTTP trace
// recorder. The run loop attaches one to the request context before starting a
// streaming provider call; the RecordingRoundTripper picks it up, records the
// trace ID, time-to-first-byte, and the request start time, and the loop
// records the provider-parsed usage, finish reason, model, run/turn IDs, and
// time-to-first-token. When the stream closes and the trace is finalized, the
// recorder merges these measurements into the trace.
//
// The same pointer is reused across retries of a single turn: the loop updates
// the attempt number before each retry. Because the recorder copies the values
// at finalize time, updating the meta after a trace has completed never mutates
// an already-recorded trace.
//
// All accessors are safe for concurrent use.
type TraceMeta struct {
	mu           sync.Mutex
	traceID      TraceID
	runID        string
	turn         int
	attempt      int
	usage        *UsageTotals
	finishReason string
	model        string
	ttfbMs       int64
	requestStart time.Time
	firstToken   time.Time
}

// WithTraceMeta returns a copy of ctx carrying the given TraceMeta. Callers
// pass the returned context to the LLM client so the recording round-tripper
// can enrich the HTTP trace for the request.
func WithTraceMeta(ctx context.Context, meta *TraceMeta) context.Context {
	return context.WithValue(ctx, traceMetaContextKey{}, meta)
}

// TraceMetaFromContext returns the TraceMeta attached to ctx, or nil if none
// was attached (e.g. requests that bypass the run loop).
func TraceMetaFromContext(ctx context.Context) *TraceMeta {
	if ctx == nil {
		return nil
	}
	meta, _ := ctx.Value(traceMetaContextKey{}).(*TraceMeta)
	return meta
}

// SetTraceID records the trace ID assigned by the recorder for the request.
func (m *TraceMeta) SetTraceID(id TraceID) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.traceID = id
	m.mu.Unlock()
}

// TraceID returns the recorder-assigned trace ID for the request.
func (m *TraceMeta) TraceID() TraceID {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.traceID
}

// SetRunID records the run ID this LLM call belongs to.
func (m *TraceMeta) SetRunID(runID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.runID = runID
	m.mu.Unlock()
}

// RunID returns the run ID this LLM call belongs to, or "" when unknown.
func (m *TraceMeta) RunID() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runID
}

// SetTurn records the 1-based turn number of this LLM call within its run.
func (m *TraceMeta) SetTurn(turn int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.turn = turn
	m.mu.Unlock()
}

// Turn returns the 1-based turn number of this LLM call, or 0 when unknown.
func (m *TraceMeta) Turn() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.turn
}

// SetAttempt records the zero-based retry attempt number for the request.
func (m *TraceMeta) SetAttempt(attempt int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.attempt = attempt
	m.mu.Unlock()
}

// Attempt returns the retry attempt number for the request.
func (m *TraceMeta) Attempt() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.attempt
}

// SetUsage records the provider-reported token usage parsed from the stream.
func (m *TraceMeta) SetUsage(usage *UsageTotals) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.usage = usage
	m.mu.Unlock()
}

// Usage returns the provider-reported token usage, or nil if none was parsed.
func (m *TraceMeta) Usage() *UsageTotals {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usage
}

// SetFinishReason records the provider-reported finish reason for the response.
func (m *TraceMeta) SetFinishReason(reason string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.finishReason = reason
	m.mu.Unlock()
}

// FinishReason returns the provider-reported finish reason, or "" if unknown.
func (m *TraceMeta) FinishReason() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.finishReason
}

// SetModel records the model name that produced the response.
func (m *TraceMeta) SetModel(model string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.model = model
	m.mu.Unlock()
}

// Model returns the model name that produced the response.
func (m *TraceMeta) Model() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.model
}

// SetTTFBMs records the time-to-first-byte in milliseconds.
func (m *TraceMeta) SetTTFBMs(ms int64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.ttfbMs = ms
	m.mu.Unlock()
}

// TTFBMs returns the time-to-first-byte in milliseconds.
func (m *TraceMeta) TTFBMs() int64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ttfbMs
}

// SetRequestStart records when the HTTP request was initiated. The recorder
// combines this with SetFirstTokenTime to derive time-to-first-token.
func (m *TraceMeta) SetRequestStart(t time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.requestStart = t
	m.mu.Unlock()
}

// RequestStart returns when the HTTP request was initiated, or the zero time
// when unknown.
func (m *TraceMeta) RequestStart() time.Time {
	if m == nil {
		return time.Time{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requestStart
}

// SetFirstTokenTime records when the first content token arrived from the
// stream. Only the first recorded time is kept; later calls are ignored so a
// single attempt never reports multiple first tokens. The loop resets the
// anchor between attempts via ResetFirstToken so each attempt's trace measures
// its own time-to-first-token (issue #988).
func (m *TraceMeta) SetFirstTokenTime(t time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.firstToken.IsZero() {
		m.firstToken = t
	}
	m.mu.Unlock()
}

// ResetFirstToken clears the first-token anchor, so the next content token
// recorded after a retry restarts the time-to-first-token measurement. The
// request-start anchor is also cleared; the recorder re-stamps it on the next
// request.
func (m *TraceMeta) ResetFirstToken() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.firstToken = time.Time{}
	m.requestStart = time.Time{}
	m.mu.Unlock()
}

// FirstTokenTime returns when the first token arrived, or the zero time when
// no token has been recorded yet.
func (m *TraceMeta) FirstTokenTime() time.Time {
	if m == nil {
		return time.Time{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.firstToken
}

// TTFTMs returns the time-to-first-token in milliseconds (from request start
// to the first content/reasoning token), or 0 when either endpoint is unknown.
func (m *TraceMeta) TTFTMs() int64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.requestStart.IsZero() || m.firstToken.IsZero() {
		return 0
	}
	return m.firstToken.Sub(m.requestStart).Milliseconds()
}
