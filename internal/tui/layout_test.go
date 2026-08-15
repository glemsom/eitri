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
		Turn: func(ctx context.Context, prompt string, _ SkillInject) (TurnResult, error) {
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

	if m.tx.layout.builds != 0 {
		t.Fatalf("layout cache should start unbuilt, got %d builds", m.tx.layout.builds)
	}

	// The first hit-test performs the single layout build and records the index.
	m.tx.toolEntryAtLine(0) // result irrelevant; the build is what we're asserting
	if m.tx.layout.builds != 1 {
		t.Fatalf("first hit-test must build the layout exactly once, got %d builds", m.tx.layout.builds)
	}

	// Repeated toolEntryAtLine calls reuse the recorded index, never re-run layout.
	for i := 0; i < 20; i++ {
		m.tx.toolEntryAtLine(0)
	}
	if m.tx.layout.builds != 1 {
		t.Fatalf("repeated toolEntryAtLine must not re-run layout, got %d builds", m.tx.layout.builds)
	}

	// The mouse-to-content path also reads the recorded plain-row space, so a
	// drag's motion events stop at the first build too.
	for i := 0; i < 20; i++ {
		m.mouseToContent(2, 3)
	}
	if m.tx.layout.builds != 1 {
		t.Fatalf("repeated mouseToContent must not re-run layout, got %d builds", m.tx.layout.builds)
	}
}

// TestLayoutCache_recordsRowMessageIndex asserts the batched render also records
// the row->message index (issue #242 AC1) alongside the tool-entry index, so the
// persistent layout records every owner the transcript renders.
func TestLayoutCache_recordsRowMessageIndex(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ SkillInject) (TurnResult, error) {
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
	m.tx.toolEntryAtLine(0) // build the layout once

	if len(m.tx.layout.msgs) == 0 {
		t.Fatalf("row->message index must be recorded, got 0 message spans")
	}
	// Every span must be a valid, non-empty content-line range onto plain rows.
	if len(m.tx.layout.plain) == 0 {
		t.Fatalf("plain-row space must be recorded for message spans to index")
	}
	for _, r := range m.tx.layout.msgs {
		if r.start > r.end || r.end >= len(m.tx.layout.plain) {
			t.Fatalf("message span [%d,%d] out of bounds of %d plain rows", r.start, r.end, len(m.tx.layout.plain))
		}
	}
	// The tool entry must also be recorded for the null hypothesis (the cache is
	// the thing under test, not whether tools exist).
	if len(m.tx.layout.rows) == 0 {
		t.Fatalf("row->tool-entry index must also be recorded")
	}
}

// TestLayoutCache_messageAtLineConsumesRowIndex asserts the row->message index
// (issue #242 AC1) is a live, consumable surface: messageAtLine maps a rendered
// row back to its owning message via the recorded index, and reports ok=false
// for a non-message row (the workspace header) without re-building layout.
func TestLayoutCache_messageAtLineConsumesRowIndex(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ SkillInject) (TurnResult, error) {
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
	m.tx.messageAtLine(0) // build the layout once

	base := m.tx.layout.builds
	// The prompt row and a later answer row must map to message 0 (the single
	// committed turn); the workspace header (row 0) must not map to any message.
	first := m.tx.layout.msgs[0]
	if idx, ok := m.tx.messageAtLine(first.start); !ok || idx != first.idx {
		t.Fatalf("messageAtLine(%d) = %d/%v, want message %d", first.start, idx, ok, first.idx)
	}
	if _, ok := m.tx.messageAtLine(0); ok {
		t.Errorf("row 0 (workspace header) must not map to a message, got ok=true")
	}
	// Consuming the index must not re-run the layout pass.
	if m.tx.layout.builds != base {
		t.Fatalf("messageAtLine re-ran layout: builds %d -> %d", base, m.tx.layout.builds)
	}
}
