package app

import (
	"context"
	"github.com/glemsom/eitri/internal/tui/livekey"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/tools"
	"github.com/glemsom/eitri/internal/tui"

	tea "charm.land/bubbletea/v2"
)

// TestRunEngineTurnRebindsSessionArtifactsAfterNew guards the `/new` TUI seam:
// after the live key changes, the next turn must write message logs under the
// displayed GUID and expose that GUID's temp directory to bash.
func TestRunEngineTurnRebindsSessionArtifactsAfterNew(t *testing.T) {
	dataDir := t.TempDir()
	oldSess, err := session.NewWithGUID(dataDir, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false)
	if err != nil {
		t.Fatalf("old session: %v", err)
	}
	reg, _ := tools.NewRegistry(tools.Deps{Runner: tools.RealRunner, Workspace: t.TempDir(), TempHost: oldSess.TempDir()})

	logged := provider.NewLoggingProvider(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{Done: true, FinishReason: "stop"}), nil
	}), oldSess.MessageLogSink())
	e := engine.New(logged, oldSess)
	live := livekey.NewLiveSessionKey("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	bind := func(key string) error {
		sess, err := session.NewWithGUID(dataDir, key, false)
		if err != nil {
			return err
		}
		return bindSessionArtifacts(e, logged, reg, sess)
	}

	turn := runEngineTurn(e, func() config.Config { return config.Default() }, reg, live, nil, nil, bind)
	if _, err := turn(context.Background(), "old prompt", ""); err != nil {
		t.Fatalf("old session turn: %v", err)
	}
	live.Set("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if _, err := turn(context.Background(), "fresh prompt", ""); err != nil {
		t.Fatalf("fresh session turn: %v", err)
	}

	freshDir := filepath.Join(dataDir, "sessions", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if _, err := os.Stat(filepath.Join(freshDir, "messages.jsonl")); err != nil {
		t.Fatalf("fresh session messages.jsonl missing: %v", err)
	}
	transcript, err := os.ReadFile(filepath.Join(freshDir, "transcript.md"))
	if err != nil {
		t.Fatalf("fresh session transcript missing: %v", err)
	}
	if !strings.Contains(string(transcript), "ok") {
		t.Fatalf("fresh transcript = %q, want answer", transcript)
	}
	oldDir := filepath.Join(dataDir, "sessions", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	oldTranscript, err := os.ReadFile(filepath.Join(oldDir, "transcript.md"))
	if err != nil {
		t.Fatalf("old session transcript missing: %v", err)
	}
	if strings.Contains(string(oldTranscript), "fresh prompt") {
		t.Fatalf("old transcript received fresh turn: %q", oldTranscript)
	}
	if _, err := os.Stat(filepath.Join(oldDir, "messages.jsonl")); err != nil {
		t.Fatalf("old session messages.jsonl missing: %v", err)
	}
	if got := reg.TempHost(); got != filepath.Join(freshDir, "tmp") {
		t.Fatalf("registry temp = %q, want fresh session temp", got)
	}
}

// TestRunTUINewKeepsInjectedProvider verifies `/new` does not replace an
// injected provider just because it is held through a hot wrapper.
func TestRunTUINewKeepsInjectedProvider(t *testing.T) {
	dataDir := t.TempDir()
	initial, err := session.NewWithGUID(dataDir, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false)
	if err != nil {
		t.Fatalf("create initial session: %v", err)
	}
	reg, err := tools.NewRegistry(tools.Deps{Runner: tools.RealRunner, Workspace: t.TempDir(), TempHost: initial.TempDir()})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	injected := provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(provider.Chunk{Content: "injected"}, provider.Chunk{Done: true, FinishReason: "stop"}), nil
	})
	liveProvider := newHotProvider(injected)
	logged := provider.NewLoggingProvider(liveProvider, initial.MessageLogSink())
	e := engine.New(logged, initial)
	cfg := config.Default()
	cfg.Provider = string(provider.ProviderCustomOpenAI)
	cfg.CustomOpenAI.BaseURL = "http://127.0.0.1:1"

	orig := runProgram
	runProgram = func(m tui.Model) error {
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyExtended, Text: "/new"})
		m = next.(tui.Model)
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = next.(tui.Model)
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyExtended, Text: "hello"})
		m = next.(tui.Model)
		next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("submit command = nil")
		}
		_, _ = next.(tui.Model).Update(cmd())
		return nil
	}
	t.Cleanup(func() { runProgram = orig })

	if err := runTUI(e, logged, cfg, reg, initial.GUID(), liveProvider, "", dataDir, nil, t.TempDir(), initial.TempDir(), false, true, false, nil, initial, false); err != nil {
		t.Fatalf("runTUI: %v", err)
	}
	if got := liveProvider.current(); got != injected {
		t.Fatalf("provider after /new = %T, want injected provider", got)
	}
}

func TestRunTUIClosesInitialAndReplacedSessions(t *testing.T) {
	dataDir := t.TempDir()
	initial, err := session.NewWithGUID(dataDir, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false)
	if err != nil {
		t.Fatalf("create initial session: %v", err)
	}
	if err := initial.WriteTranscript([]byte("initial\n")); err != nil {
		t.Fatalf("write initial transcript: %v", err)
	}
	reg, err := tools.NewRegistry(tools.Deps{Runner: tools.RealRunner, Workspace: t.TempDir(), TempHost: initial.TempDir()})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	base := provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{Done: true, FinishReason: "stop"}), nil
	})
	liveProvider := newHotProvider(base)
	logged := provider.NewLoggingProvider(liveProvider, initial.MessageLogSink())
	e := engine.New(logged, initial)

	orig := runProgram
	runProgram = func(m tui.Model) error {
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyExtended, Text: "/new"})
		m = next.(tui.Model)
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = next.(tui.Model)
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyExtended, Text: "hello"})
		m = next.(tui.Model)
		next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = next.(tui.Model)
		if cmd == nil {
			t.Fatal("submit command = nil")
		}
		next, _ = m.Update(cmd())
		_ = next.(tui.Model)
		return nil
	}
	t.Cleanup(func() { runProgram = orig })

	if err := runTUI(e, logged, config.Default(), reg, initial.GUID(), liveProvider, "", dataDir, nil, t.TempDir(), initial.TempDir(), false, true, false, nil, initial, false); err != nil {
		t.Fatalf("runTUI: %v", err)
	}
	if openSessionFile(t, dataDir) {
		t.Fatal("runTUI retained an initial or replaced session file")
	}
}
