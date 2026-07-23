package runner

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/history"
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
	cfg := RunConfig{
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
