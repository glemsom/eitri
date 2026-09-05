package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// longSessionTranscript builds a completed multi-turn transcript where every
// turn carries a reasoning block, one tool call, and an answer — the long-
// session shape T05 must keep scannable. Each turn's message carries the
// finalized reasoning snapshot (Commit sets it) alongside its event
// log, mirroring real committed turns.
func longSessionTranscript(n int) *Transcript {
	th := themeFor(config.DefaultTheme)
	var log toolLog
	var messages []message
	for i := 0; i < n; i++ {
		messages = append(messages, message{role: "you", content: fmt.Sprintf("prompt-%02d", i)})
		log.SetAnchor(len(messages) - 1)
		log.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: fmt.Sprintf(`{"command":"tool-%02d"}`, i)}})
		log.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: fmt.Sprintf("toolresult-%02d\nline2", i), Lines: 2}})
		messages = append(messages, message{
			role:              "eitri",
			content:           fmt.Sprintf("answer-%02d", i),
			reasoning:         fmt.Sprintf("reasoning-%02d step-one step-two", i),
			thinkingRequested: true,
			events: []TimelineEvent{
				{Kind: EventReasoning, Seq: i * 4, Delta: fmt.Sprintf("reasoning-%02d", i)},
				{Kind: EventToolStart, Seq: i*4 + 1, Start: &ToolStart{Name: "bash", Args: fmt.Sprintf(`{"command":"tool-%02d"}`, i)}},
				{Kind: EventToolResult, Seq: i*4 + 2, Result: &ToolResult{Name: "bash", Result: fmt.Sprintf("toolresult-%02d\nline2", i), Lines: 2}},
				{Kind: EventAnswer, Seq: i*4 + 3, Delta: fmt.Sprintf("answer-%02d", i)},
			},
		})
	}
	return &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           100,
		height:          30,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		log:             log,
		messages:        messages,
	}
}

// focusBlock sets the block-focus cursor onto collapsible block idx (0-
// based), the outcome of Tab being pressed until that block is focused.
func focusBlock(tx *Transcript, idx int) {
	tx.focus.on = true
	tx.focus.cursor = idx
}

// TestTranscript_20TurnHistoryStaysScannable pins the T05 past-turn collapse
// projection: a 20-turn history keeps every answer visible, renders each CoT
// and tool result as a collapsed one-liner, and stays scrollable — the
// long-session shape a large reasoning block must never blow out of view.
func TestTranscript_20TurnHistoryStaysScannable(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := longSessionTranscript(20)

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	// Every turn's answer stays visible.
	for i := 0; i < 20; i++ {
		if !strings.Contains(plain, fmt.Sprintf("answer-%02d", i)) {
			t.Errorf("turn %d answer must stay visible in the scannable history, got:\n%s", i, plain)
		}
	}
	// CoT bodies collapse to the hint: no full reasoning body leaks through.
	if strings.Contains(plain, "reasoning-00") {
		t.Error("CoT body must collapse to the hint in a long session, got full reasoning text")
	}
	// Tool results collapse to their one-liner: the head stays, the body hides.
	if !strings.Contains(plain, "tool-00") {
		t.Errorf("tool head must stay visible in the one-liner, got:\n%s", plain)
	}
	if strings.Contains(plain, "toolresult-00") {
		t.Error("tool result body must collapse to the one-liner in a long session")
	}
	// Exactly one collapsed CoT hint per turn: past turns must not duplicate
	// their reasoning hint at tool boundaries (the T05 projection regression).
	if got := strings.Count(plain, "tok"); got != 20 {
		t.Errorf("long session must render one collapsed CoT hint per turn, got %d hints (want 20):\n%s", got, plain)
	}
	// The history overflows the viewport: it stays scrollable.
	if n := lineCount(hist.String()); n <= tx.height {
		t.Errorf("20-turn history (%d rows) must exceed the viewport height (%d) to stay scrollable", n, tx.height)
	}
}

// TestTranscript_pastTurnExpandReplaysFullInterleavedSequence pins the second
// T05 acceptance criterion: expanding a past turn's blocks replays the full
// interleaved event sequence (reasoning before tool before answer) exactly
// once, in arrival order, nested between its neighbors' answers.
func TestTranscript_pastTurnExpandReplaysFullInterleavedSequence(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := longSessionTranscript(20)
	k := 10

	// Expand turn k's reasoning and tool blocks through the block-focus
	// interaction (Tab + Enter), the same path a user takes on a past turn.
	focusBlock(tx, 2*k)
	tx.toggleFocused() // reveal the reasoning body
	focusBlock(tx, 2*k+1)
	tx.toggleFocused() // reveal the tool result

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	reasoning := fmt.Sprintf("reasoning-%02d", k)
	toolHead := fmt.Sprintf("tool-%02d", k)
	toolResult := fmt.Sprintf("toolresult-%02d", k)
	answer := fmt.Sprintf("answer-%02d", k)
	prevAnswer := fmt.Sprintf("answer-%02d", k-1)
	nextAnswer := fmt.Sprintf("answer-%02d", k+1)

	rb := strings.Index(plain, reasoning)
	tb := strings.Index(plain, toolHead)
	xb := strings.Index(plain, toolResult)
	ab := strings.Index(plain, answer)
	pa := strings.Index(plain, prevAnswer)
	na := strings.Index(plain, nextAnswer)
	if rb < 0 || tb < 0 || xb < 0 || ab < 0 || pa < 0 || na < 0 {
		t.Fatalf("expanded past turn %d must replay all segments (r=%d t=%d x=%d a=%d prev=%d next=%d):\n%s", k, rb, tb, xb, ab, pa, na, plain)
	}
	// The full interleaved sequence replays in arrival order, nested between
	// the previous turn's answer and the next turn's, so the turn reads as one
	// contiguous replayable block.
	if !(pa < rb && rb < tb && tb < xb && xb < ab && ab < na) {
		t.Errorf("expanded past turn must replay reasoning < tool < answer after the previous answer and before the next, got r=%d t=%d x=%d a=%d (prev=%d next=%d):\n%s", rb, tb, xb, ab, pa, na, plain)
	}
	// No segment may replay twice: a committed turn's reasoning snapshot must
	// render once, not once per tool boundary.
	if n := strings.Count(plain, reasoning); n != 1 {
		t.Errorf("expanded past turn reasoning must replay exactly once, got %d copies:\n%s", n, plain)
	}
	if n := strings.Count(plain, answer); n != 1 {
		t.Errorf("expanded past turn answer must appear exactly once, got %d copies", n)
	}
}
