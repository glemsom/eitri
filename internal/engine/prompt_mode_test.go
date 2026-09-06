package engine

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// TestRunAgentSelectsPromptHead guards that the request head is the
// byte-stable embedded prompt in every session mode.
func TestRunAgentSelectsPromptHead(t *testing.T) {
	t.Parallel()
	c := &skillIndexCaptureHandler{}
	e := New(provider.NewScripted(c.stream), nil)

	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:  "deepseek-v4-flash",
		Prompt: "hi",
	}, AgentOptions{MaxTurns: 1}); err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if len(c.requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(c.requests))
	}
	head := c.requests[0].Messages[0]
	if head.Role != provider.RoleSystem || head.Content != SystemPromptContent() {
		t.Fatalf("session head = %q, want byte-identical SystemPromptContent", head.Content)
	}
}

// TestIsSystemPromptHeadRecognizesHead guards the prompt-head detection path
// shared by message partitioning: the request head is the request head, and a
// run-state directive never is.
func TestIsSystemPromptHeadRecognizesHead(t *testing.T) {
	t.Parallel()
	if !isSystemPromptHead(SystemPromptContent()) {
		t.Error("prompt head not recognized")
	}
	if isSystemPromptHead("## Working directory\n/workspace") {
		t.Error("workspace directive misdetected as prompt head")
	}
}
