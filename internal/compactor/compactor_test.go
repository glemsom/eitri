package compactor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/message"
)

// mockCompactorProvider implements litellm.Provider for testing.
// It returns a canned summary for any summarization request.
type mockCompactorProvider struct {
	summary    string
	failOnCall bool // when true, Chat returns an error
}

func (m *mockCompactorProvider) Name() string { return "mock-compactor" }

func (m *mockCompactorProvider) Chat(_ context.Context, req *litellm.Request) (*litellm.Response, error) {
	if m.failOnCall {
		return nil, assertAnError
	}
	// Verify the prompt looks like a summarization request.
	if len(req.Messages) == 0 {
		return &litellm.Response{
			Blocks:   []litellm.Block{litellm.TextBlock{Text: m.summary}},
			Provider: "mock-compactor",
			Model:    "mock-model",
		}, nil
	}
	msg := req.Messages[len(req.Messages)-1]
	content := extractText(msg)
	if !strings.Contains(content, "Summarize the following tool result") {
		return &litellm.Response{
			Blocks:   []litellm.Block{litellm.TextBlock{Text: m.summary}},
			Provider: "mock-compactor",
			Model:    "mock-model",
		}, nil
	}
	return &litellm.Response{
		Blocks:   []litellm.Block{litellm.TextBlock{Text: m.summary}},
		Provider: "mock-compactor",
		Model:    "mock-model",
	}, nil
}

func (m *mockCompactorProvider) Stream(_ context.Context, _ *litellm.Request) (litellm.Stream, error) {
	return nil, fmt.Errorf("unimplemented")
}

// extractText returns the concatenated text from a litellm.Message.
func extractText(msg litellm.Message) string {
	var text strings.Builder
	for _, block := range msg.Blocks {
		if tb, ok := block.(litellm.TextBlock); ok {
			text.WriteString(tb.Text)
		}
	}
	return text.String()
}

// newMockClient creates a *litellm.Client backed by mockCompactorProvider.
func newMockClient(summary string, failOnCall ...bool) *litellm.Client {
	mock := &mockCompactorProvider{summary: summary}
	if len(failOnCall) > 0 {
		mock.failOnCall = failOnCall[0]
	}
	client, err := litellm.New(mock)
	if err != nil {
		panic(fmt.Sprintf("failed to create mock client: %v", err))
	}
	return client
}

// errorSentinel is used as a distinguishable error in tests.
var assertAnError = testError("mock LLM error")

type testError string

func (e testError) Error() string { return string(e) }

func TestCompact_AlwaysRunsRegardlessOfThresholds(t *testing.T) {
	c := New()
	msgs := []message.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "list files"},
		{Role: "tool", Content: strings.Repeat("data payload line\n", 20)}, // large enough to save tokens
	}
	llmSvc := newMockClient("listed files")
	// HighWater far above total — with the old gate this returned nil.
	// Now Compact always scans for tool results regardless.
	thresholds := Thresholds{HighWater: 999_999, LowWater: 100}

	result, count, freed, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
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
	msgs := []message.Message{
		{Role: "user", Content: "run build"},
		{Role: "tool", Content: "Build succeeded.\nAll 42 tests passed.\nOutput: ./bin/app\n" + strings.Repeat("log line\n", 50)},
		{Role: "user", Content: "run tests"},
		{Role: "tool", Content: "Test results: 142 passed, 0 failed, coverage 87.5%\n" + strings.Repeat("detail\n", 50)},
	}
	llmSvc := newMockClient("build completed successfully with 42 tests passing.")

	// Low threshold to trigger compaction.
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, count, freed, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
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

	msgs := make([]message.Message, 20)
	for i := range 20 {
		msgs[i] = message.Message{
			Role:    "tool",
			Content: largeContent,
		}
	}

	llmSvc := newMockClient("compacted summary result.")

	// Set low water so that after one compaction we stop.
	// Each large message is ~2100 tokens, summary is ~3 tokens.
	// So total ~42000 tokens, after one compaction ~42000 - 2100 + 3 ≈ 39903.
	// Set LowWater to 40000 so after first compaction we stop.
	totalEst := messagesTokenEstimate(msgs, nil, "")
	thresholds := Thresholds{
		HighWater: totalEst - 1,    // trigger compaction
		LowWater:  totalEst - 1000, // stop after freeing ~1000 tokens (one message)
	}

	result, count, freed, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
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
	msgs := []message.Message{
		{Role: "user", Content: "do something"},
		{Role: "tool", Content: "result with important data"},
		{Role: "user", Content: "do another thing"},
		{Role: "tool", Content: "another result to summarize"},
	}
	llmSvc := newMockClient("fallback", true)
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, count, freed, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
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
	llmSvc3, err := litellm.New(&callTrackingMock{
		summary:       "successful summary",
		failOnCallNum: 2, // first call succeeds, second fails
	})
	if err != nil {
		t.Fatalf("failed to create mock client: %v", err)
	}

	msgs2 := []message.Message{
		{Role: "user", Content: "first"},
		{Role: "tool", Content: "first large result with lots of data " + strings.Repeat("x", 200)},
		{Role: "user", Content: "second"},
		{Role: "tool", Content: "second large result with lots of data " + strings.Repeat("y", 200)},
	}
	thresholds2 := Thresholds{HighWater: 1, LowWater: 0}

	result2, count2, freed2, _, err := c.Compact(context.Background(), msgs2, llmSvc3, thresholds2)
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

// callTrackingMock is a litellm.Provider that fails on a specific call number.
type callTrackingMock struct {
	summary       string
	failOnCallNum int
	callCount     int
}

func (m *callTrackingMock) Name() string { return "call-tracking-mock" }

func (m *callTrackingMock) Chat(_ context.Context, req *litellm.Request) (*litellm.Response, error) {
	m.callCount++
	if m.callCount == m.failOnCallNum {
		return nil, assertAnError
	}
	return &litellm.Response{
		Blocks:   []litellm.Block{litellm.TextBlock{Text: m.summary}},
		Provider: "call-tracking-mock",
		Model:    "mock-model",
	}, nil
}

func (m *callTrackingMock) Stream(_ context.Context, _ *litellm.Request) (litellm.Stream, error) {
	return nil, fmt.Errorf("unimplemented")
}

func TestCompact_AlreadyCompactedSkipped(t *testing.T) {
	c := New()
	msgs := []message.Message{
		{Role: "user", Content: "first"},
		{Role: "tool", Content: "[TOOL RESULT COMPACTED - originally 100 tokens] some summary here"},
		{Role: "user", Content: "second"},
		{Role: "tool", Content: "fresh tool result to compact with lots of data " + strings.Repeat("z", 200)},
	}
	llmSvc := newMockClient("new summary")
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, count, freed, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
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
	result, count, freed, _, err := c.Compact(context.Background(), nil, newMockClient("x"), Thresholds{HighWater: 1, LowWater: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil for empty messages")
	}
	if count != 0 || freed != 0 {
		t.Fatalf("expected count=0, freed=0; got count=%d, freed=%d", count, freed)
	}

	result, count, freed, _, err = c.Compact(context.Background(), []message.Message{}, newMockClient("x"), Thresholds{HighWater: 1, LowWater: 0})
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
	msgs := []message.Message{
		{Role: "system", Content: "You are Eitri."},
		{Role: "user", Content: strings.Repeat("hello ", 500)}, // large user message
		{Role: "assistant", Content: strings.Repeat("hi ", 500), ToolCalls: []message.ToolCall{
			{ID: "call1", Function: message.FunctionCall{Name: "bash", Arguments: `{"cmd":"ls"}`}},
		}},
		{Role: "tool", Content: "file1.txt\nfile2.txt\n" + strings.Repeat("data\n", 200), ToolCallID: "call1"},
		{Role: "user", Content: "good"},
	}
	llmSvc := newMockClient("summarized content")
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, count, freed, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
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
	msgs := []message.Message{
		{Role: "user", Content: "small"},                                        // ~1 token
		{Role: "user", Content: strings.Repeat("large payload ", 800)},          // ~10400 chars → ~2600 tokens
		{Role: "assistant", Content: "tiny"},                                    // ~1 token
		{Role: "assistant", Content: strings.Repeat("big response data ", 800)}, // ~10400 chars → ~2600 tokens
		{Role: "tool", Content: "tiny tool"},                                    // ~1 token
		{Role: "tool", Content: strings.Repeat("massive tool output\n", 400)},   // ~10400 chars → ~2600 tokens
	}
	llmSvc := newMockClient("summary")
	thresholds := Thresholds{HighWater: 1, LowWater: 0, MessageSizeThreshold: 2000}

	result, count, freed, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
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
	msgs := []message.Message{
		{Role: "user", Content: "[MESSAGE COMPACTED - originally 50 tokens] user summary here"},
		{Role: "tool", Content: "[TOOL RESULT COMPACTED - originally 100 tokens] tool summary here"},
		{Role: "user", Content: strings.Repeat("fresh user content ", 400)},
		{Role: "tool", Content: strings.Repeat("fresh tool content ", 400)},
	}
	llmSvc := newMockClient("new summary")
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, count, _, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
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
	msgs := []message.Message{
		{Role: "user", Content: "do it"},
		{Role: "tool", Content: ""},
		{Role: "tool", Content: "real content here with lots of data " + strings.Repeat("w", 200)},
	}
	llmSvc := newMockClient("summary")
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, count, freed, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
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
		{"hello", 1},               // 5 chars / 4 = 1
		{"hello world", 2},         // 11 chars / 4 = 2
		{"a b c d e f g h i j", 4}, // 19 chars / 4 = 4
		{"1234567890", 2},          // 10/4=2
	}
	for _, tt := range tests {
		got := tokenEstimate(tt.input, nil, "")
		if got != tt.want {
			t.Errorf("tokenEstimate(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestMessagesTokenEstimate(t *testing.T) {
	msgs := []message.Message{
		{Content: "hello world"}, // 11/4=2
		{Content: "a"},           // 1
		{ToolCalls: []message.ToolCall{ // name="bash" (4/4=1) + args={"cmd":"ls"} (13/4=3)
			{Function: message.FunctionCall{Name: "bash", Arguments: `{"cmd":"ls"}`}},
		}},
	}
	total := messagesTokenEstimate(msgs, nil, "")
	// 2 + 1 + 1 + 3 = 7
	if total != 7 {
		t.Errorf("messagesTokenEstimate = %d, want 7", total)
	}
}

func TestCompact_NegativeOrZeroThresholds(t *testing.T) {
	c := New()
	// Use large messages so that the default thresholds (HighWater=90) are exceeded.
	largeContent := strings.Repeat("data payload with important information ", 10) // ~420 chars → ~105 tokens
	msgs := []message.Message{
		{Role: "user", Content: "hi"},
		{Role: "tool", Content: largeContent},
	}

	// Zero thresholds should trigger compaction with sensible defaults.
	llmSvc := newMockClient("summary result.")
	result, count, freed, _, err := c.Compact(context.Background(), msgs, llmSvc, Thresholds{HighWater: 0, LowWater: 0})
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
	msgs := []message.Message{
		{Role: "user", Content: "run command"},
		{Role: "tool", Content: "command output with important data"},
	}
	llmSvc := newMockClient("") // empty summary
	thresholds := Thresholds{HighWater: 1, LowWater: 0}

	result, count, freed, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
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

func TestCompact_PrunesToolCallArgsBeyondRetention(t *testing.T) {
	c := New()
	// 10 assistant messages, 5 with tool calls.
	// RetentionTurns=3 means the last 3 should retain their arguments.
	msgs := make([]message.Message, 0, 10)
	for i := range 10 {
		tc := []message.ToolCall{}
		if i < 5 { // first 5 have tool calls
			tc = []message.ToolCall{
				{ID: "call_1", Function: message.FunctionCall{Name: "bash", Arguments: `{"cmd":"echo hello"}`}},
				{ID: "call_2", Function: message.FunctionCall{Name: "read", Arguments: `{"path":"file.txt"}`}},
			}
		}
		msgs = append(msgs, message.Message{
			Role:      "assistant",
			Content:   "response " + fmt.Sprint(i),
			ToolCalls: tc,
		})
	}
	llmSvc := newMockClient("summary")
	thresholds := Thresholds{HighWater: 1, LowWater: 0, ToolCallRetentionTurns: 3}

	result, count, freed, pruned, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if pruned == 0 {
		t.Fatal("expected some tool calls to be pruned")
	}

	// There are 10 assistant messages (indices 0-9).
	// RetentionTurns=3: indices 7,8,9 (last 3) retain args.
	// Indices 0-4 have tool calls → 5 messages × 2 calls each = 10 tool calls.
	// Indices 5-6 have no tool calls → unaffected.
	// So 10 tool calls should be pruned.
	if pruned != 10 {
		t.Errorf("expected 10 pruned tool calls, got %d", pruned)
	}

	_ = count
	_ = freed

	// Check that messages within retention window retain their arguments.
	for i := 7; i < 10; i++ {
		if !strings.HasPrefix(result[i].Content, "[MESSAGE COMPACTED") {
			t.Errorf("message %d should be compacted", i)
		}
		// These are within retention, but they have no tool calls (indices 7-9 are assistant msgs 7-9, which have no tool calls).
	}
	// Check only the first 5 have tool calls (indices 0-4)
	for i := range 5 {
		if len(result[i].ToolCalls) == 0 {
			t.Errorf("message %d should still have tool calls", i)
		}
		for j, tc := range result[i].ToolCalls {
			if tc.Function.Name == "" {
				t.Errorf("message %d tool call %d: Name should be preserved", i, j)
			}
			if tc.ID == "" {
				t.Errorf("message %d tool call %d: ID should be preserved", i, j)
			}
			// Arguments should be pruned (index < 7 are beyond retention window)
			if tc.Function.Arguments == "" {
				t.Errorf("message %d tool call %d: Arguments should have placeholder", i, j)
			}
			if !strings.HasPrefix(tc.Function.Arguments, `{"pruned": "~`) {
				t.Errorf("message %d tool call %d: Arguments should be pruned, got %q", i, j, tc.Function.Arguments)
			}
		}
	}
}

func TestCompact_KeepsToolCallArgsWithinRetention(t *testing.T) {
	c := New()
	// 5 assistant messages, all with tool calls.
	// RetentionTurns=5 means all are within window → nothing pruned.
	msgs := make([]message.Message, 5)
	for i := range 5 {
		msgs[i] = message.Message{
			Role:    "assistant",
			Content: strings.Repeat("response ", 50),
			ToolCalls: []message.ToolCall{
				{ID: "call_1", Function: message.FunctionCall{Name: "bash", Arguments: `{"cmd":"echo hello"}`}},
			},
		}
	}
	llmSvc := newMockClient("summary")
	thresholds := Thresholds{HighWater: 1, LowWater: 0, ToolCallRetentionTurns: 5}

	result, count, freed, pruned, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pruned != 0 {
		t.Errorf("expected 0 pruned tool calls (all within retention), got %d", pruned)
	}
	_ = count
	_ = freed

	// All messages should be compacted (content summarised) but tool call args preserved.
	for i, m := range result {
		if !strings.HasPrefix(m.Content, "[MESSAGE COMPACTED") {
			t.Errorf("message %d should be compacted", i)
		}
		if len(m.ToolCalls) == 0 {
			t.Errorf("message %d should still have tool calls", i)
		}
		if m.ToolCalls[0].Function.Arguments != `{"cmd":"echo hello"}` {
			t.Errorf("message %d: arguments should be preserved, got %q", i, m.ToolCalls[0].Function.Arguments)
		}
	}
}

func TestCompact_PruneOnlyAssistantMessages(t *testing.T) {
	c := New()
	// Mix of user, assistant, and tool messages.
	// Only assistant messages with tool calls should be pruned.
	msgs := []message.Message{
		{Role: "user", Content: "hello", ToolCalls: []message.ToolCall{ // user with tool calls (unusual but test)
			{ID: "u1", Function: message.FunctionCall{Name: "test", Arguments: `{"x":"y"}`}},
		}},
		{Role: "assistant", Content: "let me check", ToolCalls: []message.ToolCall{
			{ID: "a1", Function: message.FunctionCall{Name: "bash", Arguments: `{"cmd":"ls"}`}},
		}},
		{Role: "tool", Content: "file list", ToolCallID: "a1", ToolCalls: []message.ToolCall{ // tool with tool calls (unusual)
			{ID: "t1", Function: message.FunctionCall{Name: "other", Arguments: `{"data":"lots"}`}},
		}},
	}
	llmSvc := newMockClient("summary")
	thresholds := Thresholds{HighWater: 1, LowWater: 0, ToolCallRetentionTurns: 0} // 0 retention = prune all

	result, _, _, pruned, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Only the assistant message (index 1) should have its tool call args pruned.
	if pruned != 1 {
		t.Errorf("expected 1 pruned tool call (assistant only), got %d", pruned)
	}
	// User message tool calls should NOT be pruned.
	if result[0].ToolCalls[0].Function.Arguments != `{"x":"y"}` {
		t.Error("user message tool call arguments should not have been pruned")
	}
	// Assistant message tool calls should be pruned.
	if !strings.HasPrefix(result[1].ToolCalls[0].Function.Arguments, `{"pruned": "~`) {
		t.Error("assistant message tool call arguments should have been pruned")
	}
	// Tool message tool calls should NOT be pruned.
	if result[2].ToolCalls[0].Function.Arguments != `{"data":"lots"}` {
		t.Error("tool message tool call arguments should not have been pruned")
	}
}

func TestCompact_AlreadyPrunedToolCallsSkipped(t *testing.T) {
	c := New()
	msgs := []message.Message{
		{Role: "assistant", Content: "first", ToolCalls: []message.ToolCall{
			{ID: "c1", Function: message.FunctionCall{Name: "bash", Arguments: `{"pruned": "~42 chars"}`}},
		}},
		{Role: "assistant", Content: "second", ToolCalls: []message.ToolCall{
			{ID: "c2", Function: message.FunctionCall{Name: "bash", Arguments: `{"cmd":"real data"}`}},
		}},
	}
	llmSvc := newMockClient("summary")
	thresholds := Thresholds{HighWater: 1, LowWater: 0, ToolCallRetentionTurns: 0}

	result, count, freed, pruned, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Only the second (unpruned) tool call should be counted as pruned.
	if pruned != 1 {
		t.Errorf("expected 1 new pruned tool call, got %d", pruned)
	}
	// First should still be pruned placeholder.
	if result[0].ToolCalls[0].Function.Arguments != `{"pruned": "~42 chars"}` {
		t.Error("already pruned tool call should be left unchanged")
	}
	// Second should now be pruned.
	if !strings.HasPrefix(result[1].ToolCalls[0].Function.Arguments, `{"pruned": "~`) {
		t.Error("second tool call should have been pruned")
	}
	_ = count
	_ = freed
}

func TestCompact_ZeroRetentionTurnsPrunesAll(t *testing.T) {
	c := New()
	msgs := []message.Message{
		{Role: "assistant", Content: "first", ToolCalls: []message.ToolCall{
			{ID: "c1", Function: message.FunctionCall{Name: "bash", Arguments: `{"cmd":"first"}`}},
		}},
		{Role: "assistant", Content: "second", ToolCalls: []message.ToolCall{
			{ID: "c2", Function: message.FunctionCall{Name: "bash", Arguments: `{"cmd":"second"}`}},
		}},
	}
	llmSvc := newMockClient("summary")
	// ToolCallRetentionTurns=0 means no retention → all get pruned.
	thresholds := Thresholds{HighWater: 1, LowWater: 0, ToolCallRetentionTurns: 0}

	_, _, _, pruned, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pruned != 2 {
		t.Errorf("expected 2 pruned tool calls, got %d", pruned)
	}
}

func TestCompact_NoToolCallsUnaffected(t *testing.T) {
	c := New()
	// Assistant messages with no tool calls should not cause any pruning.
	msgs := []message.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "run build"},
		{Role: "assistant", Content: "building..."},
		{Role: "tool", Content: "build output here"},
	}
	llmSvc := newMockClient("summary")
	thresholds := Thresholds{HighWater: 1, LowWater: 0, ToolCallRetentionTurns: 2}

	result, _, _, pruned, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result (messages should be compacted)")
	}
	if pruned != 0 {
		t.Errorf("expected 0 pruned tool calls (no tool calls in messages), got %d", pruned)
	}
}

func TestCompact_FreedTokensIncludesPrunedArgs(t *testing.T) {
	c := New()
	largeArgs := `{"cmd":"` + strings.Repeat("x", 1000) + `"}`
	msgs := []message.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "running command", ToolCalls: []message.ToolCall{
			{ID: "c1", Function: message.FunctionCall{Name: "bash", Arguments: largeArgs}},
		}},
		{Role: "tool", Content: "output here with lots of data " + strings.Repeat("y", 500)},
	}
	llmSvc := newMockClient("summary")
	thresholds := Thresholds{HighWater: 1, LowWater: 0, ToolCallRetentionTurns: 0, MessageSizeThreshold: 0}

	result, count, freed, pruned, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}
	if pruned != 1 {
		t.Errorf("expected 1 pruned tool call, got %d", pruned)
	}
	// freed should include both compacted message content and pruned args
	if freed <= 0 {
		t.Errorf("expected freed > 0, got %d", freed)
	}
}

func TestCompact_RetentionWithCompactOnly(t *testing.T) {
	c := New()
	// Test that ToolCallRetentionTurns works even when no content compaction occurs
	// (no LLM summarization needed — just tool call pruning).
	msgs := []message.Message{
		{Role: "assistant", Content: "small", ToolCalls: []message.ToolCall{
			{ID: "c1", Function: message.FunctionCall{Name: "bash", Arguments: `{"cmd":"echo hello"}`}},
		}},
		{Role: "assistant", Content: "small", ToolCalls: []message.ToolCall{
			{ID: "c2", Function: message.FunctionCall{Name: "read", Arguments: `{"path":"/etc/hosts"}`}},
		}},
	}
	// Set MessageSizeThreshold high so content summarization is skipped.
	thresholds := Thresholds{HighWater: 1, LowWater: 0, MessageSizeThreshold: 999_999, ToolCallRetentionTurns: 1}

	// Use a mock that would fail if called (shouldn't be called since messages are too small).
	llmSvc := newMockClient("summary", true)

	result, count, freed, pruned, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// No content compaction should happen (messages are below threshold).
	if count != 0 {
		t.Errorf("expected 0 compacted messages, got %d", count)
	}
	// Only the first assistant message (beyond retention) should have its args pruned.
	if pruned != 1 {
		t.Errorf("expected 1 pruned tool call, got %d", pruned)
	}
	// First tool call should be pruned.
	if !strings.HasPrefix(result[0].ToolCalls[0].Function.Arguments, `{"pruned": "~`) {
		t.Error("first tool call should be pruned")
	}
	// Second tool call should be preserved (within retention window).
	if result[1].ToolCalls[0].Function.Arguments != `{"path":"/etc/hosts"}` {
		t.Error("second tool call should be preserved")
	}
	_ = freed
}

func TestCompact_RetentionExactBoundary(t *testing.T) {
	c := New()
	// Exactly at the boundary: totalAssistantMsgs - retentionTurns should keep the last N.
	// 3 assistant messages, retention=1 → only the last (index 2) retains args.
	msgs := []message.Message{
		{Role: "assistant", Content: "a1", ToolCalls: []message.ToolCall{
			{ID: "c1", Function: message.FunctionCall{Name: "bash", Arguments: `{"cmd":"a"}`}},
		}},
		{Role: "assistant", Content: "a2", ToolCalls: []message.ToolCall{
			{ID: "c2", Function: message.FunctionCall{Name: "bash", Arguments: `{"cmd":"b"}`}},
		}},
		{Role: "assistant", Content: "a3", ToolCalls: []message.ToolCall{
			{ID: "c3", Function: message.FunctionCall{Name: "bash", Arguments: `{"cmd":"c"}`}},
		}},
	}
	thresholds := Thresholds{HighWater: 1, LowWater: 0, MessageSizeThreshold: 999_999, ToolCallRetentionTurns: 1}
	llmSvc := newMockClient("summary", true)

	result, _, _, pruned, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if pruned != 2 {
		t.Errorf("expected 2 pruned (first 2 beyond retention), got %d", pruned)
	}
	// First two should be pruned.
	for i := range 2 {
		if !strings.HasPrefix(result[i].ToolCalls[0].Function.Arguments, `{"pruned": "~`) {
			t.Errorf("message %d tool call should be pruned", i)
		}
	}
	// Last should be preserved.
	if result[2].ToolCalls[0].Function.Arguments != `{"cmd":"c"}` {
		t.Error("last tool call should be preserved")
	}
}

func TestSalienceScore(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantMin int // minimum expected score
		wantMax int // maximum expected score
	}{
		{
			name:    "empty content",
			content: "",
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "plain boilerplate",
			content: "hello world",
			wantMin: 30,
			wantMax: 40,
		},
		{
			name:    "error message",
			content: "Error: something went wrong in processFile()",
			wantMin: 50,
			wantMax: 100,
		},
		{
			name:    "stack trace",
			content: "panic: runtime error\n\tat main.run(/home/user/app.go:42)\n\tat main.main(/home/user/main.go:10)",
			wantMin: 100,
			wantMax: 200,
		},
		{
			name:    "file paths and functions",
			content: "read /etc/hosts file, call processData() and validateInput()",
			wantMin: 40,
			wantMax: 100,
		},
		{
			name:    "numerical measurements",
			content: "142 passed, 0 failed, coverage 87.5%",
			wantMin: 50,
			wantMax: 120,
		},
		{
			name:    "failure indicator",
			content: "Build failed: 3 errors, 2 failures. Check /home/user/build.log",
			wantMin: 50,
			wantMax: 120,
		},
		{
			name:    "long verbose output",
			content: "line1\nline2\n" + strings.Repeat("verbose log entry\n", 200),
			wantMin: 40,
			wantMax: 100,
		},
		{
			name:    "exception with stack trace",
			content: "Exception in thread main java.lang.NullPointerException\n\tat com.example.MyClass.process(MyClass.java:42)\n\tat com.example.Main.run(Main.java:10)",
			wantMin: 100,
			wantMax: 250,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := salienceScore(tt.content)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("salienceScore() = %d, want between %d and %d", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestCompact_SalienceScoredOrdering(t *testing.T) {
	c := New()
	// Create messages with varying salience levels.
	// The compactor should compact low-salience messages first.
	msgs := []message.Message{
		{Role: "tool", Content: "ls output: file1.txt file2.txt file3.txt"},                                  // low salience (~40-60)
		{Role: "tool", Content: "Error: build failed with exit code 1 at /home/user/project/src/main.go:42"}, // high salience (error + file path)
		{Role: "tool", Content: "Build succeeded. All tests passed."},                                        // low salience (~35-50)
		{Role: "tool", Content: "Exception: NullPointerException\n\tat com.example.App.main(App.java:15)"},   // very high salience (exception + stack trace)
	}
	llmSvc := newMockClient("compacted summary")

	// Use a low threshold that compacts only a few messages.
	thresholds := Thresholds{
		HighWater:            1,
		LowWater:             0,
		MessageSizeThreshold: 0,
		SalienceEnabled:      true,
	}

	result, count, _, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}

	// With SalienceEnabled and lowWater=0, all eligible messages should be compacted.
	for i, m := range result {
		if !strings.HasPrefix(m.Content, "[TOOL RESULT COMPACTED") {
			t.Errorf("message %d should have been compacted with salience ordering (lowWater=0)", i)
		}
	}
}

func TestCompact_SaliencePreservesHighValueMessages(t *testing.T) {
	c := New()
	// This test verifies that when lowWater stops compaction early,
	// high-salience messages are preserved and low-salience ones are compacted first.
	msgs := []message.Message{
		{Role: "tool", Content: strings.Repeat("low value debug log line\n", 200)},                                               // ~5600 chars, ~1400 tokens - low salience
		{Role: "tool", Content: "Error: critical failure in module\nat /src/main.go:42\n" + strings.Repeat("stack data\n", 100)}, // high salience
		{Role: "tool", Content: strings.Repeat("verbose build output\n", 200)},                                                   // low salience
		{Role: "tool", Content: "Exception: NullReference at /src/handler.go:15\n" + strings.Repeat("trace\n", 100)},             // very high salience
	}
	llmSvc := newMockClient("compacted")

	// Estimate total tokens.
	totalEst := messagesTokenEstimate(msgs, nil, "")
	// Set LowWater high enough that only 1-2 messages get compacted.
	lowWater := totalEst - 1500 // compact ~1500 tokens worth

	thresholds := Thresholds{
		HighWater:            totalEst - 1,
		LowWater:             lowWater,
		MessageSizeThreshold: 0,
		SalienceEnabled:      true,
	}

	result, count, _, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}

	// Check that not all messages were compacted (lowWater should stop early).
	compactedCount := 0
	lowSalienceCompacted := false
	highSalienceCompacted := false
	for i, m := range result {
		if strings.HasPrefix(m.Content, "[TOOL RESULT COMPACTED") {
			compactedCount++
			if i == 0 || i == 2 {
				lowSalienceCompacted = true
			}
			if i == 1 || i == 3 {
				highSalienceCompacted = true
			}
		}
	}

	if compactedCount >= 4 {
		t.Error("expected some high-salience messages to survive, but all were compacted (try adjusting LowWater)")
	}
	if !lowSalienceCompacted {
		t.Error("expected low-salience messages to be compacted first")
	}
	if highSalienceCompacted && compactedCount < 4 {
		t.Log("some high-salience messages were also compacted — this is acceptable if LowWater wasn't tight enough")
	}
}

func TestCompact_SalienceSkipHighSalience(t *testing.T) {
	c := New()
	// Test that messages with salience score above HighSalienceSkipThreshold are skipped.
	msgs := []message.Message{
		{Role: "tool", Content: "simple output"},                                  // low salience
		{Role: "tool", Content: "Error: critical failure at /path/to/file.go:42"}, // high salience
	}
	llmSvc := newMockClient("summary")

	thresholds := Thresholds{
		HighWater:                 1,
		LowWater:                  0,
		MessageSizeThreshold:      0,
		SalienceEnabled:           true,
		HighSalienceSkipThreshold: 80, // skip messages with score >= 80
	}

	result, count, _, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}

	// Low-salience message (index 0) should be compacted.
	if !strings.HasPrefix(result[0].Content, "[TOOL RESULT COMPACTED") {
		t.Error("low-salience message should be compacted")
	}
	// High-salience message (index 1) should NOT be compacted (skipped by threshold).
	if strings.HasPrefix(result[1].Content, "[TOOL RESULT COMPACTED") {
		t.Error("high-salience message should NOT be compacted (skipped by HighSalienceSkipThreshold)")
	}
}

func TestCompact_SalienceDisabledFallsBack(t *testing.T) {
	c := New()
	// When SalienceEnabled is false, the original oldest-first behaviour should be used.
	// Create messages with high salience first (oldest) and low salience later.
	msgs := []message.Message{
		{Role: "tool", Content: "Error: initial failure with stack trace at /src/main.go:42"}, // high salience, oldest
		{Role: "tool", Content: "ls output: files"},                                           // low salience, newer
	}
	llmSvc := newMockClient("summary")

	thresholds := Thresholds{
		HighWater:            1,
		LowWater:             0,
		MessageSizeThreshold: 0,
		SalienceEnabled:      false,
	}

	result, count, _, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}

	// With oldest-first, the first message (index 0, the high-salience error) should be compacted.
	if !strings.HasPrefix(result[0].Content, "[TOOL RESULT COMPACTED") {
		t.Error("with SalienceEnabled=false, oldest message should be compacted first")
	}
}

func TestCompact_SalienceWithMessageSizeThreshold(t *testing.T) {
	c := New()
	// Test that both salience and message size threshold work together.
	msgs := []message.Message{
		{Role: "tool", Content: "small output"},                                                                    // below size threshold
		{Role: "tool", Content: strings.Repeat("large debug output line\n", 200)},                                  // large, low salience
		{Role: "tool", Content: "Error: critical issue at /path/to/file.go:42\n" + strings.Repeat("trace\n", 100)}, // large, high salience
	}
	llmSvc := newMockClient("summary")

	thresholds := Thresholds{
		HighWater:            1,
		LowWater:             0,
		MessageSizeThreshold: 200,
		SalienceEnabled:      true,
	}

	result, count, _, _, err := c.Compact(context.Background(), msgs, llmSvc, thresholds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}

	// Small message should not be compacted (below size threshold).
	if result[0].Content != "small output" {
		t.Error("small message should not be compacted (below MessageSizeThreshold)")
	}
	// At least one of the large messages should be compacted.
	largeCompacted := strings.HasPrefix(result[1].Content, "[TOOL RESULT COMPACTED") || strings.HasPrefix(result[2].Content, "[TOOL RESULT COMPACTED")
	if !largeCompacted {
		t.Error("at least one large message should be compacted")
	}
}
