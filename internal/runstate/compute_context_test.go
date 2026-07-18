package runstate

import (
	"testing"

	"github.com/glemsom/eitri/internal/litellm"
)

func TestComputeContext_EmptyInput(t *testing.T) {
	t.Parallel()

	result := ComputeContext([]litellm.Message{}, 128000)

	if result == nil {
		t.Fatal("ComputeContext returned nil")
	}
	if result.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0", result.TotalTokens)
	}
	if result.ContextWindow != 128000 {
		t.Errorf("ContextWindow = %d, want 128000", result.ContextWindow)
	}
	if result.PromptTokens != 0 {
		t.Errorf("PromptTokens = %d, want 0", result.PromptTokens)
	}
	if result.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0", result.CompletionTokens)
	}
	if result.SystemTokens != 0 {
		t.Errorf("SystemTokens = %d, want 0", result.SystemTokens)
	}
	if result.HistoryTokens != 0 {
		t.Errorf("HistoryTokens = %d, want 0", result.HistoryTokens)
	}
	if result.SkillTokens != 0 {
		t.Errorf("SkillTokens = %d, want 0", result.SkillTokens)
	}
}

func TestComputeContext_SystemPromptOnly(t *testing.T) {
	t.Parallel()

	msgs := []litellm.Message{
		{Role: "system", Content: "You are a helpful assistant."},
	}

	result := ComputeContext(msgs, 128000)

	if result == nil {
		t.Fatal("ComputeContext returned nil")
	}
	if result.SystemTokens <= 0 {
		t.Errorf("SystemTokens = %d, want > 0", result.SystemTokens)
	}
	if result.HistoryTokens != 0 {
		t.Errorf("HistoryTokens = %d, want 0", result.HistoryTokens)
	}
	if result.PromptTokens != result.SystemTokens {
		t.Errorf("PromptTokens = %d, want SystemTokens %d", result.PromptTokens, result.SystemTokens)
	}
	if result.TotalTokens != result.PromptTokens {
		t.Errorf("TotalTokens = %d, want PromptTokens %d", result.TotalTokens, result.PromptTokens)
	}
}

func TestComputeContext_SystemWithSingleExchange(t *testing.T) {
	t.Parallel()

	msgs := []litellm.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hello!"},
		{Role: "assistant", Content: "Hi there! How can I help?"},
	}

	result := ComputeContext(msgs, 128000)

	if result == nil {
		t.Fatal("ComputeContext returned nil")
	}
	if result.SystemTokens <= 0 {
		t.Errorf("SystemTokens = %d, want > 0", result.SystemTokens)
	}
	if result.HistoryTokens <= 0 {
		t.Errorf("HistoryTokens = %d, want > 0", result.HistoryTokens)
	}
	if result.PromptTokens != result.SystemTokens+result.HistoryTokens {
		t.Errorf("PromptTokens = %d, want SystemTokens(%d) + HistoryTokens(%d) = %d",
			result.PromptTokens, result.SystemTokens, result.HistoryTokens,
			result.SystemTokens+result.HistoryTokens)
	}
	if result.TotalTokens != result.PromptTokens {
		t.Errorf("TotalTokens = %d, want PromptTokens %d", result.TotalTokens, result.PromptTokens)
	}
}

func TestComputeContext_SystemWithHistoryAndSkills(t *testing.T) {
	t.Parallel()

	msgs := []litellm.Message{
		{
			Role:    "system",
			Content: "You are a coder.\n\nActivated skill: bash\n\nYou can run bash commands.",
		},
		{Role: "user", Content: "List files"},
		{Role: "assistant", Content: "Sure, let me list them."},
		{Role: "tool", Content: "file1.txt\nfile2.txt", ToolCallID: "call_1"},
	}

	result := ComputeContext(msgs, 128000)

	if result == nil {
		t.Fatal("ComputeContext returned nil")
	}
	if result.SystemTokens <= 0 {
		t.Errorf("SystemTokens = %d, want > 0", result.SystemTokens)
	}
	if result.SkillTokens <= 0 {
		t.Errorf("SkillTokens = %d, want > 0 (system prompt contains 'Activated skill')", result.SkillTokens)
	}
	if result.HistoryTokens <= 0 {
		t.Errorf("HistoryTokens = %d, want > 0", result.HistoryTokens)
	}
	// Skill tokens are part of system, not separate
	if result.PromptTokens != result.SystemTokens+result.HistoryTokens {
		t.Errorf("PromptTokens = %d, want SystemTokens(%d) + HistoryTokens(%d) + SkillTokens(%d) = %d",
			result.PromptTokens, result.SystemTokens, result.HistoryTokens, result.SkillTokens,
			result.SystemTokens+result.HistoryTokens)
	}
}

func TestComputeContext_SkillTokensExtractedCorrectly(t *testing.T) {
	t.Parallel()

	// System prompt with skill content AFTER "Activated skill"
	skillContent := "## bash\nYou can run bash commands with full shell access."
	msgs := []litellm.Message{
		{
			Role:    "system",
			Content: "You are a coder.\n\nActivated skill:" + skillContent,
		},
	}

	result := ComputeContext(msgs, 128000)

	if result == nil {
		t.Fatal("ComputeContext returned nil")
	}
	if result.SkillTokens <= 0 {
		t.Errorf("SkillTokens = %d, want > 0", result.SkillTokens)
	}
	// System tokens should include skill tokens (they're part of system prompt)
	if result.SystemTokens < result.SkillTokens {
		t.Errorf("SystemTokens(%d) should be >= SkillTokens(%d), since skills are part of system prompt",
			result.SystemTokens, result.SkillTokens)
	}
}

func TestComputeContext_LargeContent(t *testing.T) {
	t.Parallel()

	// Create a large message to stress the 4-char heuristic
	largeContent := make([]byte, 10000)
	for i := range largeContent {
		largeContent[i] = 'a'
	}

	msgs := []litellm.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: string(largeContent)},
	}

	result := ComputeContext(msgs, 128000)

	if result == nil {
		t.Fatal("ComputeContext returned nil")
	}
	if result.HistoryTokens <= 0 {
		t.Errorf("HistoryTokens = %d, want > 0", result.HistoryTokens)
	}
	// 10000 chars / 4 = 2500 tokens, allow some rounding
	if result.HistoryTokens < 2400 || result.HistoryTokens > 2600 {
		t.Errorf("HistoryTokens = %d, want ~2500 (10000 chars / 4)", result.HistoryTokens)
	}
}

func TestComputeContext_MultipleSystemMessages(t *testing.T) {
	t.Parallel()

	msgs := []litellm.Message{
		{Role: "system", Content: "First system message."},
		{Role: "system", Content: "Second system message."},
		{Role: "user", Content: "Hello!"},
	}

	result := ComputeContext(msgs, 128000)

	if result == nil {
		t.Fatal("ComputeContext returned nil")
	}
	// Both system messages counted
	firstTokens := len("First system message.") / 4
	secondTokens := len("Second system message.") / 4
	expectedSystemTokens := firstTokens + secondTokens
	if result.SystemTokens < expectedSystemTokens {
		t.Errorf("SystemTokens = %d, want >= %d (both system messages counted)", result.SystemTokens, expectedSystemTokens)
	}
	if result.HistoryTokens <= 0 {
		t.Errorf("HistoryTokens = %d, want > 0 (user message counted as history)", result.HistoryTokens)
	}
}

func TestComputeContext_NoSkillNoActivatedSkill(t *testing.T) {
	t.Parallel()

	msgs := []litellm.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hi"},
	}

	result := ComputeContext(msgs, 128000)

	if result == nil {
		t.Fatal("ComputeContext returned nil")
	}
	if result.SkillTokens != 0 {
		t.Errorf("SkillTokens = %d, want 0 (no 'Activated skill' in system prompt)", result.SkillTokens)
	}
}

func TestComputeContext_CustomContextWindow(t *testing.T) {
	t.Parallel()

	msgs := []litellm.Message{
		{Role: "system", Content: "You are helpful."},
	}

	result := ComputeContext(msgs, 32000)

	if result == nil {
		t.Fatal("ComputeContext returned nil")
	}
	if result.ContextWindow != 32000 {
		t.Errorf("ContextWindow = %d, want 32000", result.ContextWindow)
	}
}

func TestComputeContext_ToolRoleIsHistory(t *testing.T) {
	t.Parallel()

	msgs := []litellm.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "run command"},
		{Role: "assistant", Content: "", ToolCalls: []litellm.ToolCall{{ID: "call_1", Function: litellm.FunctionCall{Name: "bash", Arguments: "{}"}}}},
		{Role: "tool", Content: "output", ToolCallID: "call_1"},
	}

	result := ComputeContext(msgs, 128000)

	if result == nil {
		t.Fatal("ComputeContext returned nil")
	}
	if result.HistoryTokens <= 0 {
		t.Errorf("HistoryTokens = %d, want > 0", result.HistoryTokens)
	}
	// All non-system messages counted as history (assistant content is empty)
	if result.HistoryTokens < 1 {
		t.Errorf("HistoryTokens = %d, want >= 1", result.HistoryTokens)
	}
}

func TestComputeContext_CompletionTokensZero(t *testing.T) {
	t.Parallel()

	msgs := []litellm.Message{
		{Role: "system", Content: "You are helpful."},
	}

	result := ComputeContext(msgs, 128000)

	if result == nil {
		t.Fatal("ComputeContext returned nil")
	}
	if result.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0 (set by caller)", result.CompletionTokens)
	}
}
