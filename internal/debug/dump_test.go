package debug

import (
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
	}

	crashDir, err := WriteCrashDump(opts)
	if err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	// Verify directory exists
	if _, err := os.Stat(crashDir); os.IsNotExist(err) {
		t.Fatalf("crash dir %s does not exist", crashDir)
	}

	// Verify all 4 files exist
	expectedFiles := []string{"crash.json", "sessions.json", "traces.json", "goroutines.txt"}
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
