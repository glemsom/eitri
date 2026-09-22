package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestModel_pasteDroppedWhenComposerDoesNotOwnInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		model func(t *testing.T) Model
	}{
		{
			name: "busy",
			model: func(t *testing.T) Model {
				t.Helper()
				m := resize(t, newStreamingModel())
				m = typeText(t, m, "prompt")
				m, _ = submitBusy(t, m)
				return m
			},
		},
		{
			name: "settings overlay",
			model: func(t *testing.T) Model {
				t.Helper()
				m := resize(t, NewModelCfg(Dependencies{Config: cfgFixture()}))
				m = openSettingsForTest(t, m)
				m.composer.SetValue("draft")
				return m
			},
		},
		{
			name: "help overlay",
			model: func(t *testing.T) Model {
				t.Helper()
				m := resize(t, NewModelCfg(Dependencies{Config: cfgFixture()}))
				m.help = openHelpOverlay(m.tx.theme, m.tx.configTheme, m.tx.width, m.tx.height)
				m.composer.SetValue("draft")
				return m
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.model(t)
			before := m.composer.Value()
			m = asModel(t, mustUpdate(t, m, tea.PasteMsg{Content: " pasted"}))
			if got := m.composer.Value(); got != before {
				t.Errorf("paste changed composer while another surface owned input: %q -> %q", before, got)
			}
		})
	}
}

func TestModel_visibleComposerPasteUsesEditBookkeeping(t *testing.T) {
	t.Parallel()
	m := resize(t, NewModelCfg(Dependencies{Config: cfgFixture()}))
	m.histIdx = 0
	m.histDraft = "archived draft"

	m = asModel(t, mustUpdate(t, m, tea.PasteMsg{Content: "/one\ntwo\n/"}))

	if got := m.composer.Value(); got != "/one\ntwo\n/" {
		t.Fatalf("composer paste = %q", got)
	}
	if m.histIdx != -1 || m.histDraft != "" {
		t.Errorf("paste must end active recall, got index=%d draft=%q", m.histIdx, m.histDraft)
	}
	if got := m.slash.lastValue; got != m.composer.Value() {
		t.Errorf("paste must refresh slash completion state, got %q", got)
	}
	if got := m.composer.Height(); got != 3 {
		t.Errorf("paste must synchronize composer height, got %d, want 3", got)
	}
}
