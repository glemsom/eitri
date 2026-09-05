package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

func TestMaybeCompactKeepsRepoInstructionsInStableHead(t *testing.T) {
	t.Parallel()
	// A fail-safe provider: summary generation returns nothing, so maybeCompact
	// takes its head+tail path and must still preserve the injected head.
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(provider.Chunk{Done: true}), nil
	}), &mockTranscript{})

	// A long run whose message list opens [system(Eitri), system(AGENTS.md), user, ...]:
	// the two assistant legs force the tail floor past the repo-instructions head, so
	// without the stable-head fix the AGENTS.md system message is evicted into the body
	// and lost.
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: SystemPromptContent()},
		{Role: provider.RoleSystem, Content: repoInstructionsDirective("# AGENTS.md\n\nDo the repo thing.\n")},
		{Role: provider.RoleUser, Content: "old prompt"},
		{Role: provider.RoleAssistant, Content: "old answer one"},
		{Role: provider.RoleUser, Content: "mid prompt"},
		{Role: provider.RoleAssistant, Content: "old answer two"},
		{Role: provider.RoleUser, Content: "later prompt"},
		{Role: provider.RoleAssistant, Content: "old answer three"},
		{Role: provider.RoleUser, Content: "latest prompt"},
	}

	cfg := compactCfg()
	got, ok := e.maybeCompact(context.Background(), RunRequest{}, AgentOptions{
		Compaction: cfg,
		lastUsage:  &provider.Usage{PromptTokens: 999},
	}, messages, true, 1)
	if !ok {
		t.Fatal("expected compaction to fire on a forced overflow")
	}

	var saw bool
	for _, m := range got {
		if m.Role == provider.RoleSystem && strings.Contains(m.Content, "## Repository instructions (AGENTS.md)") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("injected repo-instructions system message dropped by compaction:\n%v", got)
	}
}
