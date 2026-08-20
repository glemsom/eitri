package engine

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

func TestRunAgentPrependsSkillInjectToMessages(t *testing.T) {
	t.Parallel()
	var capturer capturedRequests
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		capturer.reqs = append(capturer.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "done", FinishReason: "stop", Done: true}), nil
	}), &mockTranscript{})

	skill := "<skill_content name=\"improve-codebase-architecture\">\nDo the architecture thing.\n</skill_content>\n"
	_, err := e.RunAgent(context.Background(), RunRequest{
		Model:       "deepseek-v4-flash",
		Prompt:      "improve this",
		SkillInject: &skill,
	}, AgentOptions{
		MaxTurns: 5,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}

	if len(capturer.reqs) == 0 {
		t.Fatal("provider received no requests")
	}
	msgs := capturer.reqs[0].Messages
	if len(msgs) != 3 {
		t.Fatalf("Messages = %v, want 3 (system + skill inject + user args)", msgs)
	}
	if msgs[0].Role != provider.RoleSystem {
		t.Errorf("Messages[0].Role = %q, want %q", msgs[0].Role, provider.RoleSystem)
	}
	if msgs[1].Role != provider.RoleSystem {
		t.Errorf("Messages[1].Role = %q, want %q (skill payload as a system prefix)", msgs[1].Role, provider.RoleSystem)
	}
	if msgs[1].Content != skill {
		t.Errorf("Messages[1].Content = %q, want the injected skill payload %q", msgs[1].Content, skill)
	}
	if msgs[2].Role != provider.RoleUser || msgs[2].Content != "improve this" {
		t.Errorf("Messages[2] = %+v, want the user args turn", msgs[2])
	}
}

func TestRunAgentWithoutSkillInjectKeepsExistingShape(t *testing.T) {
	t.Parallel()
	var capturer capturedRequests
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		capturer.reqs = append(capturer.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "done", FinishReason: "stop", Done: true}), nil
	}), &mockTranscript{})

	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:  "deepseek-v4-flash",
		Prompt: "hello",
	}, AgentOptions{MaxTurns: 5}); err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}

	msgs := capturer.reqs[0].Messages
	if len(msgs) != 2 {
		t.Fatalf("Messages = %v, want 2 (system + user) with no skill inject", msgs)
	}
	if msgs[1].Content != "hello" {
		t.Errorf("Messages[1].Content = %q, want %q", msgs[1].Content, "hello")
	}
}
