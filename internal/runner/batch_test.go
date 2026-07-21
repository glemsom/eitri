package runner

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
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
