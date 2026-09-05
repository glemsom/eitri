package engine

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// TestRunAgentSelectsPromptHeadByMode guards that the request head is chosen by
// session mode: a default session keeps the byte-stable embedded prompt, and a
// yolo (--yolo-unsafe) session selects the honest unsandboxed variant.
func TestRunAgentSelectsPromptHeadByMode(t *testing.T) {
	t.Parallel()
	c := &skillIndexCaptureHandler{}
	e := New(provider.NewScripted(c.stream), nil)

	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:  "deepseek-v4-flash",
		Prompt: "hi",
	}, AgentOptions{MaxTurns: 1}); err != nil {
		t.Fatalf("RunAgent(error=%v), want nil", err)
	}
	if len(c.requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(c.requests))
	}
	head := c.requests[0].Messages[0]
	if head.Role != provider.RoleSystem || head.Content != SystemPromptContent() {
		t.Fatalf("default session head = %q, want byte-identical SystemPromptContent", head.Content)
	}

	c.requests = nil
	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:  "deepseek-v4-flash",
		Prompt: "hi",
		Yolo:   true,
	}, AgentOptions{MaxTurns: 1}); err != nil {
		t.Fatalf("RunAgent(yolo) error = %v, want nil", err)
	}
	if len(c.requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(c.requests))
	}
	head = c.requests[0].Messages[0]
	if head.Role != provider.RoleSystem || head.Content != SystemPromptYoloContent() {
		t.Fatalf("yolo session head = %q, want SystemPromptYoloContent", head.Content)
	}
}

// TestIsSystemPromptHeadRecognizesBothVariants guards the prompt-head detection
// path shared by message partitioning: either mode's request head is the
// request head, and a run-state directive never is.
func TestIsSystemPromptHeadRecognizesBothVariants(t *testing.T) {
	t.Parallel()
	if !isSystemPromptHead(SystemPromptContent()) {
		t.Error("default prompt head not recognized")
	}
	if !isSystemPromptHead(SystemPromptYoloContent()) {
		t.Error("yolo prompt head not recognized")
	}
	if isSystemPromptHead("## Working directory\n/workspace") {
		t.Error("workspace directive misdetected as prompt head")
	}
}
