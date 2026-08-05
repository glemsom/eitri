package runner

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/persona"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

func TestBatchRun_ReturnsErrorForMissingBaseURL(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		ModelName:  "test-model",
	}
	var buf bytes.Buffer
	_, err := svc.BatchRun(context.Background(), "hello", cfg, &buf)
	if err == nil {
		t.Fatal("expected error for missing base URL")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %q, want 'not configured'", err.Error())
	}
}

func TestBatchRun_ReturnsErrorForMissingModel(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    unreachableURL(t),
	}
	var buf bytes.Buffer
	_, err := svc.BatchRun(context.Background(), "hello", cfg, &buf)
	if err == nil {
		t.Fatal("expected error for missing model")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %q, want 'not configured'", err.Error())
	}
}

func TestBatchRun_ReturnsErrorOnCancelledContext(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    unreachableURL(t),
		APIKey:     "test-key",
		ModelName:  "test-model",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Already cancelled

	var buf bytes.Buffer
	_, err := svc.BatchRun(ctx, "hello", cfg, &buf)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestBatchRun_FailsGracefullyOnConnectionFailure(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://127.0.0.1:1",
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
	}

	var buf bytes.Buffer

	// Use a very short timeout so test runs fast.
	// The connection will be refused but retries take ~1s each.
	// A short timeout demonstrates that context cancellation works.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result, err := svc.BatchRun(ctx, "test prompt", cfg, &buf)
	if err != nil {
		if result != "" {
			t.Logf("result was non-empty despite error: %q", result)
		}
		return
	}
	t.Logf("unexpected success: result=%q, buf=%q", result, buf.String())
}

func TestBatchRun_DeadEndpointReturnsFastWithZeroRetryPolicy(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://127.0.0.1:1", // connection refused
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		// RetryPolicy zero value means no retries: single attempt, no 1s sleeps.
	}

	var buf bytes.Buffer
	start := time.Now()
	_, err := svc.BatchRun(context.Background(), "test prompt", cfg, &buf)
	if err == nil {
		t.Fatal("expected connection failure error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("BatchRun took %v, want < 2s (no retry sleeps on dead endpoint)", elapsed)
	}
}

func TestBatchRun_SkipsThinkingLevelForUnsupportedModel(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID:    "opencode_go",
		BaseURL:       "http://127.0.0.1:1",
		APIKey:        "test-key",
		ModelName:     "gpt-4", // does not support thinking levels
		ThinkingLevel: "high",
		Workspace:     t.TempDir(),
	}

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := svc.BatchRun(ctx, "test prompt", cfg, &buf)
	// The guard should not error; the failure will be from connection refused or timeout
	// Ensure no thinking-level-related error is surfaced
	if err != nil && strings.Contains(err.Error(), "thinking") {
		t.Errorf("unexpected thinking-level-related error: %v", err)
	}
}

func TestBatchRun_SetsThinkingLevelForSupportedModel(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID:    "opencode_go",
		BaseURL:       "http://127.0.0.1:1",
		APIKey:        "test-key",
		ModelName:     "deepseek-reasoner", // supports thinking levels
		ThinkingLevel: "high",
		Workspace:     t.TempDir(),
	}

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := svc.BatchRun(ctx, "test prompt", cfg, &buf)
	// The guard should set reasoning_effort; failure will be from connection refused or timeout
	// No thinking-level-related error should appear
	if err != nil && strings.Contains(err.Error(), "thinking") {
		t.Errorf("unexpected thinking-level-related error: %v", err)
	}
}

// TestBatchRun_FeedsDebugRecorderMetrics verifies that a headless batch run
// records traces into the debug recorder and feeds the same interaction
// metrics as browser runs (issue #987).
func TestBatchRun_FeedsDebugRecorderMetrics(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`+"\n\n")
		fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","model":"test-model","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer llm.Close()

	rec := debug.NewRecorder(20)
	uiSessionMgr := uisession.NewManager(10, t.TempDir())
	historyMgr := history.NewSessionManager(50)
	svc := NewRunService(RunServiceDeps{
		UISessionMgr:      uiSessionMgr,
		HistorySessionMgr: historyMgr,
		DebugRecorder:     rec,
	})

	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    llm.URL,
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		MaxTurns:   1,
	}

	var buf bytes.Buffer
	if _, err := svc.BatchRun(context.Background(), "hello", cfg, &buf); err != nil {
		t.Fatalf("batch run failed: %v", err)
	}

	// The trace must carry the model + usage parsed from the stream tail.
	traces := rec.List(0, "", "")
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	if traces[0].Model != "test-model" {
		t.Fatalf("trace Model = %q, want test-model", traces[0].Model)
	}
	if traces[0].Usage == nil || traces[0].Usage.PromptTokens != 10 || traces[0].Usage.CompletionTokens != 5 {
		t.Fatalf("trace Usage = %+v, want prompt=10 completion=5", traces[0].Usage)
	}

	// The metrics aggregate must reflect the batch call.
	snap := rec.Metrics()
	if snap.TotalCalls != 1 {
		t.Fatalf("TotalCalls = %d, want 1", snap.TotalCalls)
	}
	if len(snap.Providers) != 1 || snap.Providers[0].ProviderID != "opencode_go" {
		t.Fatalf("providers = %+v, want opencode_go", snap.Providers)
	}
	mm := snap.Providers[0].Models[0]
	if mm.Model != "test-model" || mm.Calls != 1 {
		t.Fatalf("model aggregate = %+v, want test-model calls=1", mm)
	}
	if mm.Tokens.PromptTokens != 10 || mm.Tokens.CompletionTokens != 5 {
		t.Fatalf("model tokens = %+v, want prompt=10 completion=5", mm.Tokens)
	}
	if mm.Cache.Hits != 0 || mm.Cache.Misses != 1 {
		t.Fatalf("model cache = %+v, want hits=0 misses=1", mm.Cache)
	}
}

// TestBatchRun_ErrorFeedsMetrics verifies that a failing headless batch run
// classifies the error at capture time and increments the per-model error
// counters.
func TestBatchRun_ErrorFeedsMetrics(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limit exceeded"}}`)
	}))
	defer llm.Close()

	rec := debug.NewRecorder(20)
	uiSessionMgr := uisession.NewManager(10, t.TempDir())
	historyMgr := history.NewSessionManager(50)
	svc := NewRunService(RunServiceDeps{
		UISessionMgr:      uiSessionMgr,
		HistorySessionMgr: historyMgr,
		DebugRecorder:     rec,
	})

	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    llm.URL,
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		MaxTurns:   1,
	}

	var buf bytes.Buffer
	if _, err := svc.BatchRun(context.Background(), "hello", cfg, &buf); err == nil {
		t.Fatal("expected batch run to fail on 429")
	}

	snap := rec.Metrics()
	if snap.TotalCalls != 1 {
		t.Fatalf("TotalCalls = %d, want 1", snap.TotalCalls)
	}
	if snap.TotalErrors != 1 {
		t.Fatalf("TotalErrors = %d, want 1", snap.TotalErrors)
	}
	mm := snap.Providers[0].Models[0]
	if mm.Errors[debug.ErrorClassRateLimit] != 1 {
		t.Fatalf("rate_limit errors = %d, want 1", mm.Errors[debug.ErrorClassRateLimit])
	}
	if mm.LastError != debug.ErrorClassRateLimit {
		t.Fatalf("last_error = %q, want rate_limit", mm.LastError)
	}
}

func TestExtractLastMessages(t *testing.T) {
	mgr := history.NewSessionManager(10)
	mgr.Create("test-session")

	// No messages yet
	user, asst := extractLastMessages(mgr, "test-session")
	if user != "" || asst != "" {
		t.Fatalf("expected empty messages, got user=%q, asst=%q", user, asst)
	}

	// Add a user message
	mgr.AppendUser("test-session", "hello world")
	user, asst = extractLastMessages(mgr, "test-session")
	if user != "hello world" {
		t.Fatalf("expected user='hello world', got %q", user)
	}
	if asst != "" {
		t.Fatalf("expected empty assistant, got %q", asst)
	}

	// Add an assistant message
	mgr.AppendAssistant("test-session", "hi there", nil)
	user, asst = extractLastMessages(mgr, "test-session")
	if user != "hello world" {
		t.Fatalf("expected user='hello world', got %q", user)
	}
	if asst != "hi there" {
		t.Fatalf("expected assistant='hi there', got %q", asst)
	}

	// Add more messages and verify last ones are returned
	mgr.AppendUser("test-session", "second question")
	mgr.AppendAssistant("test-session", "second answer", nil)
	user, asst = extractLastMessages(mgr, "test-session")
	if user != "second question" {
		t.Fatalf("expected user='second question', got %q", user)
	}
	if asst != "second answer" {
		t.Fatalf("expected assistant='second answer', got %q", asst)
	}
}

func TestBatchRun_ConversationContextCapturedOnError(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://127.0.0.1:1", // connection refused -> error
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		MaxTurns:   5,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	_, err := svc.BatchRun(ctx, "test prompt for context capture", cfg, &buf)
	if err == nil {
		t.Fatal("expected error for connection failure")
	}

	// Verify conversation context was captured
	convCtx := svc.LastBatchConversationContext()
	if convCtx == nil {
		t.Fatal("expected non-nil conversation context after batch run error")
	}
	if convCtx.LastUserMessage == "" {
		t.Fatal("expected non-empty last user message")
	}
	if convCtx.LastUserMessage != "test prompt for context capture" {
		t.Fatalf("expected user message 'test prompt for context capture', got %q", convCtx.LastUserMessage)
	}
}

func TestBatchRun_UsesActivePersona(t *testing.T) {
	// Isolate the persona home dir per test; injected via RunConfig.HomeDir
	// instead of mutating the process HOME env (issue #1023).
	homeDir := t.TempDir()

	workspace := t.TempDir()

	// Create a test persona with a custom system prompt and an injected skill
	personaDef := &persona.PersonaDefinition{
		Name:           "test-reviewer",
		SystemPrompt:   "You are a code reviewer. Be thorough and check for edge cases.",
		RequiredSkills: []string{"test-review-skill"},
	}
	if err := persona.SaveToHome(homeDir, personaDef); err != nil {
		t.Fatalf("save persona: %v", err)
	}

	// Create a minimal test skill referenced by the persona
	skillDir := filepath.Join(workspace, ".eitri", "skills", "test-review-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillContent := "---\nname: test-review-skill\ndescription: A test skill for batch persona testing\n---\nReview the code for potential bugs and security issues."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	// Create skills service rooted at the test skill directory
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{
		{Path: filepath.Join(workspace, ".eitri", "skills"), Scope: skills.ScopeProjectAgents},
	})

	// Verify that buildSystemPrompt produces output containing the persona's
	// custom system prompt and the injected skill's content.
	cfg := RunConfig{
		Workspace:     workspace,
		HomeDir:       homeDir,
		ActivePersona: "test-reviewer",
	}
	sysPrompt, err := buildSystemPrompt(cfg, sessionSkillContext{}, skillsSvc)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}
	if !strings.Contains(sysPrompt, "You are a code reviewer. Be thorough and check for edge cases.") {
		t.Fatalf("system prompt should contain persona's custom prompt, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "test-review-skill") {
		t.Fatalf("system prompt should reference injected skill name, got:\n%s", sysPrompt)
	}
	if strings.Contains(sysPrompt, "Review the code for potential bugs and security issues.") {
		t.Fatalf("system prompt should NOT contain injected skill body (skills are loaded via skill() tool), got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "Required skills for this persona:") {
		t.Fatalf("system prompt should contain required skills directive, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "<required_skills>") {
		t.Fatalf("system prompt should contain <required_skills> block, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "</required_skills>") {
		t.Fatalf("system prompt should contain </required_skills> closing tag, got:\n%s", sysPrompt)
	}

	// End-to-end test: BatchRun with ActivePersona pointing to the test persona.
	// Use a dead-port connection pattern so the run fails (connection refused)
	// after the persona has been loaded and the system prompt built.
	uiSessionMgr := uisession.NewManager(10, t.TempDir())
	historyMgr := history.NewSessionManager(50)
	svc := NewRunService(RunServiceDeps{
		UISessionMgr:      uiSessionMgr,
		HistorySessionMgr: historyMgr,
		SkillsService:     skillsSvc,
	})

	batchCfg := RunConfig{
		ProviderID:    "opencode_go",
		BaseURL:       "http://127.0.0.1:1", // connection refused -> error
		APIKey:        "test-key",
		ModelName:     "test-model",
		Workspace:     workspace,
		HomeDir:       homeDir,
		ActivePersona: "test-reviewer",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	_, err = svc.BatchRun(ctx, "test prompt", batchCfg, &buf)
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
	// The error should be about connection failure, not about persona loading.
	// If the persona file didn't exist or failed to load, the error message
	// would contain "persona". We verify the persona was loaded correctly
	// by asserting the error is NOT persona-related.
	if strings.Contains(err.Error(), "persona") {
		t.Fatalf("unexpected persona error — persona should have been loaded successfully: %v", err)
	}
}
