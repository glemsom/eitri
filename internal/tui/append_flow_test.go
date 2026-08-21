package tui

import (
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// TestAppendMsg_synthesizesAnswerEventLog locks the contract that every
// appended note carries its own one-event transcript event log, so the
// history renderer can route it through the FlowRenderer like any turn.
func TestAppendMsg_synthesizesAnswerEventLog(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := newTestTx()

	tx.appendMsg(helpView())

	if len(tx.messages) != 1 {
		t.Fatalf("appendMsg must append one message, got %d", len(tx.messages))
	}
	events := tx.messages[0].events
	if len(events) != 1 {
		t.Fatalf("appendMsg message must carry one event, got %d", len(events))
	}
	if events[0].Kind != EventAnswer {
		t.Errorf("event kind = %v, want %v", events[0].Kind, EventAnswer)
	}
	if events[0].Delta != helpView() {
		t.Errorf("event delta = %q, want the appended content", events[0].Delta)
	}
}

// TestAppendMsg_rendersPixelIdenticalThroughFlow locks identical rendering for
// help, login, and failure notes: the history render of an appended note must
// equal exactly what the shared answer emitter produces for it.
func TestAppendMsg_rendersPixelIdenticalThroughFlow(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	notes := []string{
		helpView(),
		"Open https://example.com/device and enter code: ABCD-EFGH",
		"login saved",
		failurePrefix() + "no login flow available",
	}

	for _, note := range notes {
		tx := newTestTx()
		tx.width = 80
		tx.height = 24
		tx.histFollow = true
		tx.histViewport = newHistoryViewport()
		tx.appendMsg(note)

		var hist strings.Builder
		tx.renderHistory(&hist, nil, nil)

		want := renderAnswerBlock(th, config.DefaultTheme, tx.transcriptWidth(), tx.messages[0], note, true)
		if hist.String() != want {
			t.Errorf("appended note %q must render pixel-identical through the flow path", note)
		}
	}
}

// TestTurnDone_withoutStream_carriesEventLog extends the synthesized-log
// contract to a turn that completes without ever streaming: its assistant
// message must still carry an event log so no legacy render branch survives.
func TestTurnDone_withoutStream_carriesEventLog(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("done", nil))
	tx := newTestTx()
	d.startTurn(&tx, "go", "")

	if _, err := d.handleTurnDone(&tx, turnDoneMsg{answer: "done"}); err != nil {
		t.Fatalf("handleTurnDone err = %v", err)
	}

	events := tx.messages[1].events
	if len(events) != 1 || events[0].Kind != EventAnswer || events[0].Delta != "done" {
		t.Errorf("non-streaming turn must synthesize one answer event, got %+v", events)
	}
}
