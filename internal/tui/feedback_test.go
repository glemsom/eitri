package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestModel_renderFeedbackUsesSemanticMarkers(t *testing.T) {
	plain := lipgloss.NewStyle()
	m := Model{tx: &Transcript{theme: Theme{statusStyle: plain, outcomeOKStyle: plain, outcomeErrStyle: plain}}}

	m.feedback = successFeedback("copied")
	if got := m.renderFeedback(); !strings.Contains(got, "✓ copied") {
		t.Fatalf("success feedback = %q, want check marker", got)
	}

	m.feedback = failureFeedback("clipboard unavailable")
	if got := m.renderFeedback(); !strings.Contains(got, "✗ clipboard unavailable") {
		t.Fatalf("failure feedback = %q, want cross marker", got)
	}

	m.feedback = neutralFeedback("unknown theme")
	if got := m.renderFeedback(); got != "unknown theme" {
		t.Fatalf("neutral feedback = %q, want unmarked text", got)
	}
}

func TestModel_feedbackRendersBelowContextualHint(t *testing.T) {
	m := NewModelCfg(Dependencies{})
	m = resize(t, m)
	m.feedback = successFeedback("copied")

	lines := strings.Split(ansiStrip(view(m)), "\n")
	hintIdx, feedbackIdx := -1, -1
	for i, ln := range lines {
		if strings.Contains(ln, "enter send") {
			hintIdx = i
		}
		if strings.Contains(ln, "✓ copied") {
			feedbackIdx = i
		}
	}
	if hintIdx == -1 || feedbackIdx == -1 || feedbackIdx <= hintIdx {
		t.Fatalf("feedback must render below hint (hint %d feedback %d):\n%s", hintIdx, feedbackIdx, ansiStrip(view(m)))
	}
}
