// Package runner tests ADK runner lifecycle and caching.
package runner

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/agent"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/executor"
)

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatal("NewManager() returned nil")
	}
	if m.SessionService() == nil {
		t.Error("SessionService() returned nil")
	}
}

func TestConfigHash_DifferentInputs(t *testing.T) {
	h1 := configHash(&config.Config{Provider: "opencode_go", APIKey: "key1", BaseURL: "http://a", Model: "m1"})
	h2 := configHash(&config.Config{Provider: "opencode_go", APIKey: "key2", BaseURL: "http://a", Model: "m1"})
	if h1 == h2 {
		t.Error("different API keys should produce different hashes")
	}

	h3 := configHash(&config.Config{Provider: "opencode_go", APIKey: "key1", BaseURL: "http://b", Model: "m1"})
	if h1 == h3 {
		t.Error("different base URLs should produce different hashes")
	}
}

func TestGetOrCreate_CachesRunner(t *testing.T) {
	m := NewManager()
	llm := agent.NewOpenAIModel("test-model", "http://test.local", "test-key")
	sessionMgr := executor.NewSessionManager(t.TempDir(), 0, 0)
	ag, err := agent.NewAgent(llm, sessionMgr)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	cfg := &config.Config{Provider: "opencode_go", APIKey: "key", BaseURL: "http://localhost", Model: "test-model"}

	r1, err := m.GetOrCreate(cfg, ag)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if r1 == nil {
		t.Fatal("runner is nil")
	}

	r2, err := m.GetOrCreate(cfg, ag)
	if err != nil {
		t.Fatalf("GetOrCreate second call: %v", err)
	}
	if r1 != r2 {
		t.Error("GetOrCreate should return cached runner for same config")
	}
}

func TestInvalidate_ClearsCache(t *testing.T) {
	m := NewManager()
	llm := agent.NewOpenAIModel("test-model", "http://test.local", "test-key")
	sessionMgr := executor.NewSessionManager(t.TempDir(), 0, 0)
	ag, err := agent.NewAgent(llm, sessionMgr)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	cfg := &config.Config{Provider: "opencode_go", APIKey: "key", BaseURL: "http://localhost", Model: "test-model"}

	r1, err := m.GetOrCreate(cfg, ag)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	m.Invalidate()

	r2, err := m.GetOrCreate(cfg, ag)
	if err != nil {
		t.Fatalf("GetOrCreate after invalidate: %v", err)
	}
	if r1 == r2 {
		t.Error("runner should be recreated after Invalidate")
	}
}

func TestRun_ReturnsChannels(t *testing.T) {
	m := NewManager()
	llm := agent.NewOpenAIModel("test-model", "http://test.local", "test-key")
	sessionMgr := executor.NewSessionManager(t.TempDir(), 0, 0)
	ag, err := agent.NewAgent(llm, sessionMgr)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	cfg := &config.Config{Provider: "opencode_go", APIKey: "key", BaseURL: "http://localhost", Model: "test-model"}
	r, err := m.GetOrCreate(cfg, ag)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	eventCh, errCh, cancel := m.Run(context.Background(), r, "user1", "session1", nil)
	defer cancel()

	if eventCh == nil {
		t.Error("event channel is nil")
	}
	if errCh == nil {
		t.Error("error channel is nil")
	}
	if cancel == nil {
		t.Error("cancel func is nil")
	}
}
