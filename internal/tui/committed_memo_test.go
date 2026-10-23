package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// memoTestTx is render-ready with settled committed turns, so the memo tests
// exercise the idle layout path a finished frame takes, not the busy fast path.
func memoTestTx() *Transcript {
	th := themeFor(config.DefaultTheme)
	return &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           100,
		height:          30,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
	}
}

// commitTurn drives one streaming+commit turn through the TurnSession verbs and
// renders the settled transcript, forcing the memo to materialize its units.
func commitTurn(t testing.TB, tx *Transcript, prompt, answer string) {
	t.Helper()
	s := NewTurnSession(stubTurn(answer, nil))
	cmd := beginTurn(s, tx, prompt, "")
	projectStream(tx, AnswerStream, answer)
	projectDone(tx, cmd().(turnDoneMsg))
	tx.ensureLayout()
}

// assertLayoutMatchesFreshFullRender checks the memo-served layout against a
// fresh full render byte-for-byte, including the row->message and row->tool
// indexes: if those drift, scroll/selection/block-focus hit-tests break.
func assertLayoutMatchesFreshFullRender(t *testing.T, tx *Transcript) {
	t.Helper()
	tx.ensureLayout()

	var fullRows []toolRowRange
	var fullMsgs []msgRowRange
	var full strings.Builder
	tx.renderHistory(&full, &fullRows, &fullMsgs)

	if tx.layout.rendered != full.String() {
		t.Errorf("incremental rendered != fresh full render\n--- incremental ---\n%s\n--- fresh ---\n%s", tx.layout.rendered, full.String())
	}
	if !reflect.DeepEqual(tx.layout.rows, fullRows) {
		t.Errorf("row->tool indexes diverged from a fresh render\nincremental: %+v\nfresh:      %+v", tx.layout.rows, fullRows)
	}
	if !reflect.DeepEqual(tx.layout.msgs, fullMsgs) {
		t.Errorf("row->message indexes diverged from a fresh render\nincremental: %+v\nfresh:      %+v", tx.layout.msgs, fullMsgs)
	}
}

// TestCommittedTurnRendersOnlyItsOwnUnits: committing a new turn renders exactly
// that turn's messages into the memo and serves the prior units from it, so
// per-turn commit cost is bounded by the new turn, not by history length.
func TestCommittedTurnRendersOnlyItsOwnUnits(t *testing.T) {
	tx := memoTestTx()

	commitTurn(t, tx, "first", "answer one")
	base := tx.committedUnitRenders
	if base != 2 {
		t.Fatalf("first turn must render 2 committed units (prompt + answer), got %d", base)
	}

	// A second turn renders only its own two units; the prior ones come from the memo.
	commitTurn(t, tx, "second", "answer two")
	if got := tx.committedUnitRenders - base; got != 2 {
		t.Fatalf("second commit re-rendered %d committed units, want exactly 2 (its own turn only): prior units were re-derived", got)
	}

	commitTurn(t, tx, "third", "answer three")
	if got := tx.committedUnitRenders - base; got != 4 {
		t.Fatalf("third commit re-rendered %d committed units, want exactly 4 (2 per turn): commit cost must stay flat in prior history", got)
	}
}

// TestCommittedTurnRenderCountFlatInPriorHistory sweeps the flat-cost property:
// each additional committed turn adds exactly its own unit renders regardless of
// how many prior turns sit above it.
func TestCommittedTurnRenderCountFlatInPriorHistory(t *testing.T) {
	tx := memoTestTx()

	commitTurn(t, tx, "zero", "z")
	base := tx.committedUnitRenders
	for i := 0; i < 6; i++ {
		commitTurn(t, tx, strings.Repeat("q", i+1), "answer")
	}
	if got := tx.committedUnitRenders - base; got != 12 {
		t.Errorf("6 more turns re-rendered %d committed units, want exactly 12 (2 each): cost grew with prior history", got)
	}
}

// TestIncrementalByteIdentityWithManyPriorTurns: after a long series of committed
// turns the incremental transcript stays byte-identical to a fresh full render,
// and the memo renders exactly one unit per settled message.
func TestIncrementalByteIdentityWithManyPriorTurns(t *testing.T) {
	tx := memoTestTx()

	for i := 0; i < 40; i++ {
		commitTurn(t, tx, "q"+string(rune('a'+i)), "answer number")
	}
	assertLayoutMatchesFreshFullRender(t, tx)
	if got := tx.committedUnitRenders; got != 80 {
		t.Errorf("40 committed turns rendered %d units, want exactly 80 (2 each): cost grew with prior history", got)
	}
}

// shapeFixture holds every committed turn shape in sequence — plain answer,
// reasoning-only, tool-interleaved reasoning/answer, stopped, error, and appended
// non-streaming notes.
func shapeFixture() *Transcript {
	th := themeFor(config.DefaultTheme)
	log := toolLog{}
	log.SetAnchor(0)
	log.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	log.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2}})

	tx := &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           100,
		height:          30,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		log:             log,
	}

	tx.messages = append(tx.messages,
		// plain answer
		message{role: "you", content: "plain"},
		message{role: "eitri", content: "plain answer", events: synthAnswerLog("plain answer")},
		// reasoning-only
		message{role: "you", content: "reason"},
		message{role: "eitri", content: "", reasoning: "inner monologue", thinkingRequested: true,
			expansion: expansionWithReasoningForces(true, false),
			events:    []TimelineEvent{{Kind: EventReasoning, Seq: 0, Delta: "inner monologue"}}},
		// tool-interleaved reasoning + answer
		message{role: "you", content: "interleave"},
		message{role: "eitri", content: "Done.", reasoning: "let me check", thinkingRequested: true,
			expansion: expansionWithReasoningForces(true, false),
			events: []TimelineEvent{
				{Kind: EventReasoning, Seq: 0, Delta: "let me check"},
				{Kind: EventToolStart, Seq: 1, Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}},
				{Kind: EventToolResult, Seq: 2, Result: &ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2}},
				{Kind: EventAnswer, Seq: 3, Delta: "Done."},
			}},
		// stopped turn
		message{role: "you", content: "stop"},
		message{role: "eitri", content: "partial", stopped: true, events: synthAnswerLog("partial")},
		// error turn
		message{role: "you", content: "boom"},
		message{role: "eitri", content: failurePrefix() + "boom", events: synthAnswerLog(failurePrefix() + "boom")},
	)

	// appended non-streaming notes
	tx.AppendAssistantMsg("a standalone note")
	tx.AppendAssistantMsg("another note")

	return tx
}

func TestIncrementalMatchesFullRenderAcrossTurnShapes(t *testing.T) {
	tx := shapeFixture()
	assertLayoutMatchesFreshFullRender(t, tx)
}

func TestIncrementalMatchesFullRenderAfterCommits(t *testing.T) {
	tx := memoTestTx()

	commitTurn(t, tx, "plain", "plain answer")
	assertLayoutMatchesFreshFullRender(t, tx)

	// reasoning-only turn, committed directly (a non-streaming reasoning turn)
	tx.layout.dirty = true
	tx.messages = append(tx.messages,
		message{role: "you", content: "reason"},
		message{role: "eitri", content: "", reasoning: "inner monologue", thinkingRequested: true,
			expansion: expansionWithReasoningForces(true, false),
			events:    []TimelineEvent{{Kind: EventReasoning, Seq: 0, Delta: "inner monologue"}}})
	tx.ensureLayout()
	assertLayoutMatchesFreshFullRender(t, tx)

	// appended non-streaming note (AppendAssistantMsg dirties the layout itself)
	tx.AppendAssistantMsg("a standalone note")
	assertLayoutMatchesFreshFullRender(t, tx)

	// an error turn committed through the real TurnSession path
	es := NewTurnSession(stubTurn("", &errBoom{}))
	cmd := beginTurn(es, tx, "boom", "")
	projectDone(tx, cmd().(turnDoneMsg))
	tx.ensureLayout()
	assertLayoutMatchesFreshFullRender(t, tx)
}

type errBoom struct{}

func (e *errBoom) Error() string { return "boom" }
