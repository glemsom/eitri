package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/voocel/litellm"
)

type webFetchArgs struct {
	URL string `json:"url" jsonschema:"URL to fetch and extract text from"`
}

// WebFetchTool implements ToolHandler for fetching web pages as raw text.
type WebFetchTool struct {
	schema litellm.Schema
	client *http.Client
}

// NewWebFetchTool creates a new WebFetchTool with the default HTTP client.
func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{
		schema: SchemaOf[webFetchArgs](),
		client: http.DefaultClient,
	}
}

func (t *WebFetchTool) Name() string {
	return "web_fetch"
}

func (t *WebFetchTool) Description() string {
	return "Fetch a web page and return its text content. The response is raw text with HTML tags stripped. Use this to read documentation, articles, or any web-accessible content."
}

func (t *WebFetchTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *WebFetchTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed webFetchArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("web_fetch: invalid args: %w", err), false
	}

	if parsed.URL == "" {
		return textBlocks("Error: 'url' field is required and must be non-empty"), nil, true
	}

	parsedURL, err := url.ParseRequestURI(parsed.URL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return textBlocks(fmt.Sprintf("Error: invalid URL %q — must be a valid http or https URL", parsed.URL)), nil, true
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("web_fetch: create request: %w", err), false
	}

	req.Header.Set("User-Agent", "Eitri/1.0")
	resp, err := t.client.Do(req)
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: request failed: %v", err)), nil, true
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: reading response body: %v", err)), nil, true
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return textBlocks(fmt.Sprintf("Error: HTTP %d — %s", resp.StatusCode, strings.TrimSpace(string(body)))), nil, true
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: parsing HTML: %v", err)), nil, true
	}

	text := doc.Find("body").Text()
	if text == "" {
		text = doc.Text()
	}

	// Clean up whitespace
	text = strings.TrimSpace(text)
	if text == "" {
		return textBlocks("(empty page)"), nil, false
	}

	return textBlocks(text), nil, false
}
