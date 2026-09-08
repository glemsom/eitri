package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// memoTestTx builds a render-ready transcript with settled committed turns so
// the committed-history memo tests exercise the idle layout path (the path a
// finished turn's frame takes) rather than the busy fast path.
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

// commitTurn drives one full streaming+commit turn through the TurnSession
// verbs and renders the settled transcript (idle) so the committed memo has to
// materialize the new turn's units.
func commitTurn(t testing.TB, tx *Transcript, prompt, answer string) {
	t.Helper()
	s := NewTurnSession(stubTurn(answer, nil))
	cmd := s.Begin(tx, prompt, "")
	f := NewFold(s)
	f.Stream(tx, AnswerStream, answer)
	s.Commit(tx, cmd().(turnDoneMsg))
	tx.ensureLayout()
}

// assertLayoutMatchesFreshFullRender locks AC3 and AC4: the incrementally
// rendered transcript (served from the committed memo through recordLayout)
// must equal a fresh full render byte-for-byte, and the row->message and
// row->tool indexes it produced must match the fresh render's indexes.
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

// TestCommittedTurnRendersOnlyItsOwnUnits locks AC2 and the flat-cost
// property: committing a new turn renders exactly that turn's messages into the
// memo and serves the prior units from the memo, so per-turn commit cost is
// bounded by the new turn rather than growing with the committed-history length.
func TestCommittedTurnRendersOnlyItsOwnUnits(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := memoTestTx()

	commitTurn(t, tx, "first", "answer one")
	base := tx.committedUnitRenders
	if base != 2 {
		t.Fatalf("first turn must render 2 committed units (prompt + answer), got %d", base)
	}

	// A second turn renders only its own two units — the prior ones come from
	// the memo, so the render count stays flat in history length.
	commitTurn(t, tx, "second", "answer two")
	if got := tx.committedUnitRenders - base; got != 2 {
		t.Fatalf("second commit re-rendered %d committed units, want exactly 2 (its own turn only): prior units were re-derived", got)
	}

	// A third turn is still exactly two more renders: no prior-history growth.
	commitTurn(t, tx, "third", "answer three")
	if got := tx.committedUnitRenders - base; got != 4 {
		t.Fatalf("third commit re-rendered %d committed units, want exactly 4 (2 per turn): commit cost must stay flat in prior history", got)
	}
}

// TestCommittedTurnRenderCountFlatInPriorHistory locks the same flat-cost
// property across a wider sweep: each additional committed turn adds exactly
// its own unit renders regardless of how many prior turns sit above it.
func TestCommittedTurnRenderCountFlatInPriorHistory(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
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

// TestIncrementalByteIdentityWithManyPriorTurns locks AC5: after a long series
// of committed turns, the incrementally rendered transcript is still byte-
// identical to a fresh full render, and the memo renders exactly one unit per
// settled message regardless of history length.
func TestIncrementalByteIdentityWithManyPriorTurns(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := memoTestTx()

	for i := 0; i < 40; i++ {
		commitTurn(t, tx, "q"+string(rune('a'+i)), "answer number")
	}
	assertLayoutMatchesFreshFullRender(t, tx)
	if got := tx.committedUnitRenders; got != 80 {
		t.Errorf("40 committed turns rendered %d units, want exactly 80 (2 each): cost grew with prior history", got)
	}
}

// shapeFixture builds a single transcript holding a variety of committed turn
// shapes in sequence — plain answer, reasoning-only, tool-interleaved
// reasoning/answer, stopped, error, and appended non-streaming notes — the set
// the byte-identity acceptance criterion names.
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
	tx.appendMsg("a standalone note")
	tx.appendMsg("another note")

	return tx
}

// TestIncrementalMatchesFullRenderAcrossTurnShapes locks AC3/AC4: a transcript
// holding every named turn shape renders incrementally (via the memo) exactly
// byte-identical to a fresh full render, and the row->message / row->tool
// indexes match so scroll/selection/block-focus hit-tests cannot drift.
func TestIncrementalMatchesFullRenderAcrossTurnShapes(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := shapeFixture()
	assertLayoutMatchesFreshFullRender(t, tx)
}

// TestIncrementalMatchesFullRenderAfterCommits drives the varied shapes through
// real commits (the path that exercises memo extension turn by turn) and checks
// byte-identity and index identity after each.
func TestIncrementalMatchesFullRenderAfterCommits(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
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

	// appended non-streaming note (appendMsg dirties the layout itself)
	tx.appendMsg("a standalone note")
	assertLayoutMatchesFreshFullRender(t, tx)

	// an error turn committed through the real TurnSession path
	es := NewTurnSession(stubTurn("", &errBoom{}))
	cmd := es.Begin(tx, "boom", "")
	es.Commit(tx, cmd().(turnDoneMsg))
	tx.ensureLayout()
	assertLayoutMatchesFreshFullRender(t, tx)
}

// errBoom is a tiny always-error marker satisfying the error interface so the
// fixture can drive a turn that fails without importing errors.New churn.
type errBoom struct{}

func (e *errBoom) Error() string { return "boom" }
