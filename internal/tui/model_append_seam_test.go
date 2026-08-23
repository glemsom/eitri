package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

func TestModel_loginCodeAppendsThroughSeam(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	ch := make(chan tea.Msg)
	m.tx.layout.dirty = false // isolate the append: only the seam may re-mark it
	nm, _ := m.Update(loginCodeMsg{code: LoginCode{UserCode: "ZZ-AA", VerificationURI: "https://example.dev"}, next: ch})
	m = asModel(t, nm)
	if got := lastEitri(t, m); !strings.Contains(got, "ZZ-AA") {
		t.Errorf("login code note missing device code, got: %q", got)
	}
	if !m.tx.layout.dirty {
		t.Error("login code append must mark the transcript layout dirty")
	}
}

func TestModel_loginDoneErrorAppendsThroughSeam(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m.tx.layout.dirty = false // isolate the append: only the seam may re-mark it
	nm, _ := m.Update(loginDoneMsg{err: errors.New("device flow rejected")})
	m = asModel(t, nm)
	if got := lastEitri(t, m); !strings.Contains(got, "device flow rejected") {
		t.Errorf("login error note missing, got: %q", got)
	}
	if !m.tx.layout.dirty {
		t.Error("login error append must mark the transcript layout dirty")
	}
}

func TestModel_loginDoneSuccessAppendsThroughSeam(t *testing.T) {
	t.Parallel()
	var applied config.Config
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Config:   cfgFixture(),
		SaveBack: func(c config.Config) { applied = c },
	})
	m = resize(t, m)
	m.tx.layout.dirty = false // isolate the append: only the seam may re-mark it
	nm, _ := m.Update(loginDoneMsg{cfg: config.Config{Provider: "github-copilot"}})
	m = asModel(t, nm)
	if got := lastEitri(t, m); !strings.Contains(got, "login saved") {
		t.Errorf("login success note missing, got: %q", got)
	}
	if applied.Provider != "github-copilot" {
		t.Errorf("login config not persisted, got provider %q", applied.Provider)
	}
	if !m.tx.layout.dirty {
		t.Error("login success append must mark the transcript layout dirty")
	}
}

func TestModel_skillNoActivationAppendsThroughSeam(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
	})
	m = resize(t, m)
	m.tx.layout.dirty = false // isolate the append: only the seam may re-mark it
	m.slash.Activate(m.tx, m.deps.Skills, "my-skill", "")
	if got := lastEitri(t, m); !strings.Contains(got, "no skill activation available") {
		t.Errorf("skill failure note missing, got: %q", got)
	}
	if !m.tx.layout.dirty {
		t.Error("skill failure append must mark the transcript layout dirty")
	}
}

func TestModel_loginNoFlowAppendsThroughSeam(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m.tx.layout.dirty = false // isolate the append: only the seam may re-mark it
	nm, _ := m.startLogin()
	m = asModel(t, nm)
	if got := lastEitri(t, m); !strings.Contains(got, "no login flow available") {
		t.Errorf("login failure note missing, got: %q", got)
	}
	if !m.tx.layout.dirty {
		t.Error("login failure append must mark the transcript layout dirty")
	}
}

func lastEitri(t *testing.T, m Model) string {
	t.Helper()
	if n := len(m.tx.messages); n == 0 {
		t.Fatal("transcript has no messages")
		return ""
	}
	return m.tx.messages[len(m.tx.messages)-1].content
}
