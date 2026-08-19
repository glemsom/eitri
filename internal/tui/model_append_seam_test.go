package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

// The append seam (issue #390) says every finished assistant entry routes
// through the Transcript appendMsg seam — the message lands and the shared
// layout is marked dirty in the same step. Each migrated path's behavioral
// test below clears the dirty flag first so that only the seam may re-mark it,
// then asserts the note appended and the layout was invalidated (the same
// isolation the existing `/help` seam test uses).

// TestModel_skillResultAppendsThroughSeam verifies the slash-skill activation
// result (skillDoneMsg) appends through the seam instead of touching message
// state directly.
func TestModel_skillResultAppendsThroughSeam(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
	})
	m = resize(t, m)
	m.tx.layout.dirty = false // isolate the append: only the seam may re-mark it
	nm, _ := m.Update(skillDoneMsg{payload: "<skill_content>my-note</skill_content>"})
	m = asModel(t, nm)
	if got := lastEitri(t, m); !strings.Contains(got, "my-note") {
		t.Errorf("skill result note missing, got: %q", got)
	}
	if !m.tx.layout.dirty {
		t.Error("skill result append must mark the transcript layout dirty")
	}
}

// TestModel_loginCodeAppendsThroughSeam verifies the device-flow code note
// (loginCodeMsg) appends through the seam.
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

// TestModel_loginDoneErrorAppendsThroughSeam verifies the login-error note
// (loginDoneMsg with err) appends through the seam.
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

// TestModel_loginDoneSuccessAppendsThroughSeam verifies the login-success note
// (loginDoneMsg with cfg) appends through the seam and persists the config.
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

// TestModel_skillNoActivationAppendsThroughSeam verifies the "no skill
// activation available" failure note in activateSkill appends through the seam.
func TestModel_skillNoActivationAppendsThroughSeam(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		// No Skills surface: the activation failure note path.
	})
	m = resize(t, m)
	m.tx.layout.dirty = false // isolate the append: only the seam may re-mark it
	nm, _ := m.activateSkill("my-skill", "")
	m = asModel(t, nm)
	if got := lastEitri(t, m); !strings.Contains(got, "no skill activation available") {
		t.Errorf("skill failure note missing, got: %q", got)
	}
	if !m.tx.layout.dirty {
		t.Error("skill failure append must mark the transcript layout dirty")
	}
}

// TestModel_loginNoFlowAppendsThroughSeam verifies the "no login flow
// available" failure note in startLogin appends through the seam.
func TestModel_loginNoFlowAppendsThroughSeam(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
		// No Login seam: the login failure note path.
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

// lastEitri returns the content of the most recent eitri-role transcript
// message, failing if the transcript is empty.
func lastEitri(t *testing.T, m Model) string {
	t.Helper()
	if n := len(m.tx.messages); n == 0 {
		t.Fatal("transcript has no messages")
		return ""
	}
	return m.tx.messages[len(m.tx.messages)-1].content
}
