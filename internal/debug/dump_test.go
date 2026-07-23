package debug

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/session"
)

func TestCrashDirName(t *testing.T) {
	ts := time.Date(2025, 7, 23, 14, 30, 0, 0, time.UTC)
	name := crashDirName(ts)
	if !strings.HasPrefix(name, "crash-2025-07-23T14-30-00") {
		t.Fatalf("unexpected dir name: %q", name)
	}
	// Verify no colons (cross-platform safe)
	if strings.Contains(name, ":") {
		t.Fatalf("dir name contains colon: %q", name)
	}
}

func TestWriteCrashDump_Basic(t *testing.T) {
	// Use a temp home directory to avoid polluting real ~/.eitri
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	opts := DumpOptions{
		Error:   "test error: something went wrong",
		Stack:   "goroutine 1 [running]:\nmain.test()\n\t/path/to/file.go:42",
		Version: "0.1.0",
		ConfigSummary: map[string]interface{}{
			"provider":  "opencode_go",
			"model":     "qwen2.5-coder:32b",
			"has_api_key": false,
		},
		RuntimeSummary: &RuntimeSummary{
			UpSince:            time.Now().Add(-1 * time.Hour),
			ActiveRunCount:     0,
			SessionCount:       2,
			RecordedHTTPTraces: 3,
		},
		Sessions: []*session.UISession{
			{
				ID:     "sess-1",
				Title:  "Test Session",
				Status: session.StatusIdle,
				Messages: []session.Message{
					{Role: "user", Content: "hello", CreatedAt: time.Now()},
					{Role: "assistant", Content: "hi there", CreatedAt: time.Now()},
				},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
		},
		Traces: []*HTTPTrace{
			{
				ID:         "trace_1",
				Timestamp:  time.Now(),
				Method:     "POST",
				URL:        "/v1/chat",
				Status:     200,
				DurationMs: 1500,
			},
		},
		InFlightTraces: []*HTTPTrace{
			{
				ID:         "trace_2",
				Timestamp:  time.Now(),
				Method:     "POST",
				URL:        "/v1/chat",
				Status:     0,
				DurationMs: 500,
			},
		},
		Logs: []LogEntry{
			{
				Timestamp: time.Now(),
				Level:     "INFO",
				Message:   "server started",
				Attrs:     map[string]any{"port": 8080},
			},
			{
				Timestamp: time.Now(),
				Level:     "WARN",
				Message:   "rate limit approaching",
				Attrs:     map[string]any{"retry_after": 30},
			},
		},
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	// Verify directory exists
	if _, err := os.Stat(crashDir); os.IsNotExist(err) {
		t.Fatalf("crash dir %s does not exist", crashDir)
	}

	// Verify all 5 expected files exist
	expectedFiles := []string{"crash.json", "sessions.json", "traces.json", "goroutines.txt", "logs.json"}
	for _, f := range expectedFiles {
		path := filepath.Join(crashDir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Fatalf("expected file %s does not exist", path)
		}
	}

	// Verify crash.json contains the error
	data, err := os.ReadFile(filepath.Join(crashDir, "crash.json"))
	if err != nil {
		t.Fatalf("read crash.json: %v", err)
	}
	if !strings.Contains(string(data), "test error: something went wrong") {
		t.Fatalf("crash.json missing error message")
	}
	if !strings.Contains(string(data), "0.1.0") {
		t.Fatalf("crash.json missing version")
	}
}

func TestWriteCrashDump_NoSessions(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	opts := DumpOptions{
		Error:   "batch error",
		Version: "dev",
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	// sessions.json should not exist (Sessions was nil)
	sessionsPath := filepath.Join(crashDir, "sessions.json")
	if _, err := os.Stat(sessionsPath); !os.IsNotExist(err) {
		t.Fatalf("sessions.json should not exist when Sessions is nil")
	}

	// traces.json should exist (always written)
	tracesPath := filepath.Join(crashDir, "traces.json")
	if _, err := os.Stat(tracesPath); os.IsNotExist(err) {
		t.Fatalf("traces.json should exist")
	}

	// goroutines.txt should exist (always written)
	goroPath := filepath.Join(crashDir, "goroutines.txt")
	if _, err := os.Stat(goroPath); os.IsNotExist(err) {
		t.Fatalf("goroutines.txt should exist")
	}

	// Verify goroutines.txt has content
	data, err := os.ReadFile(goroPath)
	if err != nil {
		t.Fatalf("read goroutines.txt: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("goroutines.txt is empty")
	}

	// logs.json should not exist (Logs was nil)
	logsPath := filepath.Join(crashDir, "logs.json")
	if _, err := os.Stat(logsPath); !os.IsNotExist(err) {
		t.Fatalf("logs.json should not exist when Logs is nil")
	}
}

func TestWriteCrashDump_LogsContent(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	opts := DumpOptions{
		Error:   "log test",
		Version: "dev",
		Logs: []LogEntry{
			{Timestamp: time.Now(), Level: "INFO", Message: "test log", Attrs: map[string]any{"key": "val"}},
		},
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	logsPath := filepath.Join(crashDir, "logs.json")
	if _, err := os.Stat(logsPath); os.IsNotExist(err) {
		t.Fatalf("logs.json should exist when Logs is non-empty")
	}

	data, err := os.ReadFile(logsPath)
	if err != nil {
		t.Fatalf("read logs.json: %v", err)
	}
	if !strings.Contains(string(data), "test log") {
		t.Fatalf("logs.json missing log message")
	}
	if !strings.Contains(string(data), "INFO") {
		t.Fatalf("logs.json missing level")
	}
	if !strings.Contains(string(data), "val") {
		t.Fatalf("logs.json missing attr value")
	}
}

func TestWriteCrashDump_Permissions(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	opts := DumpOptions{
		Error:   "perm test",
		Version: "dev",
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	// Verify crash dir is readable
	info, err := os.Stat(crashDir)
	if err != nil {
		t.Fatalf("stat crash dir: %v", err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("crash dir permissions %o, want 0700", info.Mode().Perm())
	}

	// Verify crash.json is not world-readable
	crashJSON := filepath.Join(crashDir, "crash.json")
	info, err = os.Stat(crashJSON)
	if err != nil {
		t.Fatalf("stat crash.json: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("crash.json permissions %o, want 0600", info.Mode().Perm())
	}
}

func TestWriteCrashDump_DirNameTimestamp(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	opts := DumpOptions{
		Error:   "timestamp test",
		Version: "dev",
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	// Dir name should match pattern crash-YYYY-MM-DDTHH-MM-SS (no colons)
	base := filepath.Base(crashDir)
	if !strings.HasPrefix(base, "crash-") {
		t.Fatalf("dir name should start with crash-, got %q", base)
	}
	if strings.Contains(base, ":") {
		t.Fatalf("dir name contains colon: %q", base)
	}
}

func TestSanitizeConfig(t *testing.T) {
	cfg := &config.Config{
		Provider:            "opencode_go",
		APIKey:              "sk-abc123secret",
		ProviderAuth:        nil,
		BaseURL:             "https://api.example.com",
		Model:               "gpt-4",
		MaxTurns:            75,
		ContextWindowTokens: 128000,
	}

	sanitized := SanitizeConfig(cfg)

	if sanitized["provider"] != "opencode_go" {
		t.Fatalf("provider = %q, want %q", sanitized["provider"], "opencode_go")
	}
	if sanitized["api_key"] != "***" {
		t.Fatalf("api_key = %q, want '***'", sanitized["api_key"])
	}
	if sanitized["model"] != "gpt-4" {
		t.Fatalf("model = %q, want %q", sanitized["model"], "gpt-4")
	}
	if sanitized["has_api_key"] != nil {
		// has_api_key is not in sanitized output; that's fine
	}
}

func TestSanitizeConfig_Nil(t *testing.T) {
	sanitized := SanitizeConfig(nil)
	if sanitized != nil {
		t.Fatalf("expected nil, got %v", sanitized)
	}
}

func TestWriteCrashDump_ConfigSecretsRedacted(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	opts := DumpOptions{
		Error:   "secret check",
		Version: "dev",
		ConfigSummary: map[string]interface{}{
			"api_key":  "***",
			"provider": "test",
		},
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(crashDir, "crash.json"))
	if err != nil {
		t.Fatalf("read crash.json: %v", err)
	}

	if strings.Contains(string(data), "sk-") {
		t.Fatalf("crash.json contains unredacted API key pattern")
	}
}

func TestWriteCrashDump_TracesJSONStructure(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	opts := DumpOptions{
		Error:          "trace test",
		Version:        "dev",
		Traces:         []*HTTPTrace{},
		InFlightTraces: []*HTTPTrace{},
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(crashDir, "traces.json"))
	if err != nil {
		t.Fatalf("read traces.json: %v", err)
	}

	// Verify it's valid JSON with expected keys
	if !strings.Contains(string(data), `"traces":`) {
		t.Fatalf("traces.json missing 'traces' key")
	}
	if !strings.Contains(string(data), `"in_flight":`) {
		t.Fatalf("traces.json missing 'in_flight' key")
	}
}

func TestWriteCrashDump_ErrorChain(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Simulate a wrapped error chain (like fmt.Errorf with %w produces).
	baseErr := fmt.Errorf("network error: connection refused")
	wrappedErr := fmt.Errorf("provider call failed: %w", baseErr)
	finalErr := fmt.Errorf("batch run error: %w", wrappedErr)

	opts := DumpOptions{
		Error:       finalErr.Error(),
		ErrorChain:  fmt.Sprintf("%+v", finalErr),
		Stack:       "goroutine 1 [running]:\nmain.doStuff()\n\t/path/to/main.go:99",
		Version:     "0.2.0",
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(crashDir, "crash.json"))
	if err != nil {
		t.Fatalf("read crash.json: %v", err)
	}

	// Verify backward-compatible "error" field exists
	if !strings.Contains(string(data), `"error":`) {
		t.Fatalf("crash.json missing 'error' field")
	}
	if !strings.Contains(string(data), `"batch run error: provider call failed: network error: connection refused"`) {
		t.Fatalf("crash.json missing full error chain in 'error' field")
	}

	// Verify "error_chain" field exists
	if !strings.Contains(string(data), `"error_chain":`) {
		t.Fatalf("crash.json missing 'error_chain' field")
	}

	// Verify the error chain contains all levels of wrapping
	chainContent := string(data)
	if !strings.Contains(chainContent, "network error: connection refused") {
		t.Fatalf("error_chain missing innermost error")
	}
	if !strings.Contains(chainContent, "provider call failed") {
		t.Fatalf("error_chain missing middle wrapping error")
	}
	if !strings.Contains(chainContent, "batch run error") {
		t.Fatalf("error_chain missing outermost error")
	}

	// Verify "stack" field is preserved
	if !strings.Contains(string(data), `/path/to/main.go:99`) {
		t.Fatalf("crash.json missing stack frame info")
	}
}

func TestWriteCrashDump_ErrorChainEmpty(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// When ErrorChain is empty, the field should be omitted from JSON.
	opts := DumpOptions{
		Error:   "simple error",
		Version: "dev",
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(crashDir, "crash.json"))
	if err != nil {
		t.Fatalf("read crash.json: %v", err)
	}

	// "error" field must be present
	if !strings.Contains(string(data), `"error":`) {
		t.Fatalf("crash.json missing 'error' field")
	}

	// "error_chain" should not appear when empty (omitempty)
	if strings.Contains(string(data), `"error_chain":`) {
		t.Fatalf("crash.json should not have 'error_chain' field when empty")
	}
}

func TestWriteCrashDump_ConversationContext(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	opts := DumpOptions{
		Error:   "batch error with context",
		Version: "dev",
		RuntimeSummary: &RuntimeSummary{
			UpSince:       time.Now().Add(-30 * time.Minute),
			ActiveRunCount: 1,
		},
		ConversationContext: &ConversationContext{
			LastUserMessage:      "implement feature X",
			LastAssistantMessage: "I'll start by reading the codebase...",
			TurnNumber:           3,
			TotalTokensUsed:      1500,
		},
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(crashDir, "crash.json"))
	if err != nil {
		t.Fatalf("read crash.json: %v", err)
	}

	content := string(data)

	// Verify conversation_context is present
	if !strings.Contains(content, `"conversation_context":`) {
		t.Fatalf("crash.json missing 'conversation_context' key")
	}

	// Verify fields within conversation_context
	if !strings.Contains(content, `"last_user_message":`) {
		t.Fatalf("crash.json missing 'last_user_message'")
	}
	if !strings.Contains(content, `"implement feature X"`) {
		t.Fatalf("crash.json missing last_user_message content")
	}
	if !strings.Contains(content, `"last_assistant_message":`) {
		t.Fatalf("crash.json missing 'last_assistant_message'")
	}
	if !strings.Contains(content, `"I'll start by reading the codebase..."`) {
		t.Fatalf("crash.json missing last_assistant_message content")
	}
	if !strings.Contains(content, `"turn_number": 3`) {
		t.Fatalf("crash.json missing or incorrect turn_number")
	}
	if !strings.Contains(content, `"total_tokens_used": 1500`) {
		t.Fatalf("crash.json missing or incorrect total_tokens_used")
	}

	// Verify runtime_summary shows active run count
	if !strings.Contains(content, `"active_run_count": 1`) {
		t.Fatalf("crash.json missing active_run_count: 1")
	}
}

func TestWriteCrashDump_ConversationContextNil(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	opts := DumpOptions{
		Error:               "error without context",
		Version:             "dev",
		ConversationContext: nil,
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(crashDir, "crash.json"))
	if err != nil {
		t.Fatalf("read crash.json: %v", err)
	}

	// conversation_context should be omitted when nil
	if strings.Contains(string(data), `"conversation_context":`) {
		t.Fatalf("crash.json should not have 'conversation_context' when nil")
	}
}

func TestWriteCrashDump_FailingHTTPTrace(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	failingTrace := &HTTPTrace{
		ID:              "trace_fail_1",
		Timestamp:       time.Now(),
		SessionID:       "batch-1",
		Method:          "POST",
		URL:             "/v1/chat",
		Status:          400,
		DurationMs:      500,
		RequestBytes:    50,
		RequestBody:     `{"prompt":"test"}`,
		ResponseBytes:   30,
		ResponseBody:    `{"error":"bad request"}`,
		ResponseHeaders: map[string][]string{"X-Request-Id": {"req-abc"}},
	}

	opts := DumpOptions{
		Error:            "provider returned 400",
		Version:          "dev",
		FailingHTTPTrace: failingTrace,
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(crashDir, "crash.json"))
	if err != nil {
		t.Fatalf("read crash.json: %v", err)
	}

	content := string(data)

	// Verify failing_http_trace is present
	if !strings.Contains(content, `"failing_http_trace":`) {
		t.Fatalf("crash.json missing 'failing_http_trace' key")
	}

	// Verify fields within failing_http_trace
	if !strings.Contains(content, `"trace_fail_1"`) {
		t.Fatalf("crash.json missing trace ID")
	}
	if !strings.Contains(content, `"status": 400`) {
		t.Fatalf("crash.json missing status 400")
	}
	if !strings.Contains(content, `"response_headers":`) {
		t.Fatalf("crash.json missing response_headers")
	}
	if !strings.Contains(content, `"req-abc"`) {
		t.Fatalf("crash.json missing X-Request-Id value")
	}
}

func TestWriteCrashDump_FailingHTTPTraceNil(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	opts := DumpOptions{
		Error:            "error without failing trace",
		Version:          "dev",
		FailingHTTPTrace: nil,
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(crashDir, "crash.json"))
	if err != nil {
		t.Fatalf("read crash.json: %v", err)
	}

	// failing_http_trace should be omitted when nil
	if strings.Contains(string(data), `"failing_http_trace":`) {
		t.Fatalf("crash.json should not have 'failing_http_trace' when nil")
	}
}

func TestCollectSystemDiagnostics_Basic(t *testing.T) {
	startTime := time.Now()
	diag := CollectSystemDiagnostics(startTime)

	if diag == nil {
		t.Fatal("CollectSystemDiagnostics returned nil")
	}

	if diag.GoVersion == "" {
		t.Error("go_version should not be empty")
	}
	if diag.OS == "" {
		t.Error("os should not be empty")
	}
	if diag.Arch == "" {
		t.Error("arch should not be empty")
	}
	if diag.NumCPU <= 0 {
		t.Errorf("num_cpu should be > 0, got %d", diag.NumCPU)
	}
	if diag.MemoryMB == nil {
		t.Error("memory_mb should not be nil")
	} else {
		if _, ok := diag.MemoryMB["total"]; !ok {
			t.Error("memory_mb should have 'total' key")
		}
		if _, ok := diag.MemoryMB["used"]; !ok {
			t.Error("memory_mb should have 'used' key")
		}
	}
	if diag.ProcessUptimeSeconds < 0 {
		t.Errorf("process_uptime_seconds should be >= 0, got %d", diag.ProcessUptimeSeconds)
	}
	if diag.ProcessStartedAt.IsZero() {
		t.Error("process_started_at should not be zero")
	}
	if len(diag.EnvKeys) == 0 {
		t.Error("env_keys should not be empty (at least PATH and similar)")
	}
	// Verify no env values leaked
	for _, key := range diag.EnvKeys {
		if key == "" {
			t.Error("env_keys should not contain empty strings")
		}
		if strings.Contains(key, "=") {
			t.Errorf("env_keys should not contain '=' in key names, got %q", key)
		}
	}
}

func TestWriteCrashDump_WithSystemDiagnostics(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	startTime := time.Now()
	diag := CollectSystemDiagnostics(startTime)

	opts := DumpOptions{
		Error:             "test with system diagnostics",
		Version:           "dev",
		SystemDiagnostics: diag,
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(crashDir, "crash.json"))
	if err != nil {
		t.Fatalf("read crash.json: %v", err)
	}

	// Verify system key exists with content
	if !strings.Contains(string(data), `"system":`) {
		t.Fatal("crash.json should contain 'system' key")
	}
	if !strings.Contains(string(data), diag.GoVersion) {
		t.Fatal("crash.json should contain go_version")
	}
	if !strings.Contains(string(data), diag.OS) {
		t.Fatal("crash.json should contain os")
	}
	if !strings.Contains(string(data), diag.Arch) {
		t.Fatal("crash.json should contain arch")
	}
}

func TestWriteCrashDump_SystemDiagnosticsNil(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	opts := DumpOptions{
		Error:             "no system diagnostics",
		Version:           "dev",
		SystemDiagnostics: nil,
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(crashDir, "crash.json"))
	if err != nil {
		t.Fatalf("read crash.json: %v", err)
	}

	// system key should be omitted when nil
	if strings.Contains(string(data), `"system":`) {
		t.Fatal("crash.json should not have 'system' key when SystemDiagnostics is nil")
	}
}
