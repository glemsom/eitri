package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// This file locks T3: invalidation of the committed render memo is scoped to
// the inputs that actually feed it. An expansion toggle or a focus-marker move
// re-renders only the unit(s) whose flow draws the affected block — never the
// whole history — while a width or theme change (which genuinely re-wraps
// everything) re-renders all units. The counters and byte-identity assertions
// below use the established seams from the T2/T4 work: committedUnitRenders
// counts settled messages freshly rendered into the memo, and
// assertLayoutMatchesFreshFullRender proves the memo never serves bytes a
// fresh full render would not produce.

// scopedMemoTx builds a settled three-turn transcript whose collapsible blocks
// sit in different committed units: turn one carries a reasoning block (unit
// 1), turn two carries a reasoning block plus a tool entry (unit 3), and turn
// three is a plain answer (unit 5) — so a toggle or focus move can be observed
// to re-render exactly its own unit(s) while the rest of the memo is served
// unchanged.
func scopedMemoTx() *Transcript {
	th := themeFor(config.DefaultTheme)
	log := toolLog{}
	log.SetAnchor(2)
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
		message{role: "you", content: "one"},
		message{role: "eitri", content: "Done one.", reasoning: "think one", thinkingRequested: true,
			expansion: expansionWithReasoningForces(true, false),
			events: []TimelineEvent{
				{Kind: EventReasoning, Seq: 0, Delta: "think one"},
				{Kind: EventAnswer, Seq: 1, Delta: "Done one."},
			}},
		message{role: "you", content: "two"},
		message{role: "eitri", content: "Done two.", reasoning: "think two", thinkingRequested: true,
			expansion: expansionWithReasoningForces(true, false),
			events: []TimelineEvent{
				{Kind: EventReasoning, Seq: 0, Delta: "think two"},
				{Kind: EventToolStart, Seq: 1, Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}},
				{Kind: EventToolResult, Seq: 2, Result: &ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2}},
				{Kind: EventAnswer, Seq: 3, Delta: "Done two."},
			}},
		message{role: "you", content: "three"},
		message{role: "eitri", content: "Done three.", events: synthAnswerLog("Done three.")},
	)
	// The fixture starts dirty so the first ensureLayout materializes the memo
	// (like a real first frame after commit).
	tx.layout.dirty = true
	return tx
}

// TestReasoningToggleRerendersOnlyItsUnit locks AC1's reasoning half: toggling
// one turn's reasoning block re-renders exactly that turn's unit, the other
// committed units come from the memo, and the memo stays byte-identical to a
// fresh full render.
func TestReasoningToggleRerendersOnlyItsUnit(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := scopedMemoTx()
	tx.ensureLayout()
	base := tx.committedUnitRenders
	if base != 6 {
		t.Fatalf("precondition: settled memo must hold 6 units, got %d renders", base)
	}
	if !strings.Contains(ansiStrip(tx.layout.rendered), "think one") {
		t.Fatalf("precondition: turn one's reasoning must render expanded, got:\n%s", ansiStrip(tx.layout.rendered))
	}

	tx.toggleThinkingFragment(1, 0) // collapse turn one's reasoning block (unit 1)
	tx.ensureLayout()

	if got := tx.committedUnitRenders - base; got != 1 {
		t.Fatalf("a reasoning expansion toggle re-rendered %d committed units, want exactly 1 (the toggled turn's unit): other units must come from the memo", got)
	}
	if strings.Contains(ansiStrip(tx.layout.rendered), "think one") {
		t.Errorf("the collapsed reasoning body must disappear, got:\n%s", ansiStrip(tx.layout.rendered))
	}
	assertLayoutMatchesFreshFullRender(t, tx)
}

// TestToolToggleRerendersOnlyItsUnit locks AC1's tool half: toggling one tool
// entry's expansion re-renders only the unit whose flow draws that entry, and
// the memo stays byte-identical to a fresh full render.
func TestToolToggleRerendersOnlyItsUnit(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := scopedMemoTx()
	tx.ensureLayout()
	base := tx.committedUnitRenders
	if strings.Contains(ansiStrip(tx.layout.rendered), "a.go") {
		t.Fatalf("precondition: turn two's tool result must render collapsed, got:\n%s", ansiStrip(tx.layout.rendered))
	}

	tx.toggleToolEntry(0) // expand turn two's tool result (unit 3)
	tx.ensureLayout()

	if got := tx.committedUnitRenders - base; got != 1 {
		t.Fatalf("a tool expansion toggle re-rendered %d committed units, want exactly 1 (the toggled turn's unit): other units must come from the memo", got)
	}
	if !strings.Contains(ansiStrip(tx.layout.rendered), "a.go") {
		t.Errorf("the expanded tool result must appear, got:\n%s", ansiStrip(tx.layout.rendered))
	}
	assertLayoutMatchesFreshFullRender(t, tx)
}

// TestScopedToggleCostFlatInHistorySize is the cost-flat regression guard for
// input-scoped invalidation: an expansion toggle re-renders exactly one
// committed unit no matter how much history sits above it, so a toggle on a
// 1000-turn session costs the same as on a 10-turn one.
func TestScopedToggleCostFlatInHistorySize(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	for _, n := range []int{10, 100, 1000} {
		t.Run("N="+strconv.Itoa(n), func(t *testing.T) {
			tx := memoTestTx()
			buildCommittedTurns(tx, n)
			base := tx.committedUnitRenders

			tx.toggleThinkingFragment(1, 0) // a block inside an early unit
			tx.ensureLayout()

			if got := tx.committedUnitRenders - base; got != 1 {
				t.Fatalf("an expansion toggle on top of %d prior committed turns re-rendered %d committed units, want exactly 1: invalidation must be scoped to the block's unit, never the history", n, got)
			}
			assertLayoutMatchesFreshFullRender(t, tx)
		})
	}
}

// TestWidthChangeRerendersAllCommittedUnits locks AC2: a width change
// genuinely re-wraps every committed unit, so the memo re-renders all of them
// and the output stays byte-identical to a fresh render at the new width.
func TestWidthChangeRerendersAllCommittedUnits(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := scopedMemoTx()
	tx.ensureLayout()
	base := tx.committedUnitRenders

	tx.SetSize(80, 30) // narrower terminal: every unit re-wraps
	tx.ensureLayout()

	if got := tx.committedUnitRenders - base; got != 6 {
		t.Fatalf("a width change re-rendered %d committed units, want all 6: every unit re-wraps at the new width", got)
	}
	assertLayoutMatchesFreshFullRender(t, tx)
}

// TestThemeChangeRerendersAllCommittedUnits locks AC3: a theme change
// re-colors every committed unit, so the memo re-renders all of them and the
// output stays byte-correct.
func TestThemeChangeRerendersAllCommittedUnits(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := scopedMemoTx()
	tx.ensureLayout()
	base := tx.committedUnitRenders

	tx.applySettings(config.Config{Theme: "gruvbox"})
	tx.ensureLayout()

	if got := tx.committedUnitRenders - base; got != 6 {
		t.Fatalf("a theme change re-rendered %d committed units, want all 6: every unit re-colors at the new theme", got)
	}
	assertLayoutMatchesFreshFullRender(t, tx)
}

// TestFocusMoveRerendersOnlyAffectedUnits locks AC4: moving the block-focus
// marker re-renders only the units that draw the old and new focused blocks —
// never the whole history. The fixture's blocks live in units 1 and 3, so the
// first Tab (off -> the first block) refreshes one unit, the second Tab (unit
// 1 -> unit 3) refreshes two, and each step stays byte-identical to a fresh
// render.
func TestFocusMoveRerendersOnlyAffectedUnits(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := scopedMemoTx()
	tx.ensureLayout()
	base := tx.committedUnitRenders

	// Tab activates the cursor on turn one's reasoning block (unit 1): only
	// that unit gains the marker.
	tx.focusNext()
	tx.ensureLayout()
	if got := tx.committedUnitRenders - base; got != 1 {
		t.Fatalf("activating the focus marker re-rendered %d committed units, want exactly 1: the marker must not rebuild history", got)
	}
	assertLayoutMatchesFreshFullRender(t, tx)

	// Tab again moves the marker to turn two's reasoning block: the old unit
	// loses the marker and the new one gains it, so exactly two units refresh.
	tx.focusNext()
	tx.ensureLayout()
	if got := tx.committedUnitRenders - base; got != 3 {
		t.Fatalf("moving the focus marker re-rendered %d committed units cumulative, want exactly 3 (1 + the two units the marker left and entered): history must not rebuild", got)
	}
	assertLayoutMatchesFreshFullRender(t, tx)
}

// TestLiveToolToggleDoesNotTouchCommittedMemo locks the live-tail half of AC1:
// toggling a tool entry of the running (live) turn changes only the live
// region, so no committed unit re-renders and the busy-path prefix stays
// byte-stable.
func TestLiveToolToggleDoesNotTouchCommittedMemo(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := liveToolBusyTranscript()
	tx.applyTool(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	idx := len(tx.log.entries) - 1 // anchored to the live prompt, not committed
	tx.renderPaneContent()
	base := tx.committedUnitRenders
	prefix := tx.busyPrefix

	tx.toggleToolEntry(idx)
	tx.renderPaneContent()

	if got := tx.committedUnitRenders - base; got != 0 {
		t.Errorf("a live-tail tool toggle re-rendered %d committed units, want 0: live blocks are not baked into the memo", got)
	}
	if tx.busyPrefix != prefix {
		t.Error("the committed prefix must stay byte-stable across a live-tail tool toggle")
	}
	assertBusyRenderMatchesFresh(t, tx)
}

// TestCommittedToolObservationRerendersOnlyItsUnit locks the committed-half of
// AC1: a tool observation landing on a committed entry re-renders only that
// entry's units, not the whole prefix.
func TestCommittedToolObservationRerendersOnlyItsUnit(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := busyWithCommittedToolTranscript()
	tx.renderPaneContent()
	base := tx.committedUnitRenders
	if base != 2 {
		t.Fatalf("precondition: busy memo must hold the committed turn's 2 units, got %d", base)
	}

	// The committed turn's tool result lands while the live turn streams.
	tx.applyTool(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2}})
	tx.renderPaneContent()

	if got := tx.committedUnitRenders - base; got != 1 {
		t.Fatalf("a committed tool observation re-rendered %d committed units, want exactly 1 (the turn that draws the entry): the prefix must not rebuild", got)
	}
	assertBusyRenderMatchesFresh(t, tx)
}

// TestPostTurnToolObservationScopedToCommittedUnits locks the idle half of the
// committed-observation path: a tool observation landing after the turn settled
// attaches its event to the last committed assistant message, so exactly that
// message's unit re-renders — and a later toggle of the never-drawn entry it
// introduced re-renders nothing at all.
func TestPostTurnToolObservationScopedToCommittedUnits(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := scopedMemoTx()
	tx.ensureLayout()
	base := tx.committedUnitRenders

	f := NewFold(NewTurnSession(nil))
	f.Tool(tx, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"true"}`}})
	tx.ensureLayout()

	if got := tx.committedUnitRenders - base; got != 1 {
		t.Fatalf("a post-turn tool observation re-rendered %d committed units, want exactly 1 (the last assistant unit the event appended to)", got)
	}
	assertLayoutMatchesFreshFullRender(t, tx)

	// The added entry sits past every flow's drawn start count, so toggling it
	// cannot change a single rendered byte: the memo re-renders nothing.
	added := len(tx.log.entries) - 1
	tx.toggleToolEntry(added)
	tx.ensureLayout()
	if got := tx.committedUnitRenders - base; got != 1 {
		t.Fatalf("toggling a tool entry no unit draws re-rendered %d committed units, want still exactly 1: a block with no rendered bytes must not invalidate anything", got)
	}
	assertLayoutMatchesFreshFullRender(t, tx)
}

// busyWithCommittedToolTranscript builds a busy transcript with one committed
// turn whose tool entry is still running (started, no result yet) and a live
// turn streaming above it — the shape in which a committed tool observation
// arrives mid-turn.
func busyWithCommittedToolTranscript() *Transcript {
	th := themeFor(config.DefaultTheme)
	log := toolLog{}
	log.SetAnchor(0)
	log.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	tx := &Transcript{
		theme:        th,
		configTheme:  config.DefaultTheme,
		width:        100,
		height:       30,
		histFollow:   true,
		histViewport: newHistoryViewport(),
		busy:         true,
		log:          log,
	}
	tx.messages = append(tx.messages,
		message{role: "you", content: "turn one"},
		message{role: "eitri", content: "answer one", events: []TimelineEvent{
			{Kind: EventToolStart, Seq: 0, Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}},
			{Kind: EventAnswer, Seq: 1, Delta: "answer one"},
		}},
		message{role: "you", content: "live prompt"},
		message{role: "eitri", streaming: true, thinkingRequested: true, reasoning: "reasoning", expansion: ExpansionState{}},
	)
	log.SetAnchor(2)
	tx.log = log
	return tx
}

// assertBusyRenderMatchesFresh compares the busy-path concatenation (cached
// committed prefix + live tail) against a fresh full render, byte for byte.
func assertBusyRenderMatchesFresh(t *testing.T, tx *Transcript) {
	t.Helper()
	got := tx.renderPaneContent()
	var full strings.Builder
	tx.renderHistory(&full, nil, nil)
	if got != full.String() {
		t.Errorf("busy render diverged from fresh full render\n--- got ---\n%s\n--- want ---\n%s", got, full.String())
	}
}
