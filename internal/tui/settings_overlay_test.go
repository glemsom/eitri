package tui

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

func TestSettingsOverlay_OpenArmsDiscoveryOnlyWhenModelListEmpty(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		DiscoverModels: func(ctx context.Context, cfg config.Config) ([]string, error) {
			return nil, errors.New("should not run during open")
		},
	}
	o, cmd := openSettingsOverlay(cfgFixture(), []string{"deepseek-v4-flash"}, defaultTheme, nil, nil, deps)
	if cmd != nil {
		t.Fatal("open with a known model list returned a command, want none")
	}
	if o.discoverState != discoverIdle {
		t.Fatalf("discoverState = %v, want discoverIdle", o.discoverState)
	}

	o, cmd = openSettingsOverlay(cfgFixture(), nil, defaultTheme, nil, nil, deps)
	if cmd == nil {
		t.Fatal("open with no models and discovery available returned nil command")
	}
	if o.discoverState != discoverLoading {
		t.Fatalf("discoverState = %v, want discoverLoading", o.discoverState)
	}
}

func TestSettingsOverlay_EscClosesWithoutPersisting(t *testing.T) {
	t.Parallel()
	saved := false
	deps := Dependencies{Save: func(config.Config) error { saved = true; return nil }}
	o, _ := openSettingsOverlay(cfgFixture(), []string{"m"}, defaultTheme, nil, nil, deps)

	outcome, cmd := o.Key(tea.KeyPressMsg{Code: tea.KeyEsc})
	if outcome != outcomeClosed || cmd != nil {
		t.Fatalf("esc outcome/cmd = %v/%v, want outcomeClosed/nil", outcome, cmd)
	}
	if saved {
		t.Fatal("esc triggered save")
	}
}

func TestSettingsOverlay_SaveReportsStatusAndReturnsDraft(t *testing.T) {
	t.Parallel()
	var saved config.Config
	var mirrored config.Config
	deps := Dependencies{
		Save:     func(c config.Config) error { saved = c; return nil },
		SaveBack: func(c config.Config) { mirrored = c },
	}
	o, _ := openSettingsOverlay(cfgFixture(), []string{"deepseek-v4-flash"}, defaultTheme, nil, nil, deps)
	for range fieldSave {
		outcome, _ := o.Key(tea.KeyPressMsg{Code: tea.KeyTab})
		if outcome == outcomeSaved {
			break
		}
	}

	cfg, status := o.Save()
	if status != "saved" {
		t.Fatalf("save status = %q, want \"saved\"", status)
	}
	if saved.Provider != cfg.Provider {
		t.Fatalf("seam-saved provider = %q, want %q", saved.Provider, cfg.Provider)
	}
	if cfg.Provider != cfgFixture().Provider {
		t.Fatalf("saved provider = %q, want the seeded draft provider", cfg.Provider)
	}
	if mirrored.Provider != cfg.Provider {
		t.Fatalf("mirrored provider = %q, want %q", mirrored.Provider, cfg.Provider)
	}
}

func TestSettingsOverlay_ApplyDiscoveryErrorState(t *testing.T) {
	t.Parallel()
	o := &SettingsOverlay{}
	o.ApplyDiscovery(discoverDoneMsg{provider: "p", err: errors.New("boom")})
	if o.discoverState != discoverError || o.discoverErr != "boom" {
		t.Fatalf("discovery state = %v/%q, want discoverError/boom", o.discoverState, o.discoverErr)
	}
}

func TestSettingsOverlay_SaveFailureCarriesError(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		Save: func(config.Config) error { return errors.New("disk full") },
	}
	o, _ := openSettingsOverlay(cfgFixture(), []string{"m"}, defaultTheme, nil, nil, deps)
	_, status := o.Save()
	if status != "save failed: disk full" {
		t.Fatalf("save status = %q, want \"save failed: disk full\"", status)
	}
}

func TestSettingsOverlay_ProviderChangeArmsDiscoveryForDraftProvider(t *testing.T) {
	t.Parallel()
	var providers []string
	deps := Dependencies{
		DiscoverModels: func(_ context.Context, cfg config.Config) ([]string, error) {
			providers = append(providers, cfg.Provider)
			return []string{"gpt-4o"}, nil
		},
	}
	o, _ := openSettingsOverlay(cfgFixture(), []string{"m"}, defaultTheme, nil, nil, deps)

	outcome, cmd := o.Key(tea.KeyPressMsg{Code: tea.KeyDown})
	if outcome != outcomeContinue || cmd == nil {
		t.Fatalf("provider change outcome/cmd = %v/%v, want outcomeContinue/discovery cmd", outcome, cmd)
	}
	if o.cfg.Provider != "github-copilot" {
		t.Fatalf("draft provider = %q, want github-copilot", o.cfg.Provider)
	}
	if len(providers) != 0 {
		t.Fatalf("discovery ran before command delivery: %v", providers)
	}
	msg := cmd()
	done, ok := msg.(discoverDoneMsg)
	if !ok || done.provider != "github-copilot" {
		t.Fatalf("cmd() message = %#v, want discoverDoneMsg for github-copilot", msg)
	}
	o.ApplyDiscovery(done)
	if got := o.Model(); got != "gpt-4o" {
		t.Fatalf("model after discovery = %q, want gpt-4o", got)
	}
}
