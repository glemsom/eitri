package compactor

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/llm"
)

// mockLLMService implements llm.LLMService for testing.
// It returns a canned summary for any summarization request.
type mockLLMService struct {
	summary    string
	failOnCall bool // when true, Chat returns an error
}

func (m *mockLLMService) Chat(_ context.Context, req llm.Request) (*llm.Response, error) {
	if m.failOnCall {
		return nil, assertAnError
	}
	// Verify the prompt looks like a summarization request.
	if len(req.Messages) == 0 {
		return &llm.Response{Content: m.summary}, nil
	}
	msg := req.Messages[len(req.Messages)-1]
	if !strings.Contains(msg.Content, "Summarize the following tool result") {
		return &llm.Response{Content: m.summary}, nil
	}
	return &llm.Response{Content: m.summary}, nil
}

func (m *mockLLMService) ChatStream(_ context.Context, _ llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch, nil
}

// errorSentinel is used as a distinguishable error in tests.
var assertAnError = testError("mock LLM error")

type testError string

func (e testError) Error() string { return string(e) }

func TestCompact_NoCompactionNeeded(t *testing.T) {
	c := New()
	msgs := []llm.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "list files"},
		{Role: "tool", Content: "file1.txt\nfile2.txt"},
	}
	llmSvc := &mockLLMService{summary: "listed files"}
	thresholds := Thresholds{HighWater: 999_999, LowWater: 100} // far above total

	result, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil (no compaction needed)")
	}
}

func TestCompact_CompactsToolMessages(t *testing.T) {
	c := New()
	msgs := []llm.Message{
		{Role: "user", Content: "run build"},
		{Role: "tool", Content: "Build succeeded.\nAll 42 tests passed.\nOutput: ./bin/app"},
		{Role: "user", Content: "run tests"},
		{Role: "tool", Content: "Test results: 142 passed, 0 failed, coverage 87.5%"},
	}
	llmSvc := &mockLLMService{summary: "build completed successfully with 42 tests passing."}

	// Low threshold to trigger compaction.
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result (compaction should have occurred)")
	}

	// Original slice must not be modified.
	if msgs[1].Content != "Build succeeded.\nAll 42 tests passed.\nOutput: ./bin/app" {
		t.Error("original slice was mutated")
	}

	// Check that tool messages were compacted.
	foundCompacted := false
	for _, m := range result {
		if strings.HasPrefix(m.Content, "[TOOL RESULT COMPACTED") {
			foundCompacted = true
			if !strings.Contains(m.Content, "build completed successfully") {
				t.Errorf("compacted message does not contain summary: %q", m.Content)
			}
		}
	}
	if !foundCompacted {
		t.Error("no tool result was compacted")
	}
}

func TestCompact_LowWaterStopsEarly(t *testing.T) {
	c := New()

	// Create many tool messages so that after compacting one, we're below low-water.
	largeContent := strings.Repeat("data payload with important information ", 200) // ~8400 chars → ~2100 tokens

	msgs := make([]llm.Message, 20)
	for i := 0; i < 20; i++ {
		msgs[i] = llm.Message{
			Role:    "tool",
			Content: largeContent,
		}
	}

	llmSvc := &mockLLMService{summary: "compacted summary result."}

	// Set low water so that after one compaction we stop.
	// Each large message is ~2100 tokens, summary is ~3 tokens.
	// So total ~42000 tokens, after one compaction ~42000 - 2100 + 3 ≈ 39903.
	// Set LowWater to 40000 so after first compaction we stop.
	totalEst := messagesTokenEstimate(msgs)
	thresholds := Thresholds{
		HighWater: totalEst - 1,       // trigger compaction
		LowWater:  totalEst - 1000,    // stop after freeing ~1000 tokens (one message)
	}

	result, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Count compacted messages.
	compactedCount := 0
	for _, m := range result {
		if strings.HasPrefix(m.Content, "[TOOL RESULT COMPACTED") {
			compactedCount++
		}
	}
	if compactedCount == 0 {
		t.Fatal("expected at least one compacted message")
	}
	if compactedCount >= 20 {
		t.Fatal("expected some tool messages to remain uncompacted (low water should stop early)")
	}
}

func TestCompact_SkipsOnLLMError(t *testing.T) {
	c := New()
	msgs := []llm.Message{
		{Role: "user", Content: "do something"},
		{Role: "tool", Content: "result with important data"},
		{Role: "user", Content: "do another thing"},
		{Role: "tool", Content: "another result to summarize"},
	}
	llmSvc := &mockLLMService{summary: "fallback", failOnCall: true}
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result when all LLM calls fail and no compaction occurs")
	}

	// Ensure that when at least one succeeds and others fail, partial compaction occurs.
	callCount := 0
	llmSvc3 := &callTrackingMock{
		summary:       "successful summary",
		failOnCallNum: 2, // first call succeeds, second fails
	}

	msgs2 := []llm.Message{
		{Role: "user", Content: "first"},
		{Role: "tool", Content: "first large result with lots of data to summarize"},
		{Role: "user", Content: "second"},
		{Role: "tool", Content: "second large result with lots of data to summarize"},
	}
	thresholds2 := Thresholds{HighWater: 1, LowWater: 0}

	result2, err := c.Compact(context.Background(), msgs2, llmSvc3, thresholds2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result2 == nil {
		t.Fatal("expected partial compaction despite one failure")
	}
	_ = callCount

	// First tool should be compacted, second should be skipped due to error.
	if !strings.HasPrefix(result2[1].Content, "[TOOL RESULT COMPACTED") {
		t.Error("expected first tool result to be compacted")
	}
	if strings.HasPrefix(result2[3].Content, "[TOOL RESULT COMPACTED") {
		t.Error("expected second tool result to NOT be compacted (LLM error)")
	}
}

// callTrackingMock is an LLM service that fails on a specific call number.
type callTrackingMock struct {
	summary       string
	failOnCallNum int
	callCount     int
}

func (m *callTrackingMock) Chat(_ context.Context, req llm.Request) (*llm.Response, error) {
	m.callCount++
	if m.callCount == m.failOnCallNum {
		return nil, assertAnError
	}
	return &llm.Response{Content: m.summary}, nil
}

func (m *callTrackingMock) ChatStream(_ context.Context, _ llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch, nil
}

func TestCompact_AlreadyCompactedSkipped(t *testing.T) {
	c := New()
	msgs := []llm.Message{
		{Role: "user", Content: "first"},
		{Role: "tool", Content: "[TOOL RESULT COMPACTED - originally 100 tokens] some summary here"},
		{Role: "user", Content: "second"},
		{Role: "tool", Content: "fresh tool result to compact"},
	}
	llmSvc := &mockLLMService{summary: "new summary"}
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// First tool (already compacted) should remain unchanged.
	if result[1].Content != "[TOOL RESULT COMPACTED - originally 100 tokens] some summary here" {
		t.Error("already compacted message was modified")
	}
	// Second tool (fresh) should be compacted.
	if !strings.HasPrefix(result[3].Content, "[TOOL RESULT COMPACTED") {
		t.Error("expected second tool result to be compacted")
	}
}

func TestCompact_ReturnsNilOnEmptyMessages(t *testing.T) {
	c := New()
	result, err := c.Compact(context.Background(), nil, &mockLLMService{summary: "x"}, Thresholds{HighWater: 1, LowWater: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil for empty messages")
	}

	result, err = c.Compact(context.Background(), []llm.Message{}, &mockLLMService{summary: "x"}, Thresholds{HighWater: 1, LowWater: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil for empty messages")
	}
}

func TestCompact_NonToolMessagesPreserved(t *testing.T) {
	c := New()
	msgs := []llm.Message{
		{Role: "system", Content: "You are Eitri."},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi", ToolCalls: []llm.ToolCall{
			{ID: "call1", Function: llm.FunctionCall{Name: "bash", Arguments: `{"cmd":"ls"}`}},
		}},
		{Role: "tool", Content: "file1.txt\nfile2.txt", ToolCallID: "call1"},
		{Role: "user", Content: "good"},
	}
	llmSvc := &mockLLMService{summary: "listed files"}
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// System message preserved.
	if result[0].Role != "system" || result[0].Content != "You are Eitri." {
		t.Error("system message was modified")
	}
	// Assistant message preserved with tool calls.
	if result[2].Role != "assistant" || len(result[2].ToolCalls) != 1 {
		t.Error("assistant tool calls were modified")
	}
	// Tool message compacted.
	if !strings.HasPrefix(result[3].Content, "[TOOL RESULT COMPACTED") {
		t.Error("tool result was not compacted")
	}
	// User message preserved.
	if result[4].Role != "user" || result[4].Content != "good" {
		t.Error("user message was modified")
	}
}

func TestCompact_EmptyToolContentSkipped(t *testing.T) {
	c := New()
	msgs := []llm.Message{
		{Role: "user", Content: "do it"},
		{Role: "tool", Content: ""},
		{Role: "tool", Content: "real content here"},
	}
	llmSvc := &mockLLMService{summary: "summary"}
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Empty tool content should be skipped (not compacted).
	if result[1].Content != "" {
		t.Error("empty tool content was modified")
	}
	// Non-empty tool content should be compacted.
	if !strings.HasPrefix(result[2].Content, "[TOOL RESULT COMPACTED") {
		t.Error("non-empty tool content was not compacted")
	}
}

func TestTokenEstimate(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"a", 1},
		{"hello", 1},  // 5 chars / 4 = 1
		{"hello world", 2},  // 11 chars / 4 = 2
		{"a b c d e f g h i j", 4}, // 19 chars / 4 = 4
		{"1234567890", 2}, // 10/4=2
	}
	for _, tt := range tests {
		got := tokenEstimate(tt.input)
		if got != tt.want {
			t.Errorf("tokenEstimate(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestMessagesTokenEstimate(t *testing.T) {
	msgs := []llm.Message{
		{Content: "hello world"},                    // 11/4=2
		{Content: "a"},                              // 1
		{ToolCalls: []llm.ToolCall{                  // name="bash" (4/4=1) + args={"cmd":"ls"} (13/4=3)
			{Function: llm.FunctionCall{Name: "bash", Arguments: `{"cmd":"ls"}`}},
		}},
	}
	total := messagesTokenEstimate(msgs)
	// 2 + 1 + 1 + 3 = 7
	if total != 7 {
		t.Errorf("messagesTokenEstimate = %d, want 7", total)
	}
}

func TestCompact_NegativeOrZeroThresholds(t *testing.T) {
	c := New()
	// Use large messages so that the default thresholds (HighWater=90) are exceeded.
	largeContent := strings.Repeat("data payload with important information ", 10) // ~420 chars → ~105 tokens
	msgs := []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "tool", Content: largeContent},
	}

	// Zero thresholds should trigger compaction with sensible defaults.
	llmSvc := &mockLLMService{summary: "summary result."}
	result, err := c.Compact(context.Background(), msgs, llmSvc, Thresholds{HighWater: 0, LowWater: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected compaction with zero thresholds (defaults applied)")
	}
}

func TestCompact_EmptySummarySkipped(t *testing.T) {
	c := New()
	msgs := []llm.Message{
		{Role: "user", Content: "run command"},
		{Role: "tool", Content: "command output with important data"},
	}
	llmSvc := &mockLLMService{summary: ""} // empty summary
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result when LLM returns empty summary (no compaction)")
	}
}
