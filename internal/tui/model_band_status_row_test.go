package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// bandStatusRow returns the band's single status row: the lowest non-empty
// rendered line, which sits at the base of the bottom band in both idle and
// busy states.
func bandStatusRow(m Model) string {
	rows := strings.Split(strings.TrimRight(view(m), "\n"), "\n")
	for i := len(rows) - 1; i >= 0; i-- {
		if row := ansiStrip(rows[i]); strings.TrimSpace(row) != "" {
			return row
		}
	}
	return ""
}

const testWorkspace = "/home/glenn/projects/eitri"

func bandModel() Model {
	return NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{}, nil
		},
		WorkspacePath: testWorkspace,
		Config:        config.Config{Provider: "opencode-go", Model: "deepseek-v4-flash"},
	})
}

func TestBandStatusRow_idleShowsPhaseAndWorkspace(t *testing.T) {
	m := bandModel()
	m = resize(t, m)

	row := bandStatusRow(m)
	if !strings.Contains(row, "idle") {
		t.Errorf("idle status row must show the idle phase, got: %q", row)
	}
	if !strings.Contains(row, testWorkspace) {
		t.Errorf("idle status row must show the workspace path, got: %q", row)
	}
	if !strings.Contains(row, phaseBadge(PhaseIdle)) {
		t.Errorf("idle status row must carry the phase badge %q, got: %q", phaseBadge(PhaseIdle), row)
	}
	// The right zone duplicates nothing the rail owns: no provider/model, no
	// elapsed counter, no token stats.
	for _, forbidden := range []string{"opencode-go", "deepseek-v4-flash", "elapsed", " tok"} {
		if strings.Contains(row, forbidden) {
			t.Errorf("idle status row must not carry %q, got: %q", forbidden, row)
		}
	}
}

func TestBandStatusRow_busyShowsPhaseAndWorkspaceBelowForge(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn:          streamingTurn,
		Events:        NewEventFeed(),
		WorkspacePath: testWorkspace,
		Config:        config.Config{ThinkingEnabled: true, CoTCollapsedByDefault: true, ToolResultsCollapsedByDefault: true},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	content := ansiStrip(view(m))
	row := bandStatusRow(m)
	if !strings.Contains(row, "working") {
		t.Errorf("busy status row must show the working phase, got: %q", row)
	}
	if !strings.Contains(row, testWorkspace) {
		t.Errorf("busy status row must keep the workspace path, got: %q", row)
	}
	// The status row sits below the forge panel while a turn works.
	forgeIdx := strings.Index(content, "Eitri is forging")
	rowIdx := strings.Index(content, row)
	if forgeIdx == -1 {
		t.Fatalf("busy state must render the forge panel, got:\n%s", content)
	}
	if rowIdx <= forgeIdx {
		t.Errorf("busy status row must sit below the forge panel (forge %d, row %d):\n%s", forgeIdx, rowIdx, content)
	}
	// The right zone stays free of the rail's counters even mid-turn.
	for _, forbidden := range []string{"opencode-go", "deepseek-v4-flash", "elapsed", " tok"} {
		if strings.Contains(row, forbidden) {
			t.Errorf("busy status row must not carry %q, got: %q", forbidden, row)
		}
	}
}

func TestBandStatusRow_asciiFallback(t *testing.T) {
	t.Setenv("EITRI_NO_MOTION", "1")
	m := bandModel()
	m = resize(t, m)

	row := bandStatusRow(m)
	if strings.Contains(row, "●") {
		t.Errorf("reduced-motion status row must drop the ● marker, got: %q", row)
	}
	if !strings.Contains(row, "o idle") {
		t.Errorf("reduced-motion status row must carry the ASCII phase marker, got: %q", row)
	}
}
