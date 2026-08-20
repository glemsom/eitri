package tui

import (
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// liveAnswerFlow builds a running turn whose live timeline already carries an
// answer so the merged flow can render it de-emphasized while it streams.
func liveAnswerFlow(t *testing.T, streaming, stopped bool, content string) *Transcript {
	t.Helper()
	th := themeFor(config.DefaultTheme)
	return &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           100,
		height:          30,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		log:             toolLog{},
		busy:            streaming && !stopped,
		messages: []message{
			{role: "you", content: "write a haiku"},
			{
				role:              "eitri",
				content:           content,
				streaming:         streaming,
				stopped:           stopped,
				thinkingRequested: true,
				events: []TimelineEvent{
					{Kind: EventAnswer, Seq: 0, Delta: content},
				},
			},
		},
	}
}

func renderFlow(t *testing.T, tx *Transcript) string {
	t.Helper()
	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	return hist.String()
}

// TestRenderFlow_liveAnswerDeemphasizedWhileStreaming locks the first half of
// T04: while a turn is live (its answer still streaming on the merged flow) the
// answer must render in the dimmed streaming pane, not the full accent used by
// a completed turn.
func TestRenderFlow_liveAnswerDeemphasizedWhileStreaming(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)

	live := renderFlow(t, liveAnswerFlow(t, true, false, "the haiku begins here"))
	done := renderFlow(t, liveAnswerFlow(t, false, false, "the finished haiku"))

	liveColor := lineBorderColor(live, "the haiku begins here")
	doneColor := lineBorderColor(done, "the finished haiku")
	if liveColor == "" || doneColor == "" {
		t.Fatalf("live answer color = %q, done answer color = %q; both must frame a border:\n%s\n%s", liveColor, doneColor, ansiStrip(live), ansiStrip(done))
	}
	if liveColor == doneColor {
		t.Errorf("live answer border %q must differ from completed answer border %q (de-emphasis lost)", liveColor, doneColor)
	}
	if liveColor != borderColorStr(th.streamingPaneStyle) {
		t.Errorf("live answer border = %q, want streamingPaneStyle %q", liveColor, borderColorStr(th.streamingPaneStyle))
	}
	if doneColor != borderColorStr(th.agentPaneStyle) {
		t.Errorf("completed answer border = %q, want agentPaneStyle %q", doneColor, borderColorStr(th.agentPaneStyle))
	}
}

// TestRenderFlow_stopRevealsPartialAnswer locks the third half of T04: stopping
// mid-answer keeps whatever text already streamed, framed by the stopped pane
// (accent-dimmed) and marked with the stopped marker — never lost, never an
// error.
func TestRenderFlow_stopRevealsPartialAnswer(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)

	const partial = "only the first two lines"
	rendered := renderFlow(t, liveAnswerFlow(t, false, true, partial))

	if !strings.Contains(ansiStrip(rendered), partial) {
		t.Fatalf("stopped flow must reveal the partial answer %q, got:\n%s", partial, ansiStrip(rendered))
	}
	if !strings.Contains(ansiStrip(rendered), stoppedMarker()) {
		t.Errorf("stopped flow must render the stopped marker, got:\n%s", ansiStrip(rendered))
	}
	if got := lineBorderColor(rendered, partial); got != borderColorStr(th.stoppedPaneStyle) {
		t.Errorf("stopped answer border = %q, want stoppedPaneStyle %q", got, borderColorStr(th.stoppedPaneStyle))
	}
}

// TestRenderFlow_stopNotMisreadAsError guards that a stopped turn is not
// restyled as an error: the partial answer must keep the stopped (accent-dimmed)
// pane and never the error panes, which would read the stop as a failure.
func TestRenderFlow_stopNotMisreadAsError(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)

	const partial = "partial words"
	rendered := renderFlow(t, liveAnswerFlow(t, false, true, partial))
	got := lineBorderColor(rendered, partial)
	if got == borderColorStr(th.errorPaneStyle) || got == borderColorStr(th.streamingErrorPaneStyle) {
		t.Errorf("stopped answer border = %q, never the error panes (error reads a failure, stop does not)", got)
	}
}
