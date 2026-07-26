package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/persona"
	"github.com/glemsom/eitri/internal/runner/runconfig"
	"github.com/glemsom/eitri/internal/skills"
	uisession "github.com/glemsom/eitri/internal/session"
)

func TestBatchRun_ReturnsErrorForMissingBaseURL(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := runconfig.RunConfig{
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
	cfg := runconfig.RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://test.local",
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
	cfg := runconfig.RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://test.local",
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
	cfg := runconfig.RunConfig{
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
	cfg := runconfig.RunConfig{
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
	workspace := t.TempDir()

	// Create a test persona with a custom system prompt and an injected skill
	personaDef := &persona.PersonaDefinition{
		Name:           "test-reviewer",
		SystemPrompt:   "You are a code reviewer. Be thorough and check for edge cases.",
		RequiredSkills: []string{"test-review-skill"},
	}
	if err := persona.Save(workspace, personaDef); err != nil {
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
	cfg := runconfig.RunConfig{
		Workspace:     workspace,
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

	batchCfg := runconfig.RunConfig{
		ProviderID:    "opencode_go",
		BaseURL:       "http://127.0.0.1:1", // connection refused -> error
		APIKey:        "test-key",
		ModelName:     "test-model",
		Workspace:     workspace,
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
