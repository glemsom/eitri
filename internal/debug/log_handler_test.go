package debug

import (
	"log/slog"
	"strings"
	"testing"
)

func TestRingBufferHandler_BasicLogging(t *testing.T) {
	var buf strings.Builder
	inner := slog.NewJSONHandler(&buf, nil)
	h := NewRingBufferHandler(inner, 10)

	logger := slog.New(h)
	logger.Info("hello world", "key1", "val1")

	entries := h.Entries()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Message != "hello world" {
		t.Fatalf("message = %q, want %q", entries[0].Message, "hello world")
	}
	if entries[0].Level != "INFO" {
		t.Fatalf("level = %q, want %q", entries[0].Level, "INFO")
	}
	if entries[0].Attrs["key1"] != "val1" {
		t.Fatalf("attrs[key1] = %v, want %v", entries[0].Attrs["key1"], "val1")
	}

	// Verify the inner handler also received the log entry
	if !strings.Contains(buf.String(), "hello world") {
		t.Fatalf("inner handler did not receive the log entry")
	}
}

func TestRingBufferHandler_Eviction(t *testing.T) {
	var buf strings.Builder
	inner := slog.NewJSONHandler(&buf, nil)
	h := NewRingBufferHandler(inner, 5)

	logger := slog.New(h)
	for i := 0; i < 10; i++ {
		logger.Info("msg", "i", i)
	}

	entries := h.Entries()
	if len(entries) != 5 {
		t.Fatalf("got %d entries, want 5 (capacity)", len(entries))
	}

	// The ring buffer should contain the last 5 messages
	for _, e := range entries {
		if e.Message != "msg" {
			t.Fatalf("unexpected message: %q", e.Message)
		}
	}
	// First entry should be i=5 (the 6th log, 0-indexed)
	firstI := entries[0].Attrs["i"]
	if firstI != int64(5) {
		t.Fatalf("first entry's i = %v (type %T), want 5", firstI, firstI)
	}
}

func TestRingBufferHandler_Levels(t *testing.T) {
	var buf strings.Builder
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := NewRingBufferHandler(inner, 10)

	logger := slog.New(h)
	logger.Debug("debug msg")
	logger.Info("info msg")
	logger.Warn("warn msg")
	logger.Error("error msg")

	entries := h.Entries()
	if len(entries) != 4 {
		t.Fatalf("got %d entries, want 4", len(entries))
	}

	levels := []string{"DEBUG", "INFO", "WARN", "ERROR"}
	for i, e := range entries {
		if e.Level != levels[i] {
			t.Fatalf("entry %d level = %q, want %q", i, e.Level, levels[i])
		}
	}
}

func TestRingBufferHandler_DefaultCapacity(t *testing.T) {
	var buf strings.Builder
	inner := slog.NewJSONHandler(&buf, nil)
	h := NewRingBufferHandler(inner, 0) // should use default

	if h.capacity != DefaultLogCapacity {
		t.Fatalf("capacity = %d, want %d", h.capacity, DefaultLogCapacity)
	}
}

func TestRingBufferHandler_NoPanic(t *testing.T) {
	var buf strings.Builder
	inner := slog.NewJSONHandler(&buf, nil)
	h := NewRingBufferHandler(inner, 10)

	logger := slog.New(h)
	logger.Info("msg1")
	logger.Info("msg2", "k1", "v1", "k2", 42, "k3", true, "k4", []string{"a", "b"})

	entries := h.Entries()
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
}

func TestRingBufferHandler_Concurrency(t *testing.T) {
	var buf strings.Builder
	inner := slog.NewJSONHandler(&buf, nil)
	h := NewRingBufferHandler(inner, 100)

	logger := slog.New(h)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			logger.Info("concurrent", "i", i)
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 50; i++ {
			logger.Info("concurrent", "i", i)
		}
		done <- struct{}{}
	}()

	<-done
	<-done

	entries := h.Entries()
	if len(entries) != 100 {
		t.Fatalf("got %d entries, want 100 (full capacity)", len(entries))
	}
}
