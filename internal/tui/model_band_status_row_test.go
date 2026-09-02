package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// bandStatusRow returns the band's status row: the lowest non-empty rendered
// line, which sits at the base of the bottom band in both idle and busy states.
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

func TestBandStatusRow_idleShowsWorkspace(t *testing.T) {
	m := bandModel()
	m = resize(t, m)

	row := bandStatusRow(m)
	if !strings.Contains(row, testWorkspace) {
		t.Errorf("idle status row must show the workspace path, got: %q", row)
	}
	// No phase badge, and the right zone duplicates nothing the rail owns: no
	// provider/model, no elapsed counter, no token stats.
	for _, gone := range []string{"idle", "working", "reasoning", "answering", "opencode-go", "deepseek-v4-flash", "elapsed", " tok"} {
		if strings.Contains(row, gone) {
			t.Errorf("idle status row must not carry %q, got: %q", gone, row)
		}
	}
}

func TestBandStatusRow_busyShowsWorkspaceBelowForge(t *testing.T) {
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
}

func TestBandStatusRow_asciiStable(t *testing.T) {
	t.Setenv("EITRI_NO_MOTION", "1")
	m := bandModel()
	m = resize(t, m)

	row := bandStatusRow(m)
	if !strings.Contains(row, testWorkspace) {
		t.Errorf("status row must show the workspace path under reduced motion, got: %q", row)
	}
	if strings.Contains(row, "●") || strings.Contains(row, "idle") {
		t.Errorf("status row must carry no glyph or phase marker, got: %q", row)
	}
}
