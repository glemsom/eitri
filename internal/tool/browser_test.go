package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
			name:  "empty",
			title: "",
			elements: nil,
			wantSub: []string{"No significant DOM elements found."},
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
				{Type: "input", InputType: "text", Value: "", Placeholder: "Enter name", Selector: "body > input#name"},
				{Type: "input", InputType: "email", Value: "user@test.com", Placeholder: "", Selector: "body > input#email"},
			},
			wantSub: []string{`<text placeholder="Enter name">`, `body > input#name`, `value="user@test.com"`, `body > input#email`},
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
