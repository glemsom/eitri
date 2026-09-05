package tui

import (
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// busyCacheTranscript builds a busy transcript with a committed history followed
// by a running live turn: committed turns plus one in-progress streaming turn.
func busyCacheTranscript(delta string) (*Transcript, *TurnSession) {
	th := themeFor(config.DefaultTheme)
	tx := &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           100,
		height:          30,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		busy:            true,
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

	// The live turn's prompt + its streaming reply.
	tx.messages = append(tx.messages,
		message{role: "you", content: "live prompt"},
		message{role: "eitri", streaming: true, thinkingRequested: true,
			reasoning: delta, content: "", expansion: ExpansionState{}},
	)
	tx.log = log

	s := NewTurnSession(nil)
	if delta != "" {
		s.flow.Observe(ReasoningStream, delta)
	}
	tx.live = s
	return tx, s
}

// TestBusyRender_concatenatedMatchesFullRender locks the byte-identical
// requirement: the busy-path concatenation (cached prefix + live
// tail) must equal a fresh full render of the same state, every delta.
func TestBusyRender_concatenatedMatchesFullRender(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx, s := busyCacheTranscript("")
	_ = s

	for i := 0; i < 3; i++ {
		// Grow the live turn by one reasoning delta, as a stream would.
		reasoning := tx.messages[4].reasoning + string(rune('a'+i))
		tx.syncStreamSnapshots(4, "", reasoning)
		s.flow.Observe(ReasoningStream, string(rune('a'+i)))

		got := tx.renderPaneContent()
		var full strings.Builder
		tx.renderHistory(&full, nil, nil)
		if got != full.String() {
			t.Fatalf("delta %d: busy concatenation diverged from full render\n--- got ---\n%s\n--- want ---\n%s", i, got, full.String())
		}
	}
}

// TestBusyRender_prefixCachedAcrossDeltas locks the performance requirement:
// once the committed prefix is rendered, per-delta frames re-render only the
// live tail, never the whole committed history. The prefix string is stable
// across stream deltas.
func TestBusyRender_prefixCachedAcrossDeltas(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
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

	// A stream delta must not invalidate the cached prefix.
	tx.syncStreamSnapshots(4, "", "more reasoning")
	if tx.busyPrefixDirty {
		t.Error("a stream delta must not dirty the committed-prefix cache")
	}
	tx.renderPaneContent()
	if tx.busyPrefix != prefix {
		t.Error("committed prefix must be byte-stable across stream deltas")
	}
}

// TestBusyRender_committedMutationInvalidatesPrefix locks that a committed-history
// change (a tool observation appended to the committed log) invalidates the
// prefix so the next busy frame re-renders it rather than serving stale bytes,
// and that the rebuilt concatenation stays byte-identical to a full render.
func TestBusyRender_committedMutationInvalidatesPrefix(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx, s := busyCacheTranscript("")
	_ = s

	tx.renderPaneContent()
	if tx.busyPrefixDirty {
		t.Fatal("precondition: fresh cache is clean")
	}

	// A committed-history mutation must invalidate the prefix and change its bytes.
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
