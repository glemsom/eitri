package provider

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// TestSSEParserSplitsEvents verifies the SSE scanner yields one event per
// logical event (data lines joined), ignoring comment-only events and blank
// separators, and stops at EOF.
func TestSSEParserSplitsEvents(t *testing.T) {
	in := `data: {"a":1}

: a comment only

data: {"b":2}
data: {"b":"two lines",}

data: [DONE]
`
	r := newSSE(strings.NewReader(in))
	var events []string
	for {
		e, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("sse Next() error = %v, want nil or EOF", err)
		}
		events = append(events, e.data)
	}
	if len(events) != 3 {
		t.Fatalf("parsed %d events, want 3: %v", len(events), events)
	}
	if events[0] != `{"a":1}` {
		t.Fatalf("event[0] = %q, want %q", events[0], `{"a":1}`)
	}
	if events[1] != "{\"b\":2}\n{\"b\":\"two lines\",}" {
		t.Fatalf("event[1] = %q, want data lines joined with a newline", events[1])
	}
	if events[2] != "[DONE]" {
		t.Fatalf("event[2] = %q, want [DONE]", events[2])
	}
}

// TestSSEParserEndsAtEOF verifies the scanner terminates cleanly on a body with
// no trailing blank line.
func TestSSEParserEndsAtEOF(t *testing.T) {
	r := newSSE(strings.NewReader("data: only-one\n"))
	got, err := r.Next()
	if err != nil {
		t.Fatalf("sse Next() error = %v, want nil", err)
	}
	if got.data != "only-one" {
		t.Fatalf("event data = %q, want %q", got.data, "only-one")
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second Next() error = %v, want io.EOF", err)
	}
}
