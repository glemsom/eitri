package debug

import (
	"context"
	"sync"
)

// traceMetaContextKey is the context key under which a *TraceMeta is carried.
type traceMetaContextKey struct{}

// TraceMeta is a per-LLM-call bridge between the run loop and the HTTP trace
// recorder. The run loop attaches one to the request context before starting a
// streaming provider call; the RecordingRoundTripper picks it up, records the
// trace ID and time-to-first-byte, and the loop records the provider-parsed
// usage, finish reason, and model. When the stream closes and the trace is
// finalized, the recorder merges these measurements into the trace.
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
	attempt      int
	usage        *UsageTotals
	finishReason string
	model        string
	ttfbMs       int64
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
