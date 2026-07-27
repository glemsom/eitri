package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowser_Schema(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test:9222/devtools/browser/test")
	if tool.Name() != "browser" {
		t.Errorf("Name = %q, want 'browser'", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description should not be empty")
	}
	schema := tool.JSONSchema()
	if schema == nil {
		t.Fatal("JSONSchema is nil")
	}
	if !json.Valid(schema) {
		t.Error("JSONSchema is not valid JSON")
	}
}

func TestBrowser_SchemaHasActionParam(t *testing.T) {
	t.Parallel()
	schema := NewBrowserTool("ws://test").JSONSchema()
	var schemaObj map[string]any
	if err := json.Unmarshal(schema, &schemaObj); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, ok := schemaObj["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema missing properties")
	}
	actionProp, ok := props["action"]
	if !ok {
		t.Fatal("schema missing 'action' property")
	}
	actionMap, ok := actionProp.(map[string]any)
	if !ok {
		t.Fatal("action property is not a map")
	}
	if actionMap["type"] != "string" {
		t.Errorf("action type = %v, want 'string'", actionMap["type"])
	}
	required, ok := schemaObj["required"].([]any)
	if !ok {
		t.Fatal("schema missing required array")
	}
	hasAction := false
	for _, r := range required {
		if r == "action" {
			hasAction = true
			break
		}
	}
	if !hasAction {
		t.Error("'action' not in required array")
	}
}

func TestBrowser_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test")
	_, err := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestBrowser_EmptyAction(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test")
	// Create a context with a session ID
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for empty action")
	}
}

func TestBrowser_MissingAction(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for missing action")
	}
}

func TestBrowser_UnknownAction(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"unknown_action"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for unknown action")
	}
}

func TestBrowser_NoWSURL(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"list_targets"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true when WS URL is empty")
	}
}

func TestBrowser_NoSessionID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test")
	_, err := tool.Call(context.Background(), json.RawMessage(`{"action":"list_targets"}`))
	if err == nil {
		t.Fatal("expected error when no session ID in context")
	}
}

func TestBrowser_EndSession(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test")

	// EndSession on non-existent session should not panic
	tool.EndSession("non-existent-session")
}

func TestBrowser_GetOrCreateAllocator_New(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test:9222/devtools/browser/test")

	// First call creates a new allocator
	ctx, err := tool.getOrCreateAllocator("session-1")
	if err != nil {
		t.Fatalf("getOrCreateAllocator failed: %v", err)
	}
	if ctx == nil {
		t.Fatal("getOrCreateAllocator returned nil context")
	}

	// Second call should reuse
	ctx2, err := tool.getOrCreateAllocator("session-1")
	if err != nil {
		t.Fatalf("getOrCreateAllocator failed on second call: %v", err)
	}
	if ctx != ctx2 {
		t.Error("getOrCreateAllocator returned different context for same session")
	}

	// Different session should get different context
	ctx3, err := tool.getOrCreateAllocator("session-2")
	if err != nil {
		t.Fatalf("getOrCreateAllocator failed for session-2: %v", err)
	}
	if ctx == ctx3 {
		t.Error("getOrCreateAllocator returned same context for different sessions")
	}
}

func TestBrowser_EndSession_ClearsConn(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test:9222/devtools/browser/test")

	ctx, err := tool.getOrCreateAllocator("session-to-end")
	if err != nil {
		t.Fatalf("getOrCreateAllocator failed: %v", err)
	}
	if ctx == nil {
		t.Fatal("getOrCreateAllocator returned nil context")
	}

	tool.EndSession("session-to-end")

	// After EndSession, a new call should create a new allocator
	ctx2, err := tool.getOrCreateAllocator("session-to-end")
	if err != nil {
		t.Fatalf("getOrCreateAllocator after EndSession failed: %v", err)
	}
	if ctx == ctx2 {
		t.Error("getOrCreateAllocator returned same context after EndSession")
	}
}

func TestBrowser_TypeAction_MissingTargetID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"type","args":{"selector":"#input","text":"hello"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for missing target_id")
	}
}

func TestBrowser_TypeAction_MissingSelector(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"type","args":{"target_id":"tab-1","text":"hello"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for missing selector")
	}
}

func TestBrowser_TypeAction_EmptyText(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"type","args":{"target_id":"tab-1","selector":"#input","text":""}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false for empty text (no-op)")
	}
}

func TestBrowser_TypeAction_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"type","args":"not-an-object"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for invalid args")
	}
}

func TestBrowser_TypeAction_NoWSURL(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"type","args":{"target_id":"tab-1","selector":"#input","text":"hello"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true when WS URL is empty")
	}
}

func TestBrowser_TypeAction_NoSessionID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test")
	_, err := tool.Call(context.Background(), json.RawMessage(`{"action":"type","args":{"target_id":"tab-1","selector":"#input","text":"hello"}}`))
	if err == nil {
		t.Fatal("expected error when no session ID in context")
	}
}

func TestBrowser_TypeAction_ActionNameInDescription(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test")
	desc := tool.Description()
	if !strings.Contains(desc, "type") {
		t.Error("Description should mention 'type' action")
	}
}
