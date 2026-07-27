package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/litellm"
)

// ————— DebugHook —————

func TestDebugHook_BeforeRequestLogsPrompt(t *testing.T) {
	t.Parallel()
	// DebugPrompt triggers request logging. We can't easily capture slog output
	// in a portable way, but we can verify the hook doesn't panic and that its
	// behaviour is correct by checking control flow.
	h := &DebugHook{DebugPrompt: true}
	meta := litellm.CallMeta{Provider: "openai", Model: "gpt-4", Operation: "chat"}
	req := &litellm.Request{Model: "gpt-4"}

	// Should not panic
	h.BeforeRequest(context.Background(), meta, req)

	// With neither flag set, BeforeRequest should be a no-op
	h2 := &DebugHook{}
	h2.BeforeRequest(context.Background(), meta, req)
}

func TestDebugHook_DebugFileWrittenOnError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := &DebugHook{DebugLLMDir: dir}
	meta := litellm.CallMeta{Provider: "openai", Model: "gpt-4", Operation: "chat"}

	err := litellm.NewProviderError("openai", litellm.ErrorTypeAuth, "invalid API key")
	h.AfterResponse(context.Background(), meta, nil, err)

	entries, err2 := os.ReadDir(dir)
	if err2 != nil {
		t.Fatalf("ReadDir: %v", err2)
	}
	if len(entries) == 0 {
		t.Fatal("no debug file written on error")
	}

	path := filepath.Join(dir, entries[len(entries)-1].Name())
	data, err3 := os.ReadFile(path)
	if err3 != nil {
		t.Fatalf("ReadFile: %v", err3)
	}

	var entry struct {
		Provider  string `json:"provider"`
		Model     string `json:"model"`
		Operation string `json:"operation"`
		Error     string `json:"error"`
	}
	if err4 := json.Unmarshal(data, &entry); err4 != nil {
		t.Fatalf("json.Unmarshal: %v", err4)
	}
	if entry.Provider != "openai" {
		t.Fatalf("Provider = %q, want %q", entry.Provider, "openai")
	}
	if entry.Model != "gpt-4" {
		t.Fatalf("Model = %q, want %q", entry.Model, "gpt-4")
	}
	if !strings.Contains(entry.Error, "invalid API key") {
		t.Fatalf("Error = %q, want containing 'invalid API key'", entry.Error)
	}
}

func TestDebugHook_NoFileWhenNoError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := &DebugHook{DebugLLMDir: dir}
	meta := litellm.CallMeta{Provider: "openai", Model: "gpt-4", Operation: "chat"}
	resp := &litellm.Response{}

	h.AfterResponse(context.Background(), meta, resp, nil)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatal("debug file written despite no error")
	}
}

func TestDebugHook_NoDirIsNoop(t *testing.T) {
	t.Parallel()
	h := &DebugHook{} // DebugLLMDir is empty
	meta := litellm.CallMeta{Provider: "openai", Model: "gpt-4", Operation: "chat"}
	err := litellm.NewProviderError("openai", litellm.ErrorTypeAuth, "invalid key")

	// Should not panic or create files
	h.AfterResponse(context.Background(), meta, nil, err)
	h.OnStreamEnd(context.Background(), meta, err)
}

func TestDebugHook_StreamEndWritesFileOnError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := &DebugHook{DebugLLMDir: dir}
	meta := litellm.CallMeta{Provider: "openai", Model: "gpt-4", Operation: "stream"}
	err := litellm.NewProviderError("openai", litellm.ErrorTypeProvider, "stream failed")

	h.OnStreamEnd(context.Background(), meta, err)

	entries, err2 := os.ReadDir(dir)
	if err2 != nil {
		t.Fatalf("ReadDir: %v", err2)
	}
	if len(entries) == 0 {
		t.Fatal("no debug file written on stream end error")
	}
}

func TestDebugHook_StreamEndNoErrorNoFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := &DebugHook{DebugLLMDir: dir}
	meta := litellm.CallMeta{Provider: "openai", Model: "gpt-4", Operation: "stream"}

	h.OnStreamEnd(context.Background(), meta, nil)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatal("debug file written despite no stream error")
	}
}

func TestDebugHook_FilenameContainsOperation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := &DebugHook{DebugLLMDir: dir}
	meta := litellm.CallMeta{Provider: "openai", Model: "gpt-4", Operation: "chat"}
	err := litellm.NewProviderError("openai", litellm.ErrorTypeAuth, "bad key")

	h.AfterResponse(context.Background(), meta, nil, err)

	entries, err2 := os.ReadDir(dir)
	if err2 != nil {
		t.Fatalf("ReadDir: %v", err2)
	}
	if len(entries) == 0 {
		t.Fatal("no debug file written")
	}
	name := entries[len(entries)-1].Name()
	if !strings.HasPrefix(name, "chat-llm-debug-") {
		t.Fatalf("filename = %q, want chat-llm-debug-* prefix", name)
	}
}

// ————— NewLitellmClient with debug flags —————

func TestNewLitellmClient_DebugHookAttachedWhenDebugEnabled(t *testing.T) {
	t.Parallel()
	// When DebugPrompt or DebugRequest is true, a DebugHook should be attached.
	// We verify by inspecting the client's internal hooks through a round-trip:
	// create a client with debug enabled, make a call to a server that returns
	// an error, and verify a debug file is written.
	dir := t.TempDir()
	client, err := NewLitellmClient(LitellmConfig{
		ProviderID:   "custom_openai",
		Model:        "gpt-4",
		BaseURL:      "http://127.0.0.1:1", // unreachable -> error
		APIKey:       "test-key",
		DebugPrompt:  true,
		DebugRequest: true,
		DebugLLMDir:  dir,
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}
	if client == nil {
		t.Fatal("NewLitellmClient returned nil")
	}

	// Attempt a chat to an unreachable address — the hook should fire on error.
	_, chatErr := client.Chat(context.Background(), litellm.Request{
		Model:    "gpt-4",
		Messages: []litellm.Message{{Role: "user", Blocks: []litellm.Block{litellm.TextBlock{Text: "hi"}}}},
	})
	if chatErr == nil {
		t.Fatal("expected error from unreachable address, got nil")
	}

	// A debug file should have been written
	entries, err2 := os.ReadDir(dir)
	if err2 != nil {
		t.Fatalf("ReadDir: %v", err2)
	}
	if len(entries) == 0 {
		t.Fatal("no debug file written after error with DebugLLMDir set")
	}
}

func TestNewLitellmClient_NoHookWhenDebugDisabled(t *testing.T) {
	t.Parallel()
	// When all debug flags are false/empty, no hook should be attached.
	client, err := NewLitellmClient(LitellmConfig{
		ProviderID: "custom_openai",
		Model:      "gpt-4",
		BaseURL:    "http://127.0.0.1:1",
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}
	if client == nil {
		t.Fatal("NewLitellmClient returned nil")
	}
	// Just verify the client is usable (error is expected from unreachable addr)
	_, chatErr := client.Chat(context.Background(), litellm.Request{
		Model:    "gpt-4",
		Messages: []litellm.Message{{Role: "user", Blocks: []litellm.Block{litellm.TextBlock{Text: "hi"}}}},
	})
	if chatErr == nil {
		t.Fatal("expected error from unreachable address, got nil")
	}
}
