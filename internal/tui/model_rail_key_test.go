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

func TestRailKey_Shrink(t *testing.T) {
	t.Parallel()
	m, cc := railKeyModel(t, 40)

	nm, _ := m.Update(tea.KeyPressMsg{Code: '[', Mod: tea.ModCtrl | tea.ModShift})
	m = asModel(t, nm)

	if w := m.tx.railWidthOrDefault(); w != 38 {
		t.Errorf("Ctrl+Shift+[ should shrink rail by 2, got width %d (want 38)", w)
	}
	if cc.cfg.RailWidth != 38 {
		t.Errorf("shrink should persist to config, got rail_width %d (want 38)", cc.cfg.RailWidth)
	}
}

func TestRailKey_Grow(t *testing.T) {
	t.Parallel()
	m, cc := railKeyModel(t, 40)

	nm, _ := m.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl | tea.ModShift})
	m = asModel(t, nm)

	if w := m.tx.railWidthOrDefault(); w != 42 {
		t.Errorf("Ctrl+Shift+] should grow rail by 2, got width %d (want 42)", w)
	}
	if cc.cfg.RailWidth != 42 {
		t.Errorf("grow should persist to config, got rail_width %d (want 42)", cc.cfg.RailWidth)
	}
}

func TestRailKey_Reset(t *testing.T) {
	t.Parallel()
	m, cc := railKeyModel(t, 50)

	nm, _ := m.Update(tea.KeyPressMsg{Code: '0', Mod: tea.ModAlt})
	m = asModel(t, nm)

	if w := m.tx.railWidthOrDefault(); w != defaultRailWidth {
		t.Errorf("Alt+0 should reset rail to default, got width %d (want %d)", w, defaultRailWidth)
	}
	if cc.cfg.RailWidth != defaultRailWidth {
		t.Errorf("reset should persist to config, got rail_width %d (want %d)", cc.cfg.RailWidth, defaultRailWidth)
	}
}

func TestRailKey_ShrinkFloor(t *testing.T) {
	t.Parallel()
	m, _ := railKeyModel(t, minWidthRail)

	nm, _ := m.Update(tea.KeyPressMsg{Code: '[', Mod: tea.ModCtrl | tea.ModShift})
	m = asModel(t, nm)

	if w := m.tx.railWidthOrDefault(); w != minWidthRail {
		t.Errorf("shrink below floor should clamp to minWidthRail=%d, got %d", minWidthRail, w)
	}
}

func TestRailKey_Visible(t *testing.T) {
	t.Parallel()
	m, _ := railKeyModel(t, 30)

	for _, tc := range []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{"shrink", tea.KeyPressMsg{Code: '[', Mod: tea.ModCtrl | tea.ModShift}},
		{"shrink-x", tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}},
		{"grow", tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl | tea.ModShift}},
		{"grow-z", tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}},
		{"reset", tea.KeyPressMsg{Code: '0', Mod: tea.ModAlt}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nm, _ := m.Update(tc.msg)
			mm := asModel(t, nm)
			v := view(mm)
			if v == "" {
				t.Errorf("rail key %s produced empty render", tc.name)
			}
		})
	}
}
