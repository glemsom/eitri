package tui

import (
	"context"
	"testing"
)

// This file covers the persistent transcript layout cache (issue #242): one
// batched render pass records a row->tool-entry and row->message space that the
// mouse hit-test reads back instead of re-deriving layout on every pointer /
// selection event. Tests assert the cache is built lazily once and reused, so a
// drag no longer re-runs the full renderHistory pass per motion event.

// TestLayoutCache_hitTestsReuseRecordedIndex asserts the hit-test reads the
// recorded layout index instead of re-deriving layout each call: after the
// cache is built once (on the first hit-test), repeated toolEntryAtLine and
// mouseToContent calls must not re-run the layout pass (issue #242 AC3/AC4).
func TestLayoutCache_hitTestsReuseRecordedIndex(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		WorkspacePath: "/tmp/acme",
		Tools:         NewToolFeed(),
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	m = toolResult(t, m, ToolResult{Name: "bash", Result: "full output line one\nfull output line two", Lines: 2})
	view(m) // hydrate the persisted viewport; the value-copy cache discards

	if m.layoutBuilds != 0 {
		t.Fatalf("layout cache should start unbuilt, got %d builds", m.layoutBuilds)
	}

	// The first hit-test performs the single layout build and records the index.
	m.toolEntryAtLine(0) // result irrelevant; the build is what we're asserting
	if m.layoutBuilds != 1 {
		t.Fatalf("first hit-test must build the layout exactly once, got %d builds", m.layoutBuilds)
	}

	// Repeated toolEntryAtLine calls reuse the recorded index, never re-run layout.
	for i := 0; i < 20; i++ {
		m.toolEntryAtLine(0)
	}
	if m.layoutBuilds != 1 {
		t.Fatalf("repeated toolEntryAtLine must not re-run layout, got %d builds", m.layoutBuilds)
	}

	// The mouse-to-content path also reads the recorded plain-row space, so a
	// drag's motion events stop at the first build too.
	for i := 0; i < 20; i++ {
		m.mouseToContent(2, 3)
	}
	if m.layoutBuilds != 1 {
		t.Fatalf("repeated mouseToContent must not re-run layout, got %d builds", m.layoutBuilds)
	}
}
