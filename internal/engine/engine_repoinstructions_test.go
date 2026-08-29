package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

func TestRunAgentRepoInstructionsInjectedAsSystemMessage(t *testing.T) {
	t.Parallel()
	c := &skillIndexCaptureHandler{}
	e := New(provider.NewScripted(c.stream), nil)

	content := "# AGENTS.md\n\nDo the repo thing.\n"
	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:            "deepseek-v4-flash",
		Prompt:           "hi",
		SessionKey:       "sess-abc",
		RepoInstructions: &content,
	}, AgentOptions{MaxTurns: 1}); err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}

	if len(c.requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(c.requests))
	}
	msgs := c.requests[0].Messages
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3 (persona head + repo instructions + user prompt)", len(msgs))
	}
	// Persona head stays byte-identical and first.
	if msgs[0].Role != provider.RoleSystem || msgs[0].Content != SystemPromptContent() {
		t.Fatalf("messages[0] not the persona head: role=%s content=%q", msgs[0].Role, msgs[0].Content)
	}
	// Repo instructions ride as a dedicated system message after the head, before user.
	if msgs[1].Role != provider.RoleSystem || !strings.Contains(msgs[1].Content, "## Repository instructions (AGENTS.md)") {
		t.Fatalf("messages[1] not the injected repo-instructions head: role=%s content=%q", msgs[1].Role, msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "Do the repo thing.") {
		t.Fatalf("messages[1] lacks the AGENTS.md content:\n%s", msgs[1].Content)
	}
	if msgs[2].Role != provider.RoleUser || msgs[2].Content != "hi" {
		t.Fatalf("messages[2] not the user prompt: role=%s content=%q", msgs[2].Role, msgs[2].Content)
	}
}

func TestRunAgentRepoInstructionsAbsentKeepsByteIdenticalMessages(t *testing.T) {
	t.Parallel()
	c := &skillIndexCaptureHandler{}
	e := New(provider.NewScripted(c.stream), nil)

	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "hi",
		SessionKey: "sess-abc",
	}, AgentOptions{MaxTurns: 1}); err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}

	if len(c.requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(c.requests))
	}
	msgs := c.requests[0].Messages
	// [0] persona head, [1] user prompt only — no AGENTS.md system message.
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (persona head + user prompt)", len(msgs))
	}
	if msgs[0].Role != provider.RoleSystem || msgs[0].Content != SystemPromptContent() {
		t.Fatalf("messages[0] not the persona head: role=%s content=%q", msgs[0].Role, msgs[0].Content)
	}
	if msgs[1].Role != provider.RoleUser || msgs[1].Content != "hi" {
		t.Fatalf("messages[1] not the plain user prompt: role=%s content=%q", msgs[1].Role, msgs[1].Content)
	}
}

func TestRunAgentRepoInstructionsNotPersistedInSessionHistory(t *testing.T) {
	t.Parallel()
	c := &skillIndexCaptureHandler{}
	e := New(provider.NewScripted(c.stream), nil)

	content := "# AGENTS.md\n\nDo the repo thing.\n"
	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:            "deepseek-v4-flash",
		Prompt:           "first",
		SessionKey:       "sess-abc",
		RepoInstructions: &content,
	}, AgentOptions{MaxTurns: 1}); err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}

	// Second run under the same session key must not see the instructions twice:
	// the injected message is stripped from persisted history, so the second
	// request carries persona head + repo instructions + persisted user/assistant
	// and the user prompt — the content from req.RepoInstructions is the only
	// repo-instructions message beyond the head.
	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:            "deepseek-v4-flash",
		Prompt:           "second",
		SessionKey:       "sess-abc",
		RepoInstructions: &content,
	}, AgentOptions{MaxTurns: 1}); err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}

	if len(c.requests) != 2 {
		t.Fatalf("captured %d requests, want 2", len(c.requests))
	}
	second := c.requests[1].Messages
	var sysCount, instrCount int
	for _, m := range second {
		if m.Role == provider.RoleSystem {
			sysCount++
			if strings.Contains(m.Content, "## Repository instructions (AGENTS.md)") {
				instrCount++
			}
		}
	}
	if sysCount != 2 {
		t.Errorf("second run has %d system messages, want exactly 2 (re-injected persona head + repo instructions; no leaked duplicate)", sysCount)
	}
	if instrCount != 1 {
		t.Errorf("second run sees the repo instructions %d times, want 1 (injected once, never persisted/duplicated)", instrCount)
	}
}
