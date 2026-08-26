package engine

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// skillIndexCaptureHandler records each request's messages so tests can assert
// the system-layer skill index placement without going to the wire.
type skillIndexCaptureHandler struct {
	requests []provider.Request
}

func (c *skillIndexCaptureHandler) stream(ctx context.Context, req provider.Request) (provider.Stream, error) {
	c.requests = append(c.requests, req)
	return provider.StreamFunc(
		provider.Chunk{Content: "ok", FinishReason: "stop", Done: true},
	), nil
}

func TestRunAgentSkillIndexAbsentKeepsByteIdenticalMessages(t *testing.T) {
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
	// [0] persona head, [1] user prompt only — no system skill-index message.
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

func TestRunAgentSkillIndexInjectedAsSecondSystemMessage(t *testing.T) {
	t.Parallel()
	c := &skillIndexCaptureHandler{}
	e := New(provider.NewScripted(c.stream), nil)

	idx := "<available_skills><skill><name>review</name></skill></available_skills>"
	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "hi",
		SessionKey: "sess-abc",
		SkillIndex: &idx,
	}, AgentOptions{MaxTurns: 1}); err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}

	if len(c.requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(c.requests))
	}
	msgs := c.requests[0].Messages
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3 (persona head + skill index + user prompt)", len(msgs))
	}
	// Persona head stays byte-identical and first.
	if msgs[0].Role != provider.RoleSystem || msgs[0].Content != SystemPromptContent() {
		t.Fatalf("messages[0] not the persona head: role=%s content=%q", msgs[0].Role, msgs[0].Content)
	}
	// Skill index is a dedicated system message placed after the head, before history/user.
	if msgs[1].Role != provider.RoleSystem || msgs[1].Content != idx {
		t.Fatalf("messages[1] not the injected skill index: role=%s content=%q", msgs[1].Role, msgs[1].Content)
	}
	if msgs[2].Role != provider.RoleUser || msgs[2].Content != "hi" {
		t.Fatalf("messages[2] not the user prompt: role=%s content=%q", msgs[2].Role, msgs[2].Content)
	}
}

func TestRunAgentSkillIndexNotPersistedInSessionHistory(t *testing.T) {
	t.Parallel()
	c := &skillIndexCaptureHandler{}
	e := New(provider.NewScripted(c.stream), nil)

	idx := "<available_skills><skill><name>review</name></skill></available_skills>"
	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "first",
		SessionKey: "sess-abc",
		SkillIndex: &idx,
	}, AgentOptions{MaxTurns: 1}); err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}

	// Second run under the same session key must not see the index twice:
	// the injected index is stripped from persisted history, so the second
	// request carries persona head + index + user prompt — the index from
	// req.SkillIndex is the only system message beyond the head.
	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "second",
		SessionKey: "sess-abc",
		SkillIndex: &idx,
	}, AgentOptions{MaxTurns: 1}); err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}

	if len(c.requests) != 2 {
		t.Fatalf("captured %d requests, want 2", len(c.requests))
	}
	second := c.requests[1].Messages
	// head, index, persisted [user, assistant], user
	// Count system messages: exactly one non-head skill index.
	var sysCount, indexCount int
	for _, m := range second {
		if m.Role == provider.RoleSystem {
			sysCount++
			if m.Content == idx {
				indexCount++
			}
		}
	}
	if sysCount != 2 {
		t.Errorf("second run has %d system messages, want exactly 2 (re-injected persona head + skill index; no leaked duplicate)", sysCount)
	}
	if indexCount != 1 {
		t.Errorf("second run sees the skill index %d times, want 1 (injected once, never persisted/duplicated)", indexCount)
	}
	t.Logf("second-run messages: %d total", len(second))
}
