package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/litellm"
)

// ── Mock tool for testing ──────────────────────────────────────────────────

type mockTool struct {
	callFunc func(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool)
}

func (m *mockTool) Name() string        { return "mock_tool" }
func (m *mockTool) Description() string { return "Mock tool for testing" }
func (m *mockTool) JSONSchema() litellm.Schema {
	return litellm.Schema(`{"type":"object","properties":{}}`)
}
func (m *mockTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	if m.callFunc != nil {
		return m.callFunc(ctx, args)
	}
	return []litellm.Block{litellm.TextBlock{Text: "mock ok"}}, nil, false
}

type errorTool struct{}

func (e *errorTool) Name() string        { return "error_tool" }
func (e *errorTool) Description() string { return "Tool that errors" }
func (e *errorTool) JSONSchema() litellm.Schema {
	return litellm.Schema(`{"type":"object","properties":{}}`)
}
func (e *errorTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	return []litellm.Block{litellm.TextBlock{Text: "tool error"}}, nil, true
}

// ── Registry tests ─────────────────────────────────────────────────────────

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	mock := &mockTool{}
	r.Register(mock)

	if got := r.Lookup("mock_tool"); got == nil {
		t.Fatal("expected to find mock_tool")
	}

	if got := r.Lookup("nonexistent"); got != nil {
		t.Errorf("expected nil for nonexistent tool, got %v", got)
	}
}

func TestRegistry_RegisterDuplicatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for duplicate registration")
		}
	}()

	r := NewRegistry()
	r.Register(&mockTool{})
	r.Register(&mockTool{})
}

func TestRegistry_RegisterEmptyNamePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty name")
		}
	}()

	r := NewRegistry()
	r.Register(&struct {
		ToolHandler
		name string
	}{name: ""})
}

func TestRegistry_Names(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{})
	r.Register(&errorTool{})

	names := r.Names()
	if len(names) != 2 {
		t.Fatalf("len(names) = %d, want 2", len(names))
	}
	// Both should be present
	found := make(map[string]bool)
	for _, n := range names {
		found[n] = true
	}
	if !found["mock_tool"] {
		t.Error("mock_tool not in names")
	}
	if !found["error_tool"] {
		t.Error("error_tool not in names")
	}
}

func TestRegistry_LitellmTools(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{})

	tools := r.LitellmTools()
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}
	if tools[0].Name != "mock_tool" {
		t.Errorf("tool name = %q, want 'mock_tool'", tools[0].Name)
	}
}

// ── Dispatch tests ─────────────────────────────────────────────────────────

func TestDispatch_KnownTool(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{})

	blocks, err := r.Dispatch(context.Background(), "call_1", "mock_tool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	// Should be wrapped in ToolResultBlock
	result, ok := blocks[0].(litellm.ToolResultBlock)
	if !ok {
		t.Fatalf("block is %T, want ToolResultBlock", blocks[0])
	}
	if result.IsError {
		t.Error("IsError = true, want false")
	}
}

func TestDispatch_UnknownTool(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{})

	_, err := r.Dispatch(context.Background(), "call_1", "unknown_tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestDispatch_ToolErrorIsWrapped(t *testing.T) {
	r := NewRegistry()
	r.Register(&errorTool{})

	blocks, err := r.Dispatch(context.Background(), "call_1", "error_tool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	result, ok := blocks[0].(litellm.ToolResultBlock)
	if !ok {
		t.Fatalf("block is %T, want ToolResultBlock", blocks[0])
	}
	if !result.IsError {
		t.Error("IsError = false, want true")
	}
}

func TestDispatch_ContextCancelled(t *testing.T) {
	r := NewRegistry()
	mock := &mockTool{
		callFunc: func(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
			return nil, context.Canceled, false
		},
	}
	r.Register(mock)

	_, err := r.Dispatch(context.Background(), "call_1", "mock_tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
