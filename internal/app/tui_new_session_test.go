package app

import (
	"context"
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
)

// TestRunEngineTurnRebindsSessionArtifactsAfterNew guards the `/new` TUI seam:
// after the live key changes, the next turn must write message logs under the
// displayed GUID and expose that GUID's temp directory to bash.
func TestRunEngineTurnRebindsSessionArtifactsAfterNew(t *testing.T) {
	dataDir := t.TempDir()
	oldSess, err := session.NewWithGUID(dataDir, "old", false)
	if err != nil {
		t.Fatalf("old session: %v", err)
	}
	reg, _ := tools.NewRegistry(tools.Deps{Runner: tools.RealRunner, Workspace: t.TempDir(), TempHost: oldSess.TempDir()})

	logged := provider.NewLoggingProvider(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{Done: true, FinishReason: "stop"}), nil
	}), oldSess.MessageLogSink())
	e := engine.New(logged, oldSess)
	live := tui.NewLiveSessionKey("old")
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
	live.Set("fresh")
	if _, err := turn(context.Background(), "fresh prompt", ""); err != nil {
		t.Fatalf("fresh session turn: %v", err)
	}

	freshDir := filepath.Join(dataDir, "sessions", "fresh")
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
	oldDir := filepath.Join(dataDir, "sessions", "old")
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
