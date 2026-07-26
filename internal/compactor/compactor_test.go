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

func TestCompact_AlwaysRunsRegardlessOfThresholds(t *testing.T) {
	c := New()
	msgs := []llm.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "list files"},
		{Role: "tool", Content: strings.Repeat("data payload line\n", 20)}, // large enough to save tokens
	}
	llmSvc := &mockLLMService{summary: "listed files"}
	// HighWater far above total — with the old gate this returned nil.
	// Now Compact always scans for tool results regardless.
	thresholds := Thresholds{HighWater: 999_999, LowWater: 100}

	result, count, freed, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result (compaction always runs now)")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}
	if freed <= 0 {
		t.Fatalf("expected freed > 0, got %d", freed)
	}
}

func TestCompact_CompactsToolMessages(t *testing.T) {
	c := New()
	msgs := []llm.Message{
		{Role: "user", Content: "run build"},
		{Role: "tool", Content: "Build succeeded.\nAll 42 tests passed.\nOutput: ./bin/app\n" + strings.Repeat("log line\n", 50)},
		{Role: "user", Content: "run tests"},
		{Role: "tool", Content: "Test results: 142 passed, 0 failed, coverage 87.5%\n" + strings.Repeat("detail\n", 50)},
	}
	llmSvc := &mockLLMService{summary: "build completed successfully with 42 tests passing."}

	// Low threshold to trigger compaction.
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, count, freed, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result (compaction should have occurred)")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}
	if freed <= 0 {
		t.Fatalf("expected freed > 0, got %d", freed)
	}

	// Original slice must not be modified.
	expectedOrig := "Build succeeded.\nAll 42 tests passed.\nOutput: ./bin/app\n" + strings.Repeat("log line\n", 50)
	if msgs[1].Content != expectedOrig {
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

	result, count, freed, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}
	if freed <= 0 {
		t.Fatalf("expected freed > 0, got %d", freed)
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

	result, count, freed, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result when all LLM calls fail and no compaction occurs")
	}
	if count != 0 || freed != 0 {
		t.Fatalf("expected count=0, freed=0; got count=%d, freed=%d", count, freed)
	}

	// Ensure that when at least one succeeds and others fail, partial compaction occurs.
	callCount := 0
	llmSvc3 := &callTrackingMock{
		summary:       "successful summary",
		failOnCallNum: 2, // first call succeeds, second fails
	}

	msgs2 := []llm.Message{
		{Role: "user", Content: "first"},
		{Role: "tool", Content: "first large result with lots of data " + strings.Repeat("x", 200)},
		{Role: "user", Content: "second"},
		{Role: "tool", Content: "second large result with lots of data " + strings.Repeat("y", 200)},
	}
	thresholds2 := Thresholds{HighWater: 1, LowWater: 0}

	result2, count2, freed2, err := c.Compact(context.Background(), msgs2, llmSvc3, thresholds2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result2 == nil {
		t.Fatal("expected partial compaction despite one failure")
	}
	if count2 == 0 {
		t.Fatal("expected at least one compacted message")
	}
	if freed2 <= 0 {
		t.Fatalf("expected freed > 0, got %d", freed2)
	}
	_ = callCount

	// With all roles eligible, the scan order is:
	//   msgs2[0]=user("first") → call 1 succeeds → compacted as [MESSAGE COMPACTED]
	//   msgs2[1]=tool("first large...") → call 2 fails → skipped
	//   msgs2[2]=user("second") → call 3 succeeds → compacted as [MESSAGE COMPACTED]
	//   msgs2[3]=tool("second large...") → call 4 succeeds → compacted as [TOOL RESULT COMPACTED]
	// First message (user) should be compacted.
	if !strings.HasPrefix(result2[0].Content, "[MESSAGE COMPACTED") {
		t.Error("expected first user message to be compacted")
	}
	// Second message (tool) should be skipped due to LLM error (call 2).
	if strings.HasPrefix(result2[1].Content, "[TOOL RESULT COMPACTED") {
		t.Error("expected first tool result to NOT be compacted (LLM error on call 2)")
	}
	// Third message (user) should be compacted.
	if !strings.HasPrefix(result2[2].Content, "[MESSAGE COMPACTED") {
		t.Error("expected second user message to be compacted")
	}
	// Fourth message (tool) should be compacted (call 4 succeeds).
	if !strings.HasPrefix(result2[3].Content, "[TOOL RESULT COMPACTED") {
		t.Error("expected second tool result to be compacted (call 4 succeeds)")
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
		{Role: "tool", Content: "fresh tool result to compact with lots of data " + strings.Repeat("z", 200)},
	}
	llmSvc := &mockLLMService{summary: "new summary"}
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, count, freed, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}
	if freed <= 0 {
		t.Fatalf("expected freed > 0, got %d", freed)
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
	result, count, freed, err := c.Compact(context.Background(), nil, &mockLLMService{summary: "x"}, Thresholds{HighWater: 1, LowWater: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil for empty messages")
	}
	if count != 0 || freed != 0 {
		t.Fatalf("expected count=0, freed=0; got count=%d, freed=%d", count, freed)
	}

	result, count, freed, err = c.Compact(context.Background(), []llm.Message{}, &mockLLMService{summary: "x"}, Thresholds{HighWater: 1, LowWater: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil for empty messages")
	}
	if count != 0 || freed != 0 {
		t.Fatalf("expected count=0, freed=0; got count=%d, freed=%d", count, freed)
	}
}

func TestCompact_CompactsAllRoles(t *testing.T) {
	c := New()
	msgs := []llm.Message{
		{Role: "system", Content: "You are Eitri."},
		{Role: "user", Content: strings.Repeat("hello ", 500)}, // large user message
		{Role: "assistant", Content: strings.Repeat("hi ", 500), ToolCalls: []llm.ToolCall{
			{ID: "call1", Function: llm.FunctionCall{Name: "bash", Arguments: `{"cmd":"ls"}`}},
		}},
		{Role: "tool", Content: "file1.txt\nfile2.txt\n" + strings.Repeat("data\n", 200), ToolCallID: "call1"},
		{Role: "user", Content: "good"},
	}
	llmSvc := &mockLLMService{summary: "summarized content"}
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, count, freed, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}
	if freed <= 0 {
		t.Fatalf("expected freed > 0, got %d", freed)
	}

	// System message preserved (never compacted).
	if result[0].Role != "system" || result[0].Content != "You are Eitri." {
		t.Error("system message was modified")
	}
	// User message compacted with generic prefix.
	if !strings.HasPrefix(result[1].Content, "[MESSAGE COMPACTED") {
		t.Error("user message was not compacted")
	}
	// Assistant message compacted but tool calls preserved.
	if result[2].Role != "assistant" || len(result[2].ToolCalls) != 1 {
		t.Error("assistant tool calls were lost during compaction")
	}
	if !strings.HasPrefix(result[2].Content, "[MESSAGE COMPACTED") {
		t.Error("assistant message was not compacted")
	}
	// Tool message compacted with tool-specific prefix.
	if !strings.HasPrefix(result[3].Content, "[TOOL RESULT COMPACTED") {
		t.Error("tool result was not compacted")
	}
	// Small user message (4 chars) is also compacted because threshold is 0.
	if !strings.HasPrefix(result[4].Content, "[MESSAGE COMPACTED") {
		t.Error("small user message should be compacted when MessageSizeThreshold is 0")
	}
}

func TestCompact_MessageSizeThreshold_SkipsSmallMessages(t *testing.T) {
	c := New()
	msgs := []llm.Message{
		{Role: "user", Content: "small"},                                          // ~1 token
		{Role: "user", Content: strings.Repeat("large payload ", 800)},            // ~10400 chars → ~2600 tokens
		{Role: "assistant", Content: "tiny"},                                      // ~1 token
		{Role: "assistant", Content: strings.Repeat("big response data ", 800)},   // ~10400 chars → ~2600 tokens
		{Role: "tool", Content: "tiny tool"},                                      // ~1 token
		{Role: "tool", Content: strings.Repeat("massive tool output\n", 400)},     // ~10400 chars → ~2600 tokens
	}
	llmSvc := &mockLLMService{summary: "summary"}
	thresholds := Thresholds{HighWater: 1, LowWater: 0, MessageSizeThreshold: 2000}

	result, count, freed, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}
	if freed <= 0 {
		t.Fatalf("expected freed > 0, got %d", freed)
	}

	// Small user message should NOT be compacted (below threshold).
	if result[0].Content != "small" {
		t.Error("small user message was compacted but should have been skipped by MessageSizeThreshold")
	}
	// Large user message should be compacted.
	if !strings.HasPrefix(result[1].Content, "[MESSAGE COMPACTED") {
		t.Error("large user message should be compacted")
	}
	// Small assistant message should NOT be compacted.
	if result[2].Content != "tiny" {
		t.Error("small assistant message was compacted but should have been skipped")
	}
	// Large assistant message should be compacted.
	if !strings.HasPrefix(result[3].Content, "[MESSAGE COMPACTED") {
		t.Error("large assistant message should be compacted")
	}
	// Small tool message should NOT be compacted (below threshold now).
	if result[4].Content != "tiny tool" {
		t.Error("small tool message was compacted but should have been skipped by MessageSizeThreshold")
	}
	// Large tool message should be compacted.
	if !strings.HasPrefix(result[5].Content, "[TOOL RESULT COMPACTED") {
		t.Error("large tool message should be compacted")
	}

	// All large messages are above threshold (2600 > 2000), so count should be 3.
	if count != 3 {
		t.Errorf("expected 3 compacted messages, got %d", count)
	}
}

func TestCompact_AlreadyCompactedDifferentPrefixes(t *testing.T) {
	c := New()
	msgs := []llm.Message{
		{Role: "user", Content: "[MESSAGE COMPACTED - originally 50 tokens] user summary here"},
		{Role: "tool", Content: "[TOOL RESULT COMPACTED - originally 100 tokens] tool summary here"},
		{Role: "user", Content: strings.Repeat("fresh user content ", 400)},
		{Role: "tool", Content: strings.Repeat("fresh tool content ", 400)},
	}
	llmSvc := &mockLLMService{summary: "new summary"}
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, count, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}

	// Already-compacted messages should remain unchanged.
	if result[0].Content != "[MESSAGE COMPACTED - originally 50 tokens] user summary here" {
		t.Error("already compacted user message was modified")
	}
	if result[1].Content != "[TOOL RESULT COMPACTED - originally 100 tokens] tool summary here" {
		t.Error("already compacted tool message was modified")
	}
	// Fresh messages should be compacted.
	if !strings.HasPrefix(result[2].Content, "[MESSAGE COMPACTED") {
		t.Error("fresh user message was not compacted")
	}
	if !strings.HasPrefix(result[3].Content, "[TOOL RESULT COMPACTED") {
		t.Error("fresh tool message was not compacted")
	}
}

func TestCompact_EmptyToolContentSkipped(t *testing.T) {
	c := New()
	msgs := []llm.Message{
		{Role: "user", Content: "do it"},
		{Role: "tool", Content: ""},
		{Role: "tool", Content: "real content here with lots of data " + strings.Repeat("w", 200)},
	}
	llmSvc := &mockLLMService{summary: "summary"}
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, count, freed, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}
	if freed <= 0 {
		t.Fatalf("expected freed > 0, got %d", freed)
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
	result, count, freed, err := c.Compact(context.Background(), msgs, llmSvc, Thresholds{HighWater: 0, LowWater: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected compaction with zero thresholds (defaults applied)")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}
	if freed <= 0 {
		t.Fatalf("expected freed > 0, got %d", freed)
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

	result, count, freed, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result when LLM returns empty summary (no compaction)")
	}
	if count != 0 || freed != 0 {
		t.Fatalf("expected count=0, freed=0; got count=%d, freed=%d", count, freed)
	}
}
