package tui

import (
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

func busyCacheTranscript(delta string) (*Transcript, *TurnSession) {
	s := NewTurnSession(nil)
	th := themeFor(config.DefaultTheme)
	tx := &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           100,
		height:          30,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		pulse:           busyPulse(),
	}
	log := toolLog{}
	log.SetAnchor(0)
	log.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}})
	log.Apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: "alpha", Lines: 1}})

	// A couple of committed turns so the prefix is non-trivial.
	tx.messages = append(tx.messages,
		message{role: "you", content: "turn one"},
		message{role: "eitri", content: "answer one", events: synthAnswerLog("answer one")},
		message{role: "you", content: "turn two"},
		message{role: "eitri", content: "answer two", events: synthAnswerLog("answer two")},
	)

	tx.messages = append(tx.messages,
		message{role: "you", content: "live prompt"},
		message{role: "eitri", streaming: true, thinkingRequested: true,
			reasoning: delta, content: "", expansion: ExpansionState{}},
	)
	tx.log = log

	if delta != "" {
		tx.flow.Observe(ReasoningStream, delta)
	}
	return tx, s
}

func TestBusyRender_concatenatedMatchesFullRender(t *testing.T) {
	tx, s := busyCacheTranscript("")
	_ = s

	for i := 0; i < 3; i++ {
		reasoning := tx.messages[4].reasoning + string(rune('a'+i))
		tx.syncStreamSnapshots(4, "", reasoning)
		tx.flow.Observe(ReasoningStream, string(rune('a'+i)))

		got := tx.renderPaneContent()
		var full strings.Builder
		tx.renderHistory(&full, nil, nil)
		if got != full.String() {
			t.Fatalf("delta %d: busy concatenation diverged from full render\n--- got ---\n%s\n--- want ---\n%s", i, got, full.String())
		}
	}
}

func TestBusyRender_prefixCachedAcrossDeltas(t *testing.T) {
	tx, s := busyCacheTranscript("")
	_ = s

	tx.renderPaneContent()
	prefix := tx.busyPrefix
	if prefix == "" {
		t.Fatal("busy path must build a prefix")
	}
	if !strings.Contains(ansiStrip(prefix), "answer two") {
		t.Fatalf("prefix must contain the committed history, got %q", ansiStrip(prefix))
	}

	tx.syncStreamSnapshots(4, "", "more reasoning")
	if tx.busyPrefixDirty {
		t.Error("a stream delta must not dirty the committed-prefix cache")
	}
	tx.renderPaneContent()
	if tx.busyPrefix != prefix {
		t.Error("committed prefix must be byte-stable across stream deltas")
	}
}

func TestBusyRender_committedMutationInvalidatesPrefix(t *testing.T) {
	tx, s := busyCacheTranscript("")
	_ = s

	tx.renderPaneContent()
	if tx.busyPrefixDirty {
		t.Fatal("precondition: fresh cache is clean")
	}

	tx.applyTool(ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"b.txt"}`}})
	if !tx.busyPrefixDirty {
		t.Error("a committed-history change must invalidate the prefix")
	}

	got := tx.renderPaneContent()
	var full strings.Builder
	tx.renderHistory(&full, nil, nil)
	if got != full.String() {
		t.Errorf("prefix rebuild diverged from full render\n--- got ---\n%s\n--- want ---\n%s", got, full.String())
	}
}
