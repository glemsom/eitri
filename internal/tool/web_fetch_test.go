package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/voocel/litellm"
)

func TestWebFetch_Schema(t *testing.T) {
	t.Parallel()
	tool := NewWebFetchTool()
	if tool.Name() != "web_fetch" {
		t.Errorf("Name = %q, want 'web_fetch'", tool.Name())
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

func TestWebFetch_SchemaHasURLParam(t *testing.T) {
	t.Parallel()
	schema := NewWebFetchTool().JSONSchema()
	var schemaObj map[string]any
	if err := json.Unmarshal(schema, &schemaObj); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, ok := schemaObj["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema missing properties")
	}
	urlProp, ok := props["url"]
	if !ok {
		t.Fatal("schema missing 'url' property")
	}
	urlMap, ok := urlProp.(map[string]any)
	if !ok {
		t.Fatal("url property is not a map")
	}
	if urlMap["type"] != "string" {
		t.Errorf("url type = %v, want 'string'", urlMap["type"])
	}
	required, ok := schemaObj["required"].([]any)
	if !ok {
		t.Fatal("schema missing required array")
	}
	hasURL := false
	for _, r := range required {
		if r == "url" {
			hasURL = true
			break
		}
	}
	if !hasURL {
		t.Error("'url' not in required array")
	}
}

func TestWebFetch_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewWebFetchTool()
	_, err := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestWebFetch_EmptyURL(t *testing.T) {
	t.Parallel()
	tool := NewWebFetchTool()
	result, err := tool.Call(context.Background(), json.RawMessage(`{"url":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for empty URL")
	}
}

func TestWebFetch_MissingURL(t *testing.T) {
	t.Parallel()
	tool := NewWebFetchTool()
	result, err := tool.Call(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true (url is required)")
	}
}

func TestWebFetch_InvalidURL(t *testing.T) {
	t.Parallel()
	tool := NewWebFetchTool()
	result, err := tool.Call(context.Background(), json.RawMessage(`{"url":"not-a-valid-url"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for invalid URL")
	}
}

func TestWebFetch_FetchSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Test Page</title></head><body><h1>Hello</h1><p>World</p></body></html>`)
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	result, err := tool.Call(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	tb, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block type = %T, want TextBlock", result.Blocks[0])
	}
	// Should contain title, source, and markdown
	if !strings.Contains(tb.Text, "Test Page") {
		t.Errorf("output missing title 'Test Page': %q", tb.Text)
	}
	if !strings.Contains(tb.Text, srv.URL) {
		t.Errorf("output missing source URL %q: %q", srv.URL, tb.Text)
	}
	if !strings.Contains(tb.Text, "# Hello") {
		t.Errorf("output missing heading '# Hello': %q", tb.Text)
	}
}

func TestWebFetch_StripUnwantedElements(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Clean Page</title></head><body>
<script>alert('bad')</script>
<style>.foo{}</style>
<nav>Nav links</nav>
<footer>Footer text</footer>
<header>Header text</header>
<aside>Sidebar</aside>
<p>Real content</p>
</body></html>`)
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	result, err := tool.Call(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	tb := result.Blocks[0].(litellm.TextBlock)
	// Unwanted elements should not appear
	for _, unwanted := range []string{"alert('bad')", ".foo{}", "Nav links", "Footer text", "Header text", "Sidebar"} {
		if strings.Contains(tb.Text, unwanted) {
			t.Errorf("output contains unwanted text %q", unwanted)
		}
	}
	if !strings.Contains(tb.Text, "Real content") {
		t.Errorf("output missing real content")
	}
}

func TestWebFetch_Headings(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Headings</title></head><body>
<h1>Level 1</h1>
<h2>Level 2</h2>
<h3>Level 3</h3>
<h4>Level 4</h4>
<h5>Level 5</h5>
<h6>Level 6</h6>
</body></html>`)
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	result, _ := tool.Call(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	tb := result.Blocks[0].(litellm.TextBlock)

	checks := []struct {
		level int
		want  string
	}{
		{1, "# Level 1"},
		{2, "## Level 2"},
		{3, "### Level 3"},
		{4, "#### Level 4"},
		{5, "##### Level 5"},
		{6, "###### Level 6"},
	}
	for _, c := range checks {
		if !strings.Contains(tb.Text, c.want) {
			t.Errorf("output missing heading level %d marker: %q", c.level, c.want)
		}
	}
}

func TestWebFetch_CodeBlocks(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Code</title></head><body>
<pre><code>func main() {
    fmt.Println("hello")
}</code></pre>
<p>Inline <code>code()</code> here.</p>
</body></html>`)
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	result, _ := tool.Call(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	tb := result.Blocks[0].(litellm.TextBlock)

	if !strings.Contains(tb.Text, "```") {
		t.Error("output missing fenced code block markers")
	}
	if !strings.Contains(tb.Text, "func main()") {
		t.Error("output missing code block content")
	}
	if !strings.Contains(tb.Text, "`code()`") {
		t.Errorf("output missing inline code: %q", tb.Text)
	}
}

func TestWebFetch_Links(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Links</title></head><body>
<p>Visit <a href="https://example.com">Example</a> for more.</p>
<p>Also <a href="/relative">relative</a> link.</p>
</body></html>`)
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	result, _ := tool.Call(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	tb := result.Blocks[0].(litellm.TextBlock)

	if !strings.Contains(tb.Text, "[Example](https://example.com)") {
		t.Errorf("output missing converted link: %q", tb.Text)
	}
}

func TestWebFetch_Images(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Images</title></head><body>
<p><img src="https://example.com/pic.png" alt="A picture"> here.</p>
</body></html>`)
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	result, _ := tool.Call(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	tb := result.Blocks[0].(litellm.TextBlock)

	if !strings.Contains(tb.Text, "![A picture](https://example.com/pic.png)") {
		t.Errorf("output missing converted image: %q", tb.Text)
	}
}

func TestWebFetch_Lists(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Lists</title></head><body>
<ul>
<li>Item one</li>
<li>Item two</li>
<li>Item three</li>
</ul>
<ol>
<li>First</li>
<li>Second</li>
</ol>
</body></html>`)
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	result, _ := tool.Call(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	tb := result.Blocks[0].(litellm.TextBlock)

	for _, want := range []string{"- Item one", "- Item two", "- Item three"} {
		if !strings.Contains(tb.Text, want) {
			t.Errorf("output missing list item %q: %q", want, tb.Text)
		}
	}
	if !strings.Contains(tb.Text, "1. First") {
		t.Errorf("output missing ordered list item '1. First': %q", tb.Text)
	}
	if !strings.Contains(tb.Text, "2. Second") {
		t.Errorf("output missing ordered list item '2. Second': %q", tb.Text)
	}
}

func TestWebFetch_FetchLargePage(t *testing.T) {
	t.Parallel()
	largeBody := "<html><body>" + strings.Repeat("<p>content</p>", 10000) + "</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, largeBody)
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	result, err := tool.Call(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks for large page")
	}
}

func TestWebFetch_SchemaHasTimeoutParam(t *testing.T) {
	t.Parallel()
	schema := NewWebFetchTool().JSONSchema()
	var schemaObj map[string]any
	if err := json.Unmarshal(schema, &schemaObj); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, ok := schemaObj["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema missing properties")
	}
	timeoutProp, ok := props["timeout"]
	if !ok {
		t.Fatal("schema missing 'timeout' property")
	}
	timeoutMap, ok := timeoutProp.(map[string]any)
	if !ok {
		t.Fatal("timeout property is not a map")
	}
	if timeoutMap["type"] != "integer" {
		t.Errorf("timeout type = %v, want 'integer'", timeoutMap["type"])
	}
	desc, ok := timeoutMap["description"]
	if !ok || desc == "" {
		t.Error("timeout property missing description")
	}
	// timeout should not be in required array
	required, ok := schemaObj["required"].([]any)
	if ok {
		for _, r := range required {
			if r == "timeout" {
				t.Error("'timeout' should not be in required array (optional)")
			}
		}
	}
}

func TestWebFetch_Timeout(t *testing.T) {
	t.Parallel()
	// Create a slow server that takes longer than timeout
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		fmt.Fprint(w, "<html><body><p>slow response</p></body></html>")
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	// Use timeout of 1 second
	result, err := tool.Call(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`","timeout":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for timeout")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	tb, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block type = %T, want TextBlock", result.Blocks[0])
	}
	if !strings.Contains(tb.Text, "timed out") && !strings.Contains(tb.Text, "timeout") && !strings.Contains(tb.Text, "Timeout") && !strings.Contains(tb.Text, "context deadline") && !strings.Contains(tb.Text, "canceled") && !strings.Contains(tb.Text, "Canceled") {
		t.Errorf("output should mention timeout, got: %q", tb.Text)
	}
}

func TestWebFetch_ContentCap(t *testing.T) {
	t.Parallel()
	// Generate HTML with enough content to exceed 32 KiB
	var body strings.Builder
	body.WriteString("<html><head><title>Large Page</title></head><body>")
	// Each paragraph is ~100 bytes, need > 328 paragraphs for > 32K
	for i := 0; i < 500; i++ {
		body.WriteString(fmt.Sprintf("<p>Paragraph %d: %s</p>", i, strings.Repeat("content ", 15)))
	}
	body.WriteString("</body></html>")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body.String())
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	result, err := tool.Call(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	tb, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block type = %T, want TextBlock", result.Blocks[0])
	}
	// Check truncation marker present
	if !strings.Contains(tb.Text, "truncated at 32 KiB") {
		t.Errorf("output missing truncation marker, length=%d, suffix=%q", len(tb.Text), tb.Text[max(0, len(tb.Text)-100):])
	}
	// Check total size is roughly 32 KiB + marker overhead
	if len(tb.Text) > 40*1024 {
		t.Errorf("output too long: %d bytes, want <= ~40 KiB", len(tb.Text))
	}
}

func TestWebFetch_ContentCapNoMidElement(t *testing.T) {
	t.Parallel()
	// Generate HTML with large paragraphs followed by a distinct marker
	var body strings.Builder
	body.WriteString("<html><head><title>Cap Test</title></head><body>")
	for i := 0; i < 100; i++ {
		body.WriteString(fmt.Sprintf("<p>P%d: %s</p>", i, strings.Repeat("word ", 80)))
	}
	// Add a unique final paragraph that should NOT appear if truncated at element boundary
	body.WriteString("<p>FINAL_MARKER_SHOULD_NOT_APPEAR</p>")
	body.WriteString("</body></html>")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body.String())
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	result, err := tool.Call(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	tb := result.Blocks[0].(litellm.TextBlock)
	// The truncation should not leave partial content - FINAL_MARKER should not be present
	// and output should be a valid truncation
	if strings.Contains(tb.Text, "FINAL_MARKER_SHOULD_NOT_APPEAR") {
		t.Error("output contains final marker that should have been truncated")
	}
}

func TestWebFetch_Proxy(t *testing.T) {
	// Test that proxyFromEnv reads HTTP_PROXY env var correctly.
	// We test with a non-loopback URL (example.com) because the vendored
	// httpproxy package bypasses proxy for loopback addresses.

	// Set HTTP_PROXY to a known proxy URL
	t.Setenv("HTTP_PROXY", "http://proxy.example.com:8080")

	// Create a request to a non-loopback URL
	req, err := http.NewRequest("GET", "http://example.com/page", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	proxyURL, err := proxyFromEnv(req)
	if err != nil {
		t.Fatalf("proxyFromEnv error: %v", err)
	}
	if proxyURL == nil {
		t.Fatal("proxyFromEnv returned nil, want proxy URL")
	}
	if proxyURL.String() != "http://proxy.example.com:8080" {
		t.Errorf("proxy URL = %q, want %q", proxyURL.String(), "http://proxy.example.com:8080")
	}

	// Clear HTTP_PROXY and verify proxy is not used
	t.Setenv("HTTP_PROXY", "")
	proxyURL, err = proxyFromEnv(req)
	if err != nil {
		t.Fatalf("proxyFromEnv error after clearing: %v", err)
	}
	if proxyURL != nil {
		t.Errorf("proxyFromEnv = %v, want nil after clearing HTTP_PROXY", proxyURL)
	}

	// Verify the description mentions proxy support
	tool := NewWebFetchTool()
	if !strings.Contains(tool.Description(), "proxy") {
		t.Error("Description should mention proxy support")
	}
}

func TestWebFetch_HTTPServerError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "internal error")
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	result, err := tool.Call(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true for HTTP 500")
	}
}

// --- htmlToMarkdown unit tests ---

func TestHTMLToMarkdown_Headings(t *testing.T) {
	t.Parallel()
	html := `<h1>H1</h1><h2>H2</h2><h3>H3</h3>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	result := htmlToMarkdown(doc.Find("body"))
	if !strings.Contains(result, "# H1") {
		t.Errorf("h1 not converted: %q", result)
	}
	if !strings.Contains(result, "## H2") {
		t.Errorf("h2 not converted: %q", result)
	}
	if !strings.Contains(result, "### H3") {
		t.Errorf("h3 not converted: %q", result)
	}
}

func TestHTMLToMarkdown_CodeBlock(t *testing.T) {
	t.Parallel()
	html := `<pre><code>fmt.Println("hi")</code></pre>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	result := htmlToMarkdown(doc.Find("body"))
	if !strings.Contains(result, "```") {
		t.Errorf("code fence missing: %q", result)
	}
	if !strings.Contains(result, `fmt.Println("hi")`) {
		t.Errorf("code content missing: %q", result)
	}
}

func TestHTMLToMarkdown_Link(t *testing.T) {
	t.Parallel()
	html := `<a href="https://go.dev">Go</a>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	result := htmlToMarkdown(doc.Find("body"))
	want := "[Go](https://go.dev)"
	if !strings.Contains(result, want) {
		t.Errorf("link not converted, got: %q", result)
	}
}

func TestHTMLToMarkdown_Image(t *testing.T) {
	t.Parallel()
	html := `<img src="https://example.com/pic.png" alt="Photo">`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	result := htmlToMarkdown(doc.Find("body"))
	want := "![Photo](https://example.com/pic.png)"
	if !strings.Contains(result, want) {
		t.Errorf("image not converted, got: %q", result)
	}
}

func TestHTMLToMarkdown_UnorderedList(t *testing.T) {
	t.Parallel()
	html := `<ul><li>A</li><li>B</li><li>C</li></ul>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	result := htmlToMarkdown(doc.Find("body"))
	for _, item := range []string{"- A", "- B", "- C"} {
		if !strings.Contains(result, item) {
			t.Errorf("missing list item %q: %q", item, result)
		}
	}
}

func TestHTMLToMarkdown_OrderedList(t *testing.T) {
	t.Parallel()
	html := `<ol><li>First</li><li>Second</li><li>Third</li></ol>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	result := htmlToMarkdown(doc.Find("body"))
	if !strings.Contains(result, "1. First") {
		t.Errorf("missing '1. First': %q", result)
	}
	if !strings.Contains(result, "2. Second") {
		t.Errorf("missing '2. Second': %q", result)
	}
	if !strings.Contains(result, "3. Third") {
		t.Errorf("missing '3. Third': %q", result)
	}
}

func TestWebFetch_FetchRealURL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	// Quick connectivity check before making real request
	conn, err := net.DialTimeout("tcp", "example.com:80", 2*time.Second)
	if err != nil {
		t.Skipf("network not available (cannot reach example.com:80): %v", err)
	}
	conn.Close()

	tool := NewWebFetchTool()
	result, err := tool.Call(context.Background(), json.RawMessage(`{"url":"https://example.com","timeout":5}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("result.IsError = true, want false for example.com")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	tb, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block type = %T, want TextBlock", result.Blocks[0])
	}
	if !strings.Contains(tb.Text, "Example Domain") {
		t.Errorf("output missing title 'Example Domain': %q", tb.Text)
	}
	if !strings.Contains(tb.Text, "https://example.com") {
		t.Errorf("output missing source URL: %q", tb.Text)
	}
	// Verify markdown content is present (heading, paragraphs)
	if !strings.Contains(tb.Text, "#") && !strings.Contains(tb.Text, "Example") {
		t.Errorf("output missing markdown content: %q", tb.Text)
	}
}

// --- truncateContent unit tests ---

func TestTruncateContent_UnderCap(t *testing.T) {
	t.Parallel()
	content := "short content"
	got := truncateContent(content)
	if got != content {
		t.Errorf("truncateContent(%q) = %q, want %q", content, got, content)
	}
}

func TestTruncateContent_AtCap(t *testing.T) {
	t.Parallel()
	content := strings.Repeat("a", contentCap)
	got := truncateContent(content)
	if got != content {
		t.Errorf("truncateContent at cap should return unchanged, got len=%d", len(got))
	}
}

func TestTruncateContent_OverCap(t *testing.T) {
	t.Parallel()
	// Build content well over the cap using element boundaries
	content := strings.Repeat("paragraph\n\n", 5000) // ~60 KiB, well over 32 KiB cap
	got := truncateContent(content)
	if len(got) >= len(content) {
		t.Errorf("truncateContent should shorten content, got len=%d >= original %d", len(got), len(content))
	}
	if !strings.HasSuffix(got, truncationMsg) {
		t.Errorf("truncateContent should end with truncation message, got ...%q", got[len(got)-50:])
	}
}

func TestTruncateContent_NoElementBoundary(t *testing.T) {
	t.Parallel()
	content := strings.Repeat("a ", contentCap) + "extra content"
	got := truncateContent(content)
	if len(got) >= len(content) {
		t.Errorf("truncateContent should shorten content, got len=%d", len(got))
	}
	if !strings.HasSuffix(got, truncationMsg) {
		t.Errorf("truncateContent should end with truncation message")
	}
}

// --- renderNode / renderInline / renderCodeBlock direct tests ---

func TestRenderCodeBlock(t *testing.T) {
	t.Parallel()
	html := `<pre><code>fmt.Println("hello")</code></pre>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	pre := doc.Find("pre")
	renderCodeBlock(pre.Find("code"), &buf)
	result := buf.String()
	if !strings.Contains(result, "```") {
		t.Errorf("code fence missing: %q", result)
	}
	if !strings.Contains(result, `fmt.Println("hello")`) {
		t.Errorf("code content missing: %q", result)
	}
}

func TestRenderNode_Paragraph(t *testing.T) {
	t.Parallel()
	html := `<p>Hello world</p>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	renderNode(doc.Find("p"), &buf, 0)
	result := buf.String()
	if !strings.Contains(result, "Hello world") {
		t.Errorf("paragraph text missing: %q", result)
	}
}

func TestRenderNode_BoldText(t *testing.T) {
	t.Parallel()
	html := `<strong>important</strong>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	renderNode(doc.Find("strong"), &buf, 0)
	result := buf.String()
	if !strings.Contains(result, "**important**") {
		t.Errorf("bold rendering wrong: %q", result)
	}
}

func TestRenderNode_ItalicText(t *testing.T) {
	t.Parallel()
	html := `<em>emphasis</em>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	renderNode(doc.Find("em"), &buf, 0)
	result := buf.String()
	if !strings.Contains(result, "*emphasis*") {
		t.Errorf("italic rendering wrong: %q", result)
	}
}

func TestRenderNode_Blockquote(t *testing.T) {
	t.Parallel()
	html := `<blockquote>Cited text</blockquote>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	renderNode(doc.Find("blockquote"), &buf, 0)
	result := buf.String()
	if !strings.Contains(result, "> Cited text") {
		t.Errorf("blockquote rendering wrong: %q", result)
	}
}

func TestRenderNode_HorizontalRule(t *testing.T) {
	t.Parallel()
	html := `<hr>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	renderNode(doc.Find("hr"), &buf, 0)
	result := buf.String()
	if !strings.Contains(result, "---") {
		t.Errorf("hr rendering wrong: %q", result)
	}
}

func TestRenderNode_LineBreak(t *testing.T) {
	t.Parallel()
	html := `<br>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	renderNode(doc.Find("br"), &buf, 0)
	result := buf.String()
	if result != "\n" {
		t.Errorf("br rendering wrong: %q", result)
	}
}

func TestRenderInline_SimpleText(t *testing.T) {
	t.Parallel()
	html := `<p>Hello <strong>world</strong></p>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	renderInline(doc.Find("p"), &buf)
	result := buf.String()
	if !strings.Contains(result, "Hello") {
		t.Errorf("inline text missing 'Hello': %q", result)
	}
	if !strings.Contains(result, "**world**") {
		t.Errorf("inline bold missing '**world**': %q", result)
	}
}

func TestRenderInline_InlineCode(t *testing.T) {
	t.Parallel()
	html := `<p>Use <code>fmt.Println()</code> to print</p>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	renderInline(doc.Find("p"), &buf)
	result := buf.String()
	if !strings.Contains(result, "`fmt.Println()`") {
		t.Errorf("inline code missing: %q", result)
	}
}

func TestRenderInline_Link(t *testing.T) {
	t.Parallel()
	html := `<a href="https://go.dev">Go</a>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	renderInline(doc.Find("body"), &buf) // body to get the link children
	result := buf.String()
	if !strings.Contains(result, "[Go](https://go.dev)") {
		t.Errorf("link rendering wrong: %q", result)
	}
}

func TestRenderInline_Image(t *testing.T) {
	t.Parallel()
	html := `<img src="https://example.com/pic.png" alt="Photo">`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	renderInline(doc.Find("body"), &buf)
	result := buf.String()
	if !strings.Contains(result, "![Photo](https://example.com/pic.png)") {
		t.Errorf("image rendering wrong: %q", result)
	}
}

// Test that the contentCap and truncationMsg constants are accessible
func TestContentCap_Constant(t *testing.T) {
	if contentCap <= 0 {
		t.Errorf("contentCap = %d, want > 0", contentCap)
	}
	if truncationMsg == "" {
		t.Error("truncationMsg should not be empty")
	}
	if !strings.HasPrefix(truncationMsg, "\n\n") {
		t.Errorf("truncationMsg should start with double newline, got %q", truncationMsg)
	}
}
