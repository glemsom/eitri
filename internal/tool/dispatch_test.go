package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/litellm"
)

// ── Mock tool for testing ──────────────────────────────────────────────────

type mockTool struct {
	callFunc func(ctx context.Context, args json.RawMessage) (ToolResult, error)
}

func (m *mockTool) Name() string        { return "mock_tool" }
func (m *mockTool) Description() string { return "Mock tool for testing" }
func (m *mockTool) JSONSchema() litellm.Schema {
	return litellm.Schema(`{"type":"object","properties":{}}`)
}
func (m *mockTool) Call(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	if m.callFunc != nil {
		return m.callFunc(ctx, args)
	}
	return Success([]litellm.Block{litellm.TextBlock{Text: "mock ok"}}), nil
}

type errorTool struct{}

func (e *errorTool) Name() string        { return "error_tool" }
func (e *errorTool) Description() string { return "Tool that errors" }
func (e *errorTool) JSONSchema() litellm.Schema {
	return litellm.Schema(`{"type":"object","properties":{}}`)
}
func (e *errorTool) Call(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	return ToolError([]litellm.Block{litellm.TextBlock{Text: "tool error"}}), nil
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

	result, err := r.Dispatch(context.Background(), "call_1", "mock_tool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	// Should be wrapped in ToolResultBlock
	block, ok := result.Blocks[0].(litellm.ToolResultBlock)
	if !ok {
		t.Fatalf("block is %T, want ToolResultBlock", result.Blocks[0])
	}
	if block.IsError {
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

	result, err := r.Dispatch(context.Background(), "call_1", "error_tool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.ToolResultBlock)
	if !ok {
		t.Fatalf("block is %T, want ToolResultBlock", result.Blocks[0])
	}
	if !block.IsError {
		t.Error("IsError = false, want true")
	}
}

func TestDispatch_ContextCancelled(t *testing.T) {
	r := NewRegistry()
	mock := &mockTool{
		callFunc: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			return ToolResult{}, context.Canceled
		},
	}
	r.Register(mock)

	_, err := r.Dispatch(context.Background(), "call_1", "mock_tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ── Replace tests ──────────────────────────────────────────────────────────

func TestRegistry_Replace(t *testing.T) {
	r := NewRegistry()
	original := &mockTool{
		callFunc: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			return Success([]litellm.Block{litellm.TextBlock{Text: "original"}}), nil
		},
	}
	r.Register(original)

	replacement := &mockTool{
		callFunc: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			return Success([]litellm.Block{litellm.TextBlock{Text: "replaced"}}), nil
		},
	}
	r.Replace(replacement)

	// Verify the tool now returns the replacement's result
	result, err := r.Dispatch(context.Background(), "call_1", "mock_tool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tb, ok := result.Blocks[0].(litellm.ToolResultBlock)
	if !ok {
		t.Fatalf("block is %T, want ToolResultBlock", result.Blocks[0])
	}
	innerBlock, ok := tb.Content[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("inner block is %T, want TextBlock", tb.Content[0])
	}
	if innerBlock.Text != "replaced" {
		t.Errorf("got %q, want %q", innerBlock.Text, "replaced")
	}
}

func TestRegistry_ReplacePanicsForUnknown(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for replacing unregistered tool")
		}
	}()

	r := NewRegistry()
	r.Replace(&mockTool{})
}

func TestRegistry_ReplacePanicsForEmptyName(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty name")
		}
	}()

	r := NewRegistry()
	r.Replace(&struct {
		ToolHandler
		name string
	}{name: ""})
}

// ── All tests ──────────────────────────────────────────────────────────────

func TestRegistry_All(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{})
	r.Register(&errorTool{})

	all := r.All()
	if len(all) != 2 {
		t.Fatalf("len(all) = %d, want 2", len(all))
	}

	// Verify all returns a copy — modifying it should not affect registry
	all[0] = nil
	if r.Lookup("mock_tool") == nil {
		t.Error("registry was modified by changing All() result")
	}
}
