package tool

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/voocel/litellm"
)

func TestBrowser_Schema(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test:9222/devtools/browser/test", "/tmp")
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
	schema := NewBrowserTool("ws://test", "/tmp").JSONSchema()
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
	// The action field should declare an enum of valid browser actions.
	enum, ok := actionMap["enum"].([]any)
	if !ok {
		t.Fatal("action should have an 'enum' of valid actions")
	}
	wantActions := []any{"list_targets", "navigate", "get_dom", "click", "type", "screenshot", "new_tab", "close_tab", "select", "get_value"}
	if len(enum) != len(wantActions) {
		t.Errorf("len(action enum) = %d, want %d", len(enum), len(wantActions))
	}
	for _, want := range wantActions {
		found := false
		for _, got := range enum {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("action enum missing %v (got %v)", want, enum)
		}
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

func TestBrowser_SchemaArgsDiscriminatedUnion(t *testing.T) {
	t.Parallel()
	schema := NewBrowserTool("ws://test", "/tmp").JSONSchema()
	var schemaObj map[string]any
	if err := json.Unmarshal(schema, &schemaObj); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, ok := schemaObj["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema missing properties")
	}
	argsProp, ok := props["args"]
	if !ok {
		t.Fatal("schema missing 'args' property")
	}
	argsMap, ok := argsProp.(map[string]any)
	if !ok {
		t.Fatal("args property is not a map")
	}
	// args must be a discriminated union of per-action typed schemas, not a
	// free-form blob: no type/additionalProperties, and a oneOf per action.
	if _, hasType := argsMap["type"]; hasType {
		t.Error("args should not have a 'type' (it is a oneOf union, not an object blob)")
	}
	if _, hasItems := argsMap["items"]; hasItems {
		t.Error("args should not have 'items' property")
	}
	if _, hasAdditionalProps := argsMap["additionalProperties"]; hasAdditionalProps {
		t.Error("args should not have additionalProperties (free-form blob is gone)")
	}

	oneOf, ok := argsMap["oneOf"].([]any)
	if !ok {
		t.Fatal("args should have a 'oneOf' discriminated union")
	}
	if len(oneOf) != len(browserActions) {
		t.Errorf("len(args oneOf) = %d, want %d (one branch per action)", len(oneOf), len(browserActions))
	}

	// Each branch is a typed object schema with the action's required params.
	type branchCheck struct {
		required []string
	}
	// Map from oneOf index to the expected required params, in browserActions order.
	want := []branchCheck{
		{nil},                                        // list_targets: no args
		{[]string{"target_id", "url"}},               // navigate
		{[]string{"target_id"}},                      // get_dom (selector optional)
		{[]string{"target_id", "selector"}},          // click
		{[]string{"target_id", "selector", "text"}},  // type
		{[]string{"target_id"}},                      // screenshot
		{nil},                                        // new_tab: no args
		{[]string{"target_id"}},                      // close_tab
		{[]string{"target_id", "selector", "value"}}, // select
		{[]string{"target_id", "selector"}},          // get_value
	}
	if len(want) != len(browserActions) {
		t.Fatalf("test table has %d entries, want %d", len(want), len(browserActions))
	}
	for i, branch := range oneOf {
		branchMap, ok := branch.(map[string]any)
		if !ok {
			t.Fatalf("oneOf[%d] is not an object schema", i)
		}
		if branchMap["type"] != "object" {
			t.Errorf("oneOf[%d] (%s) type = %v, want 'object'", i, browserActions[i], branchMap["type"])
		}
		if _, ok := branchMap["additionalProperties"]; ok {
			t.Errorf("oneOf[%d] (%s) should not set additionalProperties (plain per-action object schema)", i, browserActions[i])
		}
		required, _ := branchMap["required"].([]any)
		if len(required) != len(want[i].required) {
			t.Errorf("oneOf[%d] (%s) required = %v, want %v", i, browserActions[i], required, want[i].required)
			continue
		}
		for _, wantReq := range want[i].required {
			found := false
			for _, gotReq := range required {
				if gotReq == wantReq {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("oneOf[%d] (%s) required missing %q (got %v)", i, browserActions[i], wantReq, required)
			}
		}
	}
}

func TestBrowser_SchemaArgsBlobDescriptionGone(t *testing.T) {
	t.Parallel()
	schema := NewBrowserTool("ws://test", "/tmp").JSONSchema()
	if bytes.Contains(schema, []byte("Action-specific JSON parameters")) {
		t.Error("schema still contains the old free-form args blob description")
	}
}

func TestBrowser_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	_, err := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestBrowser_EmptyAction(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
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
	tool := NewBrowserTool("ws://test", "/tmp")
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
	tool := NewBrowserTool("ws://test", "/tmp")
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
	tool := NewBrowserTool("", "/tmp")
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
	tool := NewBrowserTool("ws://test", "/tmp")
	_, err := tool.Call(context.Background(), json.RawMessage(`{"action":"list_targets"}`))
	if err == nil {
		t.Fatal("expected error when no session ID in context")
	}
}

func TestBrowser_EndSession(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")

	// EndSession on non-existent session should not panic
	tool.EndSession("non-existent-session")
}

func TestBrowser_SequentialRuns_NoAccumulation(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test:9222/devtools/browser/test", "/tmp")

	sessionID := "test-session-sequential"

	// Simulate multiple runs in sequence
	for run := 0; run < 3; run++ {
		// Each run would create allocator connections
		// In a real scenario, this happens via tool.Call()
		// For this test, we simulate by calling getOrCreateAllocator
		_, err := tool.getOrCreateAllocator(sessionID)
		if err != nil {
			t.Fatalf("run %d: failed to create allocator: %v", run, err)
		}

		// Simulate target context creation (as would happen with browser actions)
		tool.targetsMu.Lock()
		if tool.targets[sessionID] == nil {
			tool.targets[sessionID] = make(map[string]*targetContext)
		}
		// Create a fake target context
		targetCtx, cancel := context.WithCancel(context.Background())
		tool.targets[sessionID]["fake-target"] = &targetContext{
			ctx:    targetCtx,
			cancel: cancel,
		}
		tool.targetsMu.Unlock()

		// Verify resources were created
		tool.mu.Lock()
		if _, exists := tool.conns[sessionID]; !exists {
			t.Fatalf("run %d: allocator should exist after creation", run)
		}
		tool.mu.Unlock()

		tool.targetsMu.Lock()
		if len(tool.targets[sessionID]) == 0 {
			t.Fatalf("run %d: targets should exist after creation", run)
		}
		tool.targetsMu.Unlock()

		// Call EndSession to clean up (as would happen in deferred cleanup)
		tool.EndSession(sessionID)

		// Verify resources were cleaned up
		tool.mu.Lock()
		if _, exists := tool.conns[sessionID]; exists {
			t.Errorf("run %d: allocator should not exist after EndSession", run)
		}
		tool.mu.Unlock()

		tool.targetsMu.Lock()
		if _, exists := tool.targets[sessionID]; exists {
			t.Errorf("run %d: targets should not exist after EndSession", run)
		}
		tool.targetsMu.Unlock()
	}
}


func TestBrowser_GetOrCreateAllocator_New(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test:9222/devtools/browser/test", "/tmp")

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
	tool := NewBrowserTool("ws://test:9222/devtools/browser/test", "/tmp")

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
	tool := NewBrowserTool("ws://test", "/tmp")
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
	tool := NewBrowserTool("ws://test", "/tmp")
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
	tool := NewBrowserTool("ws://test", "/tmp")
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
	tool := NewBrowserTool("ws://test", "/tmp")
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
	tool := NewBrowserTool("", "/tmp")
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
	tool := NewBrowserTool("ws://test", "/tmp")
	_, err := tool.Call(context.Background(), json.RawMessage(`{"action":"type","args":{"target_id":"tab-1","selector":"#input","text":"hello"}}`))
	if err == nil {
		t.Fatal("expected error when no session ID in context")
	}
}

func TestBrowser_TypeAction_ActionNameInDescription(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	desc := tool.Description()
	if !strings.Contains(desc, "type") {
		t.Error("Description should mention 'type' action")
	}
}

func TestBrowser_NavigateAction_MissingTargetID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"navigate","args":{"url":"https://example.com"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for missing target_id")
	}
}

func TestBrowser_NavigateAction_MissingURL(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"navigate","args":{"target_id":"tab-1"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for missing url")
	}
}

func TestBrowser_NavigateAction_InvalidURL(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"navigate","args":{"target_id":"tab-1","url":"not-a-url"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for invalid url")
	}
	if len(result.Blocks) > 0 {
		if text, ok := result.Blocks[0].(litellm.TextBlock); ok {
			if !strings.Contains(text.Text, "must start with http:// or https://") {
				t.Errorf("error message should mention URL format, got: %s", text.Text)
			}
		}
	}
}

func TestBrowser_NavigateAction_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"navigate","args":"not-an-object"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for invalid args")
	}
}

func TestBrowser_NavigateAction_NoWSURL(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"navigate","args":{"target_id":"tab-1","url":"https://example.com"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true when WS URL is empty")
	}
}

func TestBrowser_NavigateAction_NoSessionID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	_, err := tool.Call(context.Background(), json.RawMessage(`{"action":"navigate","args":{"target_id":"tab-1","url":"https://example.com"}}`))
	if err == nil {
		t.Fatal("expected error when no session ID in context")
	}
}

func TestBrowser_NavigateAction_ActionNameInDescription(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	desc := tool.Description()
	if !strings.Contains(desc, "navigate") {
		t.Error("Description should mention 'navigate' action")
	}
}

func TestBrowser_ClickAction_MissingTargetID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"click","args":{"selector":"#btn"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for missing target_id")
	}
}

func TestBrowser_ClickAction_MissingSelector(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"click","args":{"target_id":"tab-1"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for missing selector")
	}
}

func TestBrowser_ClickAction_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"click","args":"not-an-object"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for invalid args")
	}
}

func TestBrowser_ClickAction_NoWSURL(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"click","args":{"target_id":"tab-1","selector":"#btn"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true when WS URL is empty")
	}
}

func TestBrowser_ClickAction_NoSessionID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	_, err := tool.Call(context.Background(), json.RawMessage(`{"action":"click","args":{"target_id":"tab-1","selector":"#btn"}}`))
	if err == nil {
		t.Fatal("expected error when no session ID in context")
	}
}

func TestBrowser_ClickAction_ActionNameInDescription(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	desc := tool.Description()
	if !strings.Contains(desc, "click") {
		t.Error("Description should mention 'click' action")
	}
}

func TestBrowser_BuildDOMSummary(t *testing.T) {
	t.Parallel()

	html := `<html><body>
		<h1>Main Title</h1>
		<h2>Sub heading</h2>
		<p>Some text</p>
		<a href="/link1">Link 1</a>
		<a href="/link2">Link 2</a>
		<input type="text" name="q">
		<button>Submit</button>
	</body></html>`

	summary := buildDOMSummary(html)

	if !strings.Contains(summary, "h1: Main Title") {
		t.Error("summary should contain heading h1")
	}
	if !strings.Contains(summary, "h2: Sub heading") {
		t.Error("summary should contain heading h2")
	}
	if !strings.Contains(summary, "Links: 2") {
		t.Error("summary should contain link count 2")
	}
	if !strings.Contains(summary, "Inputs: 1") {
		t.Error("summary should contain input count 1")
	}
	if !strings.Contains(summary, "Buttons: 1") {
		t.Error("summary should contain button count 1")
	}
}

func TestBrowser_BuildDOMSummary_Empty(t *testing.T) {
	t.Parallel()
	summary := buildDOMSummary("")
	if summary == "" {
		t.Error("summary should not be empty even for empty HTML")
	}
}

func TestBrowser_BuildDOMSummary_HeadingsWithInnerTags(t *testing.T) {
	t.Parallel()
	html := `<h1><span class="highlight">Styled</span> Title</h1>`
	summary := buildDOMSummary(html)
	if !strings.Contains(summary, "h1: Styled Title") {
		t.Errorf("summary should strip inner tags, got: %s", summary)
	}
}

func TestBrowser_StripTags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"<b>bold</b>", "bold"},
		{"<a href='x'>link</a>", "link"},
		{"<h1>Title</h1>", "Title"},
		{"no tags", "no tags"},
		{"", ""},
		{"<br/>", ""},
	}
	for _, tt := range tests {
		got := stripTags(tt.input)
		if got != tt.want {
			t.Errorf("stripTags(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- Screenshot action tests ---

func TestBrowser_ScreenshotAction_MissingTargetID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"screenshot","args":{}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for missing target_id")
	}
	if len(result.Blocks) > 0 {
		if text, ok := result.Blocks[0].(litellm.TextBlock); ok {
			if !strings.Contains(text.Text, "target_id") {
				t.Errorf("error message should mention target_id, got: %s", text.Text)
			}
		}
	}
}

func TestBrowser_ScreenshotAction_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"screenshot","args":"not-an-object"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for invalid args")
	}
}

func TestBrowser_ScreenshotAction_NoWSURL(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"screenshot","args":{"target_id":"tab-1"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true when WS URL is empty")
	}
}

func TestBrowser_ScreenshotAction_NoSessionID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	_, err := tool.Call(context.Background(), json.RawMessage(`{"action":"screenshot","args":{"target_id":"tab-1"}}`))
	if err == nil {
		t.Fatal("expected error when no session ID in context")
	}
}

func TestBrowser_ScreenshotAction_ActionNameInDescription(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	desc := tool.Description()
	if !strings.Contains(desc, "screenshot") {
		t.Error("Description should mention 'screenshot' action")
	}
}

// --- get_dom action tests ---

func TestBrowser_GetDOMAction_MissingTargetID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"get_dom","args":{}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for missing target_id")
	}
	if len(result.Blocks) > 0 {
		if text, ok := result.Blocks[0].(litellm.TextBlock); ok {
			if !strings.Contains(text.Text, "target_id") {
				t.Errorf("error message should mention target_id, got: %s", text.Text)
			}
		}
	}
}

func TestBrowser_GetDOMAction_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"get_dom","args":"not-an-object"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for invalid args")
	}
}

func TestBrowser_GetDOMAction_NoWSURL(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"get_dom","args":{"target_id":"tab-1"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true when WS URL is empty")
	}
}

func TestBrowser_GetDOMAction_NoSessionID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	_, err := tool.Call(context.Background(), json.RawMessage(`{"action":"get_dom","args":{"target_id":"tab-1"}}`))
	if err == nil {
		t.Fatal("expected error when no session ID in context")
	}
}

func TestBrowser_GetDOMAction_ActionNameInDescription(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	desc := tool.Description()
	if !strings.Contains(desc, "get_dom") {
		t.Error("Description should mention 'get_dom' action")
	}
}

func TestBrowser_CleanDOMHTML(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{
			input: `<div>Hello</div>`,
			want:  `<div>Hello</div>`,
		},
		{
			input: `<div><script>alert('xss')</script><p>text</p></div>`,
			want:  `<div><p>text</p></div>`,
		},
		{
			input: `<div><style>body { color: red; }</style><p>text</p></div>`,
			want:  `<div><p>text</p></div>`,
		},
		{
			input: `<div><!-- comment --><p>text</p></div>`,
			want:  `<div><p>text</p></div>`,
		},
		{
			input: `<div>  lots   of   spaces  </div>`,
			want:  `<div> lots of spaces </div>`,
		},
		{
			input: `<div><script>a</script><style>b</style><!-- c --><p>d</p></div>`,
			want:  `<div><p>d</p></div>`,
		},
	}
	for _, tt := range tests {
		got := cleanDOMHTML(tt.input)
		if got != tt.want {
			t.Errorf("cleanDOMHTML(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBrowser_FormatDOMSummary(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")

	tests := []struct {
		name     string
		title    string
		elements []domElement
		wantSub  []string // substrings that must be present
		notWant  []string // substrings that must NOT be present
	}{
		{
			name:     "empty",
			title:    "",
			elements: nil,
			wantSub:  []string{"No significant DOM elements found."},
		},
		{
			name:  "with title and headings",
			title: "Test Page",
			elements: []domElement{
				{Type: "heading", Level: "h1", Text: "Main Title", Selector: "body > h1"},
				{Type: "heading", Level: "h2", Text: "Sub Title", Selector: "body > h2"},
			},
			wantSub: []string{"Title: Test Page", "h1: Main Title", "h2: Sub Title", "body > h1", "body > h2"},
		},
		{
			name:  "with links",
			title: "",
			elements: []domElement{
				{Type: "link", Text: "Click Here", Href: "https://example.com", Selector: "body > a"},
			},
			wantSub: []string{"Click Here", "https://example.com", "body > a"},
		},
		{
			name:  "with buttons",
			title: "",
			elements: []domElement{
				{Type: "button", Text: "Submit", Selector: "body > button#submit"},
			},
			wantSub: []string{"Submit", "body > button#submit"},
		},
		{
			name:  "with inputs",
			title: "",
			elements: []domElement{
				{Type: "input", InputType: "text", Value: "", Placeholder: "Enter name", Name: "username", Selector: "body > input#name"},
				{Type: "input", InputType: "email", Value: "user@test.com", Placeholder: "", Selector: "body > input#email"},
			},
			wantSub: []string{`<text placeholder="Enter name">`, `name="username"`, `body > input#name`, `value="user@test.com"`, `body > input#email`},
		},
		{
			name:  "link with no text",
			title: "",
			elements: []domElement{
				{Type: "link", Text: "", Href: "https://example.com", Selector: "body > a"},
			},
			wantSub: []string{"(no text)", "https://example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.formatDOMSummary(tt.title, tt.elements)
			if !result.IsError && result.IsError {
				t.Error("result.IsError = true, want false")
			}
			if len(result.Blocks) == 0 {
				t.Fatal("result has no blocks")
			}
			text, ok := result.Blocks[0].(litellm.TextBlock)
			if !ok {
				t.Fatal("result block is not a TextBlock")
			}
			for _, sub := range tt.wantSub {
				if !strings.Contains(text.Text, sub) {
					t.Errorf("result missing %q:\n%s", sub, text.Text)
				}
			}
			for _, sub := range tt.notWant {
				if strings.Contains(text.Text, sub) {
					t.Errorf("result should not contain %q:\n%s", sub, text.Text)
				}
			}
		})
	}
}

func TestBrowser_FormatDOMSummary_Truncation(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")

	// Build a very long title to trigger truncation
	longTitle := strings.Repeat("Long Title ", 5000)
	elements := []domElement{
		{Type: "heading", Level: "h1", Text: longTitle, Selector: "h1"},
	}

	result := tool.formatDOMSummary("", elements)
	if len(result.Blocks) == 0 {
		t.Fatal("result has no blocks")
	}
	text, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatal("result block is not a TextBlock")
	}
	if !strings.Contains(text.Text, "output truncated") {
		t.Error("expected truncation message in output")
	}
}

// browserResultText extracts the first text block from a ToolResult.
func browserResultText(t *testing.T, res ToolResult) string {
	t.Helper()
	if len(res.Blocks) == 0 {
		return ""
	}
	block, ok := res.Blocks[0].(litellm.TextBlock)
	if !ok {
		return ""
	}
	return block.Text
}

// browserTargetID extracts the trailing target_id from a new_tab result text.
func browserTargetID(t *testing.T, text string) string {
	t.Helper()
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

// browserTargets parses a list_targets result into its target entries.
func browserTargets(t *testing.T, res ToolResult) []testTargetInfo {
	t.Helper()
	text := browserResultText(t, res)
	var targets []testTargetInfo
	if err := json.Unmarshal([]byte(text), &targets); err != nil {
		t.Fatalf("failed to parse list_targets result %q: %v", text, err)
	}
	return targets
}

type testTargetInfo struct {
	TargetID string `json:"target_id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

func TestBrowser_NewTabAction_NoWSURL(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"new_tab"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true when WS URL is empty")
	}
}

func TestBrowser_NewTabAction_NoSessionID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	_, err := tool.Call(context.Background(), json.RawMessage(`{"action":"new_tab"}`))
	if err == nil {
		t.Fatal("expected error when no session ID in context")
	}
}

func TestBrowser_NewTabAction_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"new_tab","args":"not-an-object"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for invalid args")
	}
}

func TestBrowser_NewTabAction_ActionNameInDescription(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	desc := tool.Description()
	if !strings.Contains(desc, "new_tab") {
		t.Error("Description should mention 'new_tab' action")
	}
}

func TestBrowser_CloseTabAction_MissingTargetID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"close_tab"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for missing target_id")
	}
	text := browserResultText(t, result)
	if !strings.Contains(text, "target_id") {
		t.Errorf("error should mention target_id, got: %s", text)
	}
}

func TestBrowser_CloseTabAction_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"close_tab","args":"not-an-object"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for invalid args")
	}
}

func TestBrowser_CloseTabAction_NoWSURL(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"close_tab","args":{"target_id":"tab-1"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true when WS URL is empty")
	}
}

func TestBrowser_CloseTabAction_NoSessionID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	_, err := tool.Call(context.Background(), json.RawMessage(`{"action":"close_tab","args":{"target_id":"tab-1"}}`))
	if err == nil {
		t.Fatal("expected error when no session ID in context")
	}
}

func TestBrowser_CloseTabAction_ActionNameInDescription(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	desc := tool.Description()
	if !strings.Contains(desc, "close_tab") {
		t.Error("Description should mention 'close_tab' action")
	}
}

func TestBrowser_SelectAction_MissingTargetID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"select","args":{"selector":"#color","value":"red"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for missing target_id")
	}
}

func TestBrowser_SelectAction_MissingSelector(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"select","args":{"target_id":"tab-1","value":"red"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for missing selector")
	}
}

func TestBrowser_SelectAction_MissingValue(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"select","args":{"target_id":"tab-1","selector":"#color"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for missing value")
	}
}

func TestBrowser_SelectAction_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"select","args":"not-an-object"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for invalid args")
	}
}

func TestBrowser_SelectAction_NoWSURL(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"select","args":{"target_id":"tab-1","selector":"#color","value":"red"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true when WS URL is empty")
	}
}

func TestBrowser_SelectAction_NoSessionID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	_, err := tool.Call(context.Background(), json.RawMessage(`{"action":"select","args":{"target_id":"tab-1","selector":"#color","value":"red"}}`))
	if err == nil {
		t.Fatal("expected error when no session ID in context")
	}
}

func TestBrowser_SelectAction_ActionNameInDescription(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	desc := tool.Description()
	if !strings.Contains(desc, "select") {
		t.Error("Description should mention 'select' action")
	}
}

func TestBrowser_GetValueAction_MissingTargetID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"get_value","args":{"selector":"#name"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for missing target_id")
	}
}

func TestBrowser_GetValueAction_MissingSelector(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"get_value","args":{"target_id":"tab-1"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for missing selector")
	}
}

func TestBrowser_GetValueAction_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"get_value","args":"not-an-object"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for invalid args")
	}
}

func TestBrowser_GetValueAction_NoWSURL(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("", "/tmp")
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"get_value","args":{"target_id":"tab-1","selector":"#name"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true when WS URL is empty")
	}
}

func TestBrowser_GetValueAction_NoSessionID(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	_, err := tool.Call(context.Background(), json.RawMessage(`{"action":"get_value","args":{"target_id":"tab-1","selector":"#name"}}`))
	if err == nil {
		t.Fatal("expected error when no session ID in context")
	}
}

func TestBrowser_GetValueAction_ActionNameInDescription(t *testing.T) {
	t.Parallel()
	tool := NewBrowserTool("ws://test", "/tmp")
	desc := tool.Description()
	if !strings.Contains(desc, "get_value") {
		t.Error("Description should mention 'get_value' action")
	}
}

// findChromePath searches common locations for a Chrome/Chromium binary.
func findChromePath() string {
	candidates := []string{
		"google-chrome-stable",
		"google-chrome",
		"chromium-browser",
		"chromium",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium-browser",
	}
	for _, path := range candidates {
		if _, err := exec.LookPath(path); err == nil {
			return path
		}
	}
	return ""
}

// startRemoteChrome launches a headless Chrome with a remote debugging port and
// returns its browser WebSocket URL plus a cleanup function. Tests skip if
// Chrome/Chromium is not installed.
func startRemoteChrome(t *testing.T) (string, func()) {
	t.Helper()
	chromePath := findChromePath()
	if chromePath == "" {
		t.Skip("Chrome/Chromium not found — skipping browser test")
	}

	// Reserve a free port for the remote debugging endpoint.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find a free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	userDataDir, err := os.MkdirTemp("", "eitri-browser-test-*")
	if err != nil {
		t.Fatalf("failed to create Chrome user data dir: %v", err)
	}

	cmd := exec.Command(chromePath,
		"--headless=new",
		"--no-sandbox",
		"--disable-gpu",
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
		"about:blank",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start Chrome: %v", err)
	}

	var wsURL string
	deadline := time.Now().Add(30 * time.Second)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/json/version")
		if err == nil {
			var v struct {
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			decodeErr := json.NewDecoder(resp.Body).Decode(&v)
			resp.Body.Close()
			if decodeErr == nil && v.WebSocketDebuggerURL != "" {
				wsURL = v.WebSocketDebuggerURL
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	if wsURL == "" {
		cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(userDataDir)
		t.Fatal("Chrome did not expose the DevTools endpoint in time")
	}

	cleanup := func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = os.RemoveAll(userDataDir)
	}
	return wsURL, cleanup
}

// TestBrowser_NewTabCloseTab_EndToEnd verifies that new_tab opens a fresh tab,
// returns its target_id, and close_tab closes it again.
func TestBrowser_NewTabCloseTab_EndToEnd(t *testing.T) {
	wsURL, cleanup := startRemoteChrome(t)
	defer cleanup()

	tool := NewBrowserTool(wsURL, t.TempDir())
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	defer tool.EndSession("test-session")

	// new_tab opens a fresh tab and returns its target_id.
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"new_tab"}`))
	if err != nil {
		t.Fatalf("new_tab returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("new_tab failed: %s", browserResultText(t, result))
	}
	targetID := browserTargetID(t, browserResultText(t, result))
	if targetID == "" {
		t.Fatalf("new_tab did not return a target_id, got: %q", browserResultText(t, result))
	}

	// The new tab shows up in list_targets.
	seen := false
	for i := 0; i < 50; i++ {
		result, err = tool.Call(ctx, json.RawMessage(`{"action":"list_targets"}`))
		if err != nil {
			t.Fatalf("list_targets returned error: %v", err)
		}
		for _, tgt := range browserTargets(t, result) {
			if tgt.TargetID == targetID {
				seen = true
				break
			}
		}
		if seen {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !seen {
		t.Fatal("new tab did not appear in list_targets")
	}

	// close_tab closes the tab.
	result, err = tool.Call(ctx, json.RawMessage(fmt.Sprintf(`{"action":"close_tab","args":{"target_id":%q}}`, targetID)))
	if err != nil {
		t.Fatalf("close_tab returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("close_tab failed: %s", browserResultText(t, result))
	}

	// The closed tab disappears from list_targets.
	gone := false
	for i := 0; i < 50; i++ {
		result, err = tool.Call(ctx, json.RawMessage(`{"action":"list_targets"}`))
		if err != nil {
			t.Fatalf("list_targets returned error: %v", err)
		}
		stillThere := false
		for _, tgt := range browserTargets(t, result) {
			if tgt.TargetID == targetID {
				stillThere = true
				break
			}
		}
		if !stillThere {
			gone = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !gone {
		t.Fatal("closed tab still appears in list_targets")
	}
}

// TestBrowser_SelectGetValue_EndToEnd verifies select sets a <select> option and
// get_value reads back the current value of form elements.
func TestBrowser_SelectGetValue_EndToEnd(t *testing.T) {
	wsURL, cleanup := startRemoteChrome(t)
	defer cleanup()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!DOCTYPE html><html><head><title>Form Test</title></head><body>
			<input id="name" type="text" placeholder="Name">
			<select id="color">
				<option value="">Pick a color</option>
				<option value="red">Red</option>
				<option value="green">Green</option>
			</select>
		</body></html>`)
	}))
	defer srv.Close()

	tool := NewBrowserTool(wsURL, t.TempDir())
	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	defer tool.EndSession("test-session")

	// Open a fresh tab and navigate it to the form page.
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"new_tab"}`))
	if err != nil {
		t.Fatalf("new_tab returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("new_tab failed: %s", browserResultText(t, result))
	}
	targetID := browserTargetID(t, browserResultText(t, result))
	if targetID == "" {
		t.Fatalf("new_tab did not return a target_id, got: %q", browserResultText(t, result))
	}

	result, err = tool.Call(ctx, json.RawMessage(fmt.Sprintf(`{"action":"navigate","args":{"target_id":%q,"url":%q}}`, targetID, srv.URL)))
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("navigate failed: %s", browserResultText(t, result))
	}

	// get_value on an empty text input returns "".
	result, err = tool.Call(ctx, json.RawMessage(fmt.Sprintf(`{"action":"get_value","args":{"target_id":%q,"selector":"#name"}}`, targetID)))
	if err != nil {
		t.Fatalf("get_value returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("get_value failed: %s", browserResultText(t, result))
	}
	if !strings.Contains(browserResultText(t, result), `""`) {
		t.Errorf("expected empty initial input value, got: %s", browserResultText(t, result))
	}

	// Type text, then get_value reads it back.
	result, err = tool.Call(ctx, json.RawMessage(fmt.Sprintf(`{"action":"type","args":{"target_id":%q,"selector":"#name","text":"Alice"}}`, targetID)))
	if err != nil {
		t.Fatalf("type returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("type failed: %s", browserResultText(t, result))
	}

	result, err = tool.Call(ctx, json.RawMessage(fmt.Sprintf(`{"action":"get_value","args":{"target_id":%q,"selector":"#name"}}`, targetID)))
	if err != nil {
		t.Fatalf("get_value returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("get_value failed: %s", browserResultText(t, result))
	}
	if !strings.Contains(browserResultText(t, result), "Alice") {
		t.Errorf("get_value did not report typed text, got: %s", browserResultText(t, result))
	}

	// select sets the <select> dropdown value.
	result, err = tool.Call(ctx, json.RawMessage(fmt.Sprintf(`{"action":"select","args":{"target_id":%q,"selector":"#color","value":"green"}}`, targetID)))
	if err != nil {
		t.Fatalf("select returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("select failed: %s", browserResultText(t, result))
	}

	// get_value on the <select> reports the chosen option value.
	result, err = tool.Call(ctx, json.RawMessage(fmt.Sprintf(`{"action":"get_value","args":{"target_id":%q,"selector":"#color"}}`, targetID)))
	if err != nil {
		t.Fatalf("get_value returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("get_value failed: %s", browserResultText(t, result))
	}
	if !strings.Contains(browserResultText(t, result), "green") {
		t.Errorf("get_value did not report selected option, got: %s", browserResultText(t, result))
	}

	// get_value on a missing element reports an error.
	result, err = tool.Call(ctx, json.RawMessage(fmt.Sprintf(`{"action":"get_value","args":{"target_id":%q,"selector":"#missing"}}`, targetID)))
	if err != nil {
		t.Fatalf("get_value returned error: %v", err)
	}
	if !result.IsError {
		t.Error("get_value on a missing element should report an error")
	}
}

// startHungCDPServer starts a fake CDP endpoint that completes the WebSocket
// handshake but then never responds to any command, simulating a browser whose
// CDP connection is established but hung. It returns a wsURL suitable for
// NewBrowserTool.
func startHungCDPServer(t *testing.T) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Complete the RFC 6455 handshake, then hold the connection open and
		// silently drain any frames without ever replying.
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "expected websocket upgrade", http.StatusBadRequest)
			return
		}
		key := r.Header.Get("Sec-WebSocket-Key")
		h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		accept := base64.StdEncoding.EncodeToString(h[:])

		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijacking not supported", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()

		if _, err := rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"); err != nil {
			return
		}
		if err := rw.Flush(); err != nil {
			return
		}

		buf := make([]byte, 4096)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	return "ws://" + strings.TrimPrefix(srv.URL, "http://") + "/devtools/browser/test"
}

// TestBrowser_ClickAction_HungConnectionTimesOut verifies that a click on a
// target whose CDP connection never responds returns within the browser action
// timeout instead of blocking the agent loop indefinitely (issue #951). It
// drives a real chromedp dial against a hung endpoint with a shortened
// actionTimeout so the regression is caught quickly.
func TestBrowser_ClickAction_HungConnectionTimesOut(t *testing.T) {
	tool := NewBrowserTool(startHungCDPServer(t), t.TempDir())
	tool.actionTimeout = 150 * time.Millisecond

	ctx := context.WithValue(context.Background(), SessionIDKey, "test-session")
	defer tool.EndSession("test-session")

	start := time.Now()
	result, err := tool.Call(ctx, json.RawMessage(`{"action":"click","args":{"target_id":"tab-1","selector":"#btn"}}`))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("result.IsError = false, want error when the CDP connection hangs")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("click blocked the agent loop: took %v, want return within the action timeout", elapsed)
	}
	text, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatal("result block is not a TextBlock")
	}
	if !strings.Contains(text.Text, "failed to attach to target") {
		t.Errorf("error should report the attachment failure, got: %s", text.Text)
	}
	if !strings.Contains(text.Text, "did not initialize") {
		t.Errorf("error should report the init deadline, got: %s", text.Text)
	}
}
