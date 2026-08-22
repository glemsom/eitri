package tui

import (
	"context"
	"testing"
)

func TestLayoutCache_hitTestsReuseRecordedIndex(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		WorkspacePath: "/tmp/acme",
		Events:        NewEventFeed(),
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

	m.tx.toolEntryAtLine(0) // result irrelevant; the build is what we're asserting
	if m.tx.layout.builds != 1 {
		t.Fatalf("first hit-test must build the layout exactly once, got %d builds", m.tx.layout.builds)
	}

	for i := 0; i < 20; i++ {
		m.tx.toolEntryAtLine(0)
	}
	if m.tx.layout.builds != 1 {
		t.Fatalf("repeated toolEntryAtLine must not re-run layout, got %d builds", m.tx.layout.builds)
	}

	for i := 0; i < 20; i++ {
		m.mouseToContent(2, 3)
	}
	if m.tx.layout.builds != 1 {
		t.Fatalf("repeated mouseToContent must not re-run layout, got %d builds", m.tx.layout.builds)
	}
}

func TestLayoutCache_recordsRowMessageIndex(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		WorkspacePath: "/tmp/acme",
		Events:        NewEventFeed(),
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
	if len(m.tx.layout.plain) == 0 {
		t.Fatalf("plain-row space must be recorded for message spans to index")
	}
	for _, r := range m.tx.layout.msgs {
		if r.start > r.end || r.end >= len(m.tx.layout.plain) {
			t.Fatalf("message span [%d,%d] out of bounds of %d plain rows", r.start, r.end, len(m.tx.layout.plain))
		}
	}
	if len(m.tx.layout.rows) == 0 {
		t.Fatalf("row->tool-entry index must also be recorded")
	}
}

func TestLayoutCache_messageAtLineConsumesRowIndex(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		WorkspacePath: "/tmp/acme",
		Events:        NewEventFeed(),
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	m = toolResult(t, m, ToolResult{Name: "bash", Result: "full output line one\nfull output line two", Lines: 2})
	m.tx.messageAtLine(0) // build the layout once

	base := m.tx.layout.builds
	first := m.tx.layout.msgs[0]
	if idx, ok := m.tx.messageAtLine(first.start); !ok || idx != first.idx {
		t.Fatalf("messageAtLine(%d) = %d/%v, want message %d", first.start, idx, ok, first.idx)
	}
	if _, ok := m.tx.messageAtLine(0); ok {
		t.Errorf("row 0 (workspace header) must not map to a message, got ok=true")
	}
	if m.tx.layout.builds != base {
		t.Fatalf("messageAtLine re-ran layout: builds %d -> %d", base, m.tx.layout.builds)
	}
}
