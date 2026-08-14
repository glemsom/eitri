package tui

import (
	"context"
	"strings"
	"testing"
)

// TestModel_readRangeShownInEntryHead asserts a `read` call invoked with
// explicit start_line/end_line limits renders the invoked range in the one-line
// entry head alongside the path hint — `⊕ read  path:start-end` (issue #204
// AC1) — so a range-limited read is distinguishable at a glance from a
// whole-file dump.
func TestModel_readRangeShownInEntryHead(t *testing.T) {
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn:  func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Tools: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "read it")
	m = submitAndWait(t, m)

	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{
		Name: "read",
		Args: `{"path":"internal/main.go","start_line":12,"end_line":340}`,
	}})

	content := view(m)
	if !strings.Contains(content, "⊕ read") {
		t.Errorf("expected a one-line read entry, got: %q", content)
	}
	if !strings.Contains(content, "internal/main.go:12-340") {
		t.Errorf("expected path:start-end range in the entry head, got: %q", content)
	}
}

// TestModel_readWholeFileHasNoRange asserts a `read` call with omitted or null
// limits renders today's path-only head with no range tag (issue #204 AC2).
func TestModel_readWholeFileHasNoRange(t *testing.T) {
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn:  func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Tools: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "read it")
	m = submitAndWait(t, m)

	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{
		Name: "read",
		Args: `{"path":"internal/main.go"}`,
	}})
	content := view(m)
	if !strings.Contains(content, "internal/main.go") {
		t.Errorf("expected the path hint, got: %q", content)
	}
	if strings.Contains(content, ":12-340") {
		t.Errorf("whole-file read must not show a range tag, got: %q", content)
	}

	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{
		Name: "read",
		Args: `{"path":"internal/main.go","start_line":null,"end_line":null}`,
	}})
	content = view(m)
	if strings.Contains(content, ":12-340") || strings.Contains(content, ":-") {
		t.Errorf("null limits must not show a range tag, got: %q", content)
	}
}

// TestModel_readMalformedArgsFallBackToPath asserts malformed or unexpected
// argument shapes fall back to the current path-only rendering without a crash
// (issue #204 AC3): a single explicit limit, string-typed limits, and invalid
// JSON all render the plain path hint.
func TestModel_readMalformedArgsFallBackToPath(t *testing.T) {
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn:  func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Tools: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "read it")
	m = submitAndWait(t, m)

	cases := []string{
		`{"path":"a.go","start_line":12}`,                    // only one limit
		`{"path":"a.go","start_line":"12","end_line":"340"}`, // string limits
		`{"path":"a.go","start_line":12.5,"end_line":340}`,   // fractional limit
		`not json`, // malformed JSON
		`{"path":"a.go","start_line":-3,"end_line":340}`, // non-positive limit
	}
	for _, args := range cases {
		m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "read", Args: args}})
		content := view(m)
		if !strings.Contains(content, "a.go") {
			t.Errorf("args %q: expected the path hint, got: %q", args, content)
		}
		if strings.Contains(content, ":12-340") || strings.Contains(content, ":-") || strings.Contains(content, "12.5-") {
			t.Errorf("args %q: malformed limits must not render a range tag, got: %q", args, content)
		}
	}
}

// TestModel_clipboardCopyIncludesReadRange asserts the clipboard copy of the
// transcript carries the same range tag as the on-screen entry head (issue #204
// AC4).
func TestModel_clipboardCopyIncludesReadRange(t *testing.T) {
	var copied string
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn:      func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Tools:     feed,
		Clipboard: func(s string) error { copied = s; return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "read it")
	m = submitAndWait(t, m)

	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{
		Name: "read",
		Args: `{"path":"internal/main.go","start_line":12,"end_line":340}`,
	}})
	m = keypressCtrlO(t, m)

	if !strings.Contains(copied, "internal/main.go:12-340") {
		t.Errorf("clipboard copy must include the read range, got: %q", copied)
	}
}
