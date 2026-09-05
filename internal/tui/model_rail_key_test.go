package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

func railKeyModel(t *testing.T, railWidth int) (Model, *configCapture) {
	t.Helper()
	cc := &configCapture{}
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, p string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Rail:   NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1"),
		Config: testConfig(railWidth),
		Save: func(cfg config.Config) error {
			cc.cfg = cfg
			return nil
		},
		SaveBack: func(cfg config.Config) {
			cc.cfg = cfg
		},
	})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = asModel(t, nm)
	return m, cc
}

type configCapture struct {
	cfg config.Config
}

func testConfig(railWidth int) config.Config {
	cfg := config.Default()
	cfg.RailWidth = railWidth
	return cfg
}

func TestRailKeys_DocumentedResizeControls(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  rune
		want int
	}{
		{"shrink", 'x', 38}, {"grow", 'z', 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, cc := railKeyModel(t, 40)
			nm, _ := m.Update(tea.KeyPressMsg{Code: tc.key, Mod: tea.ModCtrl})
			m = asModel(t, nm)
			if got := m.tx.railWidthOrDefault(); got != tc.want {
				t.Errorf("Ctrl+%c changed rail width to %d, want %d", tc.key, got, tc.want)
			}
			if got := cc.cfg.RailWidth; got != tc.want {
				t.Errorf("Ctrl+%c persisted rail_width %d, want %d", tc.key, got, tc.want)
			}
		})
	}
}

func TestRailKeys_UndocumentedAliasesAreUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{"shrink", tea.KeyPressMsg{Code: '[', Mod: tea.ModCtrl | tea.ModShift}},
		{"grow", tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl | tea.ModShift}},
		{"reset", tea.KeyPressMsg{Code: '0', Mod: tea.ModAlt}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, cc := railKeyModel(t, 40)
			nm, _ := m.Update(tc.msg)
			m = asModel(t, nm)
			if got := m.tx.railWidthOrDefault(); got != 40 {
				t.Errorf("unsupported key changed rail width to %d, want 40", got)
			}
			if got := cc.cfg.RailWidth; got != 0 {
				t.Errorf("unsupported key persisted rail_width %d, want no persistence", got)
			}
		})
	}
}

func TestRailKey_ShrinkFloor(t *testing.T) {
	t.Parallel()
	m, _ := railKeyModel(t, minWidthRail)
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	m = asModel(t, nm)
	if w := m.tx.railWidthOrDefault(); w != minWidthRail {
		t.Errorf("shrink below floor should clamp to minWidthRail=%d, got %d", minWidthRail, w)
	}
}
