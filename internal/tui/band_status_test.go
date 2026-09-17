package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	tea "charm.land/bubbletea/v2"
)

func statusRowModel(t *testing.T, w int, workspace string) Model {
	t.Helper()
	m := NewModelCfg(Dependencies{
		WorkspacePath: workspace,
	})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
	return asModel(t, nm)
}

func TestRenderBandStatusRow_separatorAtNarrowWidths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		width int
	}{
		{width: 80},
		{width: 60},
		{width: 40},
	}
	for _, c := range cases {
		c := c
		t.Run(fmt.Sprintf("width/%d", c.width), func(t *testing.T) {
			m := statusRowModel(t, c.width, "/home/dev/acme")
			row := m.renderBandStatusRow()
			plain := ansi.Strip(row)
			if w := ansi.StringWidth(plain); w > c.width {
				t.Errorf("status row width = %d, want ≤ %d", w, c.width)
			}
			if !strings.Contains(plain, " /home/dev/acme") {
				t.Errorf("status row missing separator before workspace: %q", plain)
			}
		})
	}
}

func TestRenderBandStatusRow_exactBandWidth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		width int
	}{
		{width: 80},
		{width: 60},
		{width: 40},
	}
	for _, c := range cases {
		c := c
		t.Run(fmt.Sprintf("width/%d", c.width), func(t *testing.T) {
			m := statusRowModel(t, c.width, "/home/dev/acme")
			row := m.renderBandStatusRow()
			plain := ansi.Strip(row)
			if w := ansi.StringWidth(plain); w != c.width {
				t.Errorf("status row width = %d, want exactly %d", w, c.width)
			}
		})
	}
}

func TestRenderBandStatusRow_noWorkspace(t *testing.T) {
	t.Parallel()
	m := statusRowModel(t, 80, "")
	row := m.renderBandStatusRow()
	plain := ansi.Strip(row)
	if w := ansi.StringWidth(plain); w != 80 {
		t.Errorf("status row width = %d, want exactly 80", w)
	}
}
