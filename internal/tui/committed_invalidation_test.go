package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
)

// This file locks the T1 invalidation split: the committed-history prefix is
// rebuilt only when a committed unit actually changes. Live-only activity —
// a tool observation landing while the running turn streams, per-delta stream
// snapshots, and idle/timer/clock frames — must never mark the committed prefix
// for rebuild. The busy-path design guarantees the prefix is served from the
// cached busyPrefix string across those frames; the mutations below are the
// only way it can go stale.

// liveToolBusyTranscript builds a busy transcript with committed history
// followed by a running live turn, with the tool log's anchor set to the live
// turn's prompt so a tool observation lands on the live tail, not the committed
// prefix. This is the realistic runtime shape: while a turn runs, every accepted
// tool observation belongs to that running turn.
func liveToolBusyTranscript() *Transcript {
	th := themeFor(config.DefaultTheme)
	tx := &Transcript{
		theme:        th,
		configTheme:  config.DefaultTheme,
		width:        100,
		height:       30,
		histFollow:   true,
		histViewport: newHistoryViewport(),
		busy:         true,
	}
	tx.messages = append(tx.messages,
		message{role: "you", content: "turn one"},
		message{role: "eitri", content: "answer one", events: synthAnswerLog("answer one")},
		message{role: "you", content: "turn two"},
		message{role: "eitri", content: "answer two", events: synthAnswerLog("answer two")},
		message{role: "you", content: "live prompt"},
		message{role: "eitri", streaming: true, thinkingRequested: true, reasoning: "reasoning", expansion: ExpansionState{}},
	)
	// The running turn's tool-log anchor is its prompt (index 4), the live tail's
	// start, so observations land on the live region and never the prefix.
	log := toolLog{}
	log.SetAnchor(4)
	tx.log = log
	return tx
}

// TestLiveToolStartDoesNotInvalidateCommittedPrefix locks AC1: a tool
// observation landing while the running turn streams must not mark committed
// history for rebuild, and the busy-path output stays byte-identical to a fresh
// full render (the live tail picks the tool up without re-rendering the prefix).
func TestLiveToolStartDoesNotInvalidateCommittedPrefix(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := liveToolBusyTranscript()
	tx.renderPaneContent()
	if tx.busyPrefixDirty {
		t.Fatal("precondition: a fresh busy prefix cache is clean")
	}
	prefix := tx.busyPrefix

	// A live tool observation lands while the running turn streams.
	tx.applyTool(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	// Pin the in-progress entry's clock far from a whole-second boundary so the
	// elapsed timer (a live-clock decoration) cannot flip between the two renders
	// below; the committed-prefix bytes are unaffected either way.
	if len(tx.log.entries) != 1 {
		t.Fatalf("applyTool must add one tool entry, got %d", len(tx.log.entries))
	}
	tx.log.entries[0].startedAt = tx.log.entries[0].startedAt.Add(-time.Second / 2)

	if tx.busyPrefixDirty {
		t.Error("a live tool observation during a running turn must not mark committed history for rebuild")
	}
	if tx.busyPrefix != prefix {
		t.Error("the committed-prefix cache must stay byte-stable across a live tool observation")
	}

	// The concatenated busy render stays byte-identical to a fresh full render.
	got := tx.renderPaneContent()
	var full strings.Builder
	tx.renderHistory(&full, nil, nil)
	if got != full.String() {
		t.Errorf("busy render diverged from full render after a live tool apply\n--- got ---\n%s\n--- want ---\n%s", got, full.String())
	}
}

// TestLiveToolResultDoesNotInvalidateCommittedPrefix locks the trailing half of
// AC1: the tool's result landing while the turn still streams is equally
// live-only and must not rebuild the committed prefix.
func TestLiveToolResultDoesNotInvalidateCommittedPrefix(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := liveToolBusyTranscript()
	tx.applyTool(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	tx.renderPaneContent()
	if tx.busyPrefixDirty {
		t.Fatal("precondition: a fresh busy prefix cache is clean")
	}
	prefix := tx.busyPrefix

	tx.applyTool(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2}})

	if tx.busyPrefixDirty {
		t.Error("a live tool result during a running turn must not mark committed history for rebuild")
	}
	if tx.busyPrefix != prefix {
		t.Error("the committed-prefix cache must stay byte-stable across a live tool result")
	}

	got := tx.renderPaneContent()
	var full strings.Builder
	tx.renderHistory(&full, nil, nil)
	if got != full.String() {
		t.Errorf("busy render diverged from full render after a live tool result\n--- got ---\n%s\n--- want ---\n%s", got, full.String())
	}
}

// TestIdleAndTimerFramesDoNotRebuildCommittedPrefix locks AC4: an idle render
// plus the timer/clock frames the TUI emits each second (clockTickMsg) and each
// spinner tick advance the live indicator without ever marking the committed
// history for rebuild — so an idle or animated long session keeps its cached
// prefix and consumes no committed-history work.
func TestIdleAndTimerFramesDoNotRebuildCommittedPrefix(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	m := newStreamingModel()
	m = resize(t, m)

	// Commit one turn so committed history sits above the live tail, then start
	// a second live turn and stream one delta into it.
	m = typeText(t, m, "first")
	m, _ = submitBusy(t, m)
	m = applyReasoningDelta(t, m, "first reasoning")
	m = asModel(t, mustUpdate(t, m, turnDoneMsg{prompt: "first", answer: "first answer", reasoning: "first reasoning"}))
	m = typeText(t, m, "second")
	m, _ = submitBusy(t, m)
	m = applyReasoningDelta(t, m, "second reasoning")

	// Build and pin the busy prefix cache for the committed history.
	_ = view(m)
	if m.tx.busyPrefixDirty {
		t.Fatal("precondition: fresh prefix cache is clean")
	}
	prefix := m.tx.busyPrefix
	if !strings.Contains(ansiStrip(prefix), "first answer") {
		t.Fatalf("prefix must carry the committed history, got %q", ansiStrip(prefix))
	}

	// A clock tick and a spinner tick re-render the surface; neither touches the
	// committed prefix.
	m = asModel(t, mustUpdate(t, m, clockTickMsg{}))
	m = asModel(t, mustUpdate(t, m, spinnerTickMsg{}))

	if m.tx.busyPrefixDirty {
		t.Error("idle/timer/clock frames must not mark committed history for rebuild")
	}
	if m.tx.busyPrefix != prefix {
		t.Error("committed prefix must stay byte-stable across idle/timer/clock frames")
	}
}
