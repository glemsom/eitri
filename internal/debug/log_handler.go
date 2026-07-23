package debug

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// LogEntry represents a single structured log entry captured in the ring buffer.
type LogEntry struct {
	Timestamp time.Time      `json:"timestamp"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Attrs     map[string]any `json:"attrs,omitempty"`
}

// DefaultLogCapacity is the default number of log entries to retain.
const DefaultLogCapacity = 100

// RingBufferHandler is an slog.Handler wrapper that keeps a ring buffer
// of the last N log entries. It delegates to the underlying handler for
// normal logging.
type RingBufferHandler struct {
	inner    slog.Handler
	mu       sync.Mutex
	entries  []LogEntry
	capacity int
	next     int // next write position in the ring buffer
	count    int // total entries written (for ring buffer wrap detection)
}

// NewRingBufferHandler creates a new RingBufferHandler with the given capacity.
// If capacity <= 0, DefaultLogCapacity is used.
func NewRingBufferHandler(inner slog.Handler, capacity int) *RingBufferHandler {
	if capacity <= 0 {
		capacity = DefaultLogCapacity
	}
	return &RingBufferHandler{
		inner:    inner,
		entries:  make([]LogEntry, capacity),
		capacity: capacity,
	}
}

// Enabled implements slog.Handler.Enabled.
func (h *RingBufferHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle implements slog.Handler.Handle.
func (h *RingBufferHandler) Handle(ctx context.Context, record slog.Record) error {
	// Build the log entry
	entry := LogEntry{
		Timestamp: record.Time,
		Level:     record.Level.String(),
		Message:   record.Message,
		Attrs:     make(map[string]any),
	}

	record.Attrs(func(a slog.Attr) bool {
		entry.Attrs[a.Key] = a.Value.Any()
		return true
	})

	// Store in the ring buffer
	h.mu.Lock()
	h.entries[h.next] = entry
	h.next = (h.next + 1) % h.capacity
	h.count++
	h.mu.Unlock()

	// Delegate to the inner handler
	return h.inner.Handle(ctx, record)
}

// WithAttrs implements slog.Handler.WithAttrs.
func (h *RingBufferHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &RingBufferHandler{
		inner:    h.inner.WithAttrs(attrs),
		entries:  make([]LogEntry, h.capacity),
		capacity: h.capacity,
	}
}

// WithGroup implements slog.Handler.WithGroup.
func (h *RingBufferHandler) WithGroup(name string) slog.Handler {
	return &RingBufferHandler{
		inner:    h.inner.WithGroup(name),
		entries:  make([]LogEntry, h.capacity),
		capacity: h.capacity,
	}
}

// Entries returns a copy of the buffered log entries in chronological order.
func (h *RingBufferHandler) Entries() []LogEntry {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.count == 0 {
		return nil
	}

	// If we haven't filled the buffer yet, entries are at [0, count)
	if h.count < h.capacity {
		result := make([]LogEntry, h.count)
		copy(result, h.entries[:h.count])
		return result
	}

	// Buffer has wrapped; reconstruct chronological order
	result := make([]LogEntry, h.capacity)
	// Copy from next to end
	afterNext := h.capacity - h.next
	copy(result[:afterNext], h.entries[h.next:])
	// Copy from start to next
	copy(result[afterNext:], h.entries[:h.next])
	return result
}

// Count returns the number of buffered entries.
func (h *RingBufferHandler) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.count < h.capacity {
		return h.count
	}
	return h.capacity
}
