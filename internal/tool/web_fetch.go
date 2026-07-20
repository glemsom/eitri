package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/voocel/litellm"
	"golang.org/x/net/html"
	"golang.org/x/net/http/httpproxy"
)

type webFetchArgs struct {
	URL     string `json:"url" jsonschema:"URL to fetch and extract text from"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"Timeout in seconds (default 15)"`
}

// WebFetchTool implements ToolHandler for fetching web pages as raw text.
type WebFetchTool struct {
	schema litellm.Schema
}

// NewWebFetchTool creates a new WebFetchTool.
func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{
		schema: SchemaOf[webFetchArgs](),
	}
}

func (t *WebFetchTool) Name() string {
	return "web_fetch"
}

func (t *WebFetchTool) Description() string {
	return "Fetch a web page and convert to Markdown. Returns title, source URL, and body. Configurable timeout (default 15s), 32 KiB cap, proxy support. For docs, articles, or web content."
}

func (t *WebFetchTool) JSONSchema() litellm.Schema {
	return t.schema
}

const (
	// contentCap is the maximum size of extracted Markdown content (32 KiB).
	contentCap = 32 * 1024
	// truncationMsg is appended when content is truncated.
	truncationMsg = "\n\n[Content truncated at 32 KiB — use a more specific URL or section]"
)

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

	// Build request with context for cancellation
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("web_fetch: create request: %w", err), false
	}

	req.Header.Set("User-Agent", "Eitri/1.0")

	// Create client with timeout and proxy from environment
	timeout := parsed.Timeout
	if timeout <= 0 {
		timeout = 15 // default 15 seconds
	}
	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
		Transport: &http.Transport{
			Proxy: proxyFromEnv,
		},
	}

	resp, err := client.Do(req)
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

	// Extract title and source URL
	pageTitle := ""
	if doc.Find("title").Length() > 0 {
		pageTitle = strings.TrimSpace(doc.Find("title").Text())
	}

	// Remove unwanted elements
	doc.Find("script, style, nav, footer, header, aside").Remove()

	// Convert body to Markdown
	markdown := htmlToMarkdown(doc.Find("body"))
	markdown = strings.TrimSpace(markdown)

	// Build structured output
	var output strings.Builder
	if pageTitle != "" {
		output.WriteString("Title: ")
		output.WriteString(pageTitle)
		output.WriteString("\n")
	}
	output.WriteString("Source: ")
	output.WriteString(parsed.URL)
	output.WriteString("\n\n")
	output.WriteString(markdown)

	result := strings.TrimSpace(output.String())
	if result == "" {
		return textBlocks("(empty page)"), nil, false
	}

	// Apply content cap with element-boundary truncation
	result = truncateContent(result)

	return textBlocks(result), nil, false
}

// proxyFromEnv reads proxy configuration from environment variables on every call,
// unlike http.ProxyFromEnvironment which caches at process startup.
func proxyFromEnv(req *http.Request) (*url.URL, error) {
	return httpproxy.FromEnvironment().ProxyFunc()(req.URL)
}

// truncateContent truncates content at ~32 KiB, breaking at element boundaries
// (defined as double-newline separators between Markdown block elements).
func truncateContent(content string) string {
	if len(content) <= contentCap {
		return content
	}

	// Find the last element boundary (double newline) before the cap
	truncateAt := strings.LastIndex(content[:contentCap], "\n\n")
	if truncateAt < 0 {
		// No element boundary found, truncate at exact cap (but try last newline)
		truncateAt = strings.LastIndex(content[:contentCap], "\n")
		if truncateAt < 0 {
			truncateAt = contentCap
		}
	}

	// Ensure we don't truncate in the middle of the title/source header
	if truncateAt < 30 {
		truncateAt = contentCap
	}

	return content[:truncateAt] + truncationMsg
}

// htmlToMarkdown converts an HTML selection to Markdown text.
// It traverses the DOM tree and handles common block and inline elements.
func htmlToMarkdown(sel *goquery.Selection) string {
	var result strings.Builder
	sel.Contents().Each(func(i int, child *goquery.Selection) {
		renderNode(child, &result, 0)
	})
	return result.String()
}

// renderNode renders a single DOM node (and its children) to Markdown.
func renderNode(sel *goquery.Selection, buf *strings.Builder, depth int) {
	node := sel.Get(0)
	if node == nil {
		return
	}

	switch node.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(node.Data[1] - '0')
		buf.WriteString("\n\n")
		buf.WriteString(strings.Repeat("#", level))
		buf.WriteString(" ")
		renderInline(sel, buf)
		buf.WriteString("\n\n")

	case "p":
		buf.WriteString("\n\n")
		renderInline(sel, buf)
		buf.WriteString("\n\n")

	case "pre":
		code := sel.Find("code")
		if code.Length() > 0 {
			renderCodeBlock(code, buf)
		} else {
			renderCodeBlock(sel, buf)
		}

	case "code":
		// Check if parent is <pre> — if so, skip (handled by pre case)
		if sel.Parent().Is("pre") {
			return
		}
		// Inline code
		text := sel.Text()
		text = strings.TrimSpace(text)
		if text != "" {
			buf.WriteString("`")
			buf.WriteString(text)
			buf.WriteString("`")
		}

	case "ul":
		buf.WriteString("\n\n")
		sel.Children().Each(func(i int, li *goquery.Selection) {
			if li.Is("li") {
				buf.WriteString(strings.Repeat("  ", depth))
				buf.WriteString("- ")
				renderInline(li, buf)
				buf.WriteString("\n")
			}
		})
		buf.WriteString("\n")

	case "ol":
		buf.WriteString("\n\n")
		sel.Children().Each(func(i int, li *goquery.Selection) {
			if li.Is("li") {
				buf.WriteString(strings.Repeat("  ", depth))
				buf.WriteString(fmt.Sprintf("%d. ", i+1))
				renderInline(li, buf)
				buf.WriteString("\n")
			}
		})
		buf.WriteString("\n")

	case "li":
		// Handled by ul/ol parents above; if li appears standalone render as unordered
		buf.WriteString(strings.Repeat("  ", depth))
		buf.WriteString("- ")
		renderInline(sel, buf)
		buf.WriteString("\n")

	case "a":
		href := ""
		if h, exists := sel.Attr("href"); exists {
			href = h
		}
		text := sel.Text()
		if text == "" {
			text = href
		}
		if href != "" && href != text {
			buf.WriteString("[")
			buf.WriteString(text)
			buf.WriteString("](")
			buf.WriteString(href)
			buf.WriteString(")")
		} else {
			buf.WriteString(text)
		}

	case "img":
		src, _ := sel.Attr("src")
		alt, _ := sel.Attr("alt")
		if src != "" {
			buf.WriteString("![")
			buf.WriteString(alt)
			buf.WriteString("](")
			buf.WriteString(src)
			buf.WriteString(")")
		}

	case "br":
		buf.WriteString("\n")

	case "hr":
		buf.WriteString("\n\n---\n\n")

	case "blockquote":
		buf.WriteString("\n\n")
		text := sel.Text()
		text = strings.TrimSpace(text)
		lines := strings.Split(text, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				buf.WriteString("> ")
				buf.WriteString(trimmed)
				buf.WriteString("\n")
			}
		}
		buf.WriteString("\n")

	case "strong", "b":
		text := sel.Text()
		text = strings.TrimSpace(text)
		if text != "" {
			buf.WriteString("**")
			buf.WriteString(text)
			buf.WriteString("**")
		}

	case "em", "i":
		text := sel.Text()
		text = strings.TrimSpace(text)
		if text != "" {
			buf.WriteString("*")
			buf.WriteString(text)
			buf.WriteString("*")
		}

	case "table":
		// Skip tables in v1 — just inline text
		buf.WriteString("\n\n")
		renderInline(sel, buf)
		buf.WriteString("\n\n")

	default:
		// For text nodes and other inline elements, render inline
		if node.Type == html.TextNode {
			text := node.Data
			text = strings.TrimSpace(text)
			if text != "" {
				buf.WriteString(text)
				buf.WriteString(" ")
			}
			return
		}
		// For unknown elements, just render children
		renderInline(sel, buf)
	}
}

// renderInline renders the text content of a selection for inline use (no block-level formatting).
func renderInline(sel *goquery.Selection, buf *strings.Builder) {
	sel.Contents().Each(func(i int, child *goquery.Selection) {
		node := child.Get(0)
		if node == nil {
			return
		}
		if node.Type == html.TextNode {
			text := node.Data
			text = strings.TrimSpace(text)
			if text != "" {
				buf.WriteString(text)
				buf.WriteString(" ")
			}
			return
		}

		switch node.Data {
		case "a":
			href, _ := child.Attr("href")
			text := child.Text()
			if text == "" {
				text = href
			}
			if href != "" && href != text {
				buf.WriteString("[")
				buf.WriteString(text)
				buf.WriteString("](")
				buf.WriteString(href)
				buf.WriteString(")")
			} else {
				buf.WriteString(text)
			}
		case "img":
			src, _ := child.Attr("src")
			alt, _ := child.Attr("alt")
			if src != "" {
				buf.WriteString("![")
				buf.WriteString(alt)
				buf.WriteString("](")
				buf.WriteString(src)
				buf.WriteString(")")
			}
		case "code":
			text := child.Text()
			text = strings.TrimSpace(text)
			if text != "" {
				buf.WriteString("`")
				buf.WriteString(text)
				buf.WriteString("`")
			}
		case "strong", "b":
			text := child.Text()
			text = strings.TrimSpace(text)
			if text != "" {
				buf.WriteString("**")
				buf.WriteString(text)
				buf.WriteString("**")
			}
		case "em", "i":
			text := child.Text()
			text = strings.TrimSpace(text)
			if text != "" {
				buf.WriteString("*")
				buf.WriteString(text)
				buf.WriteString("*")
			}
		case "br":
			buf.WriteString("\n")
		default:
			renderInline(child, buf)
		}
	})
}

// renderCodeBlock renders a <pre><code> or <code> block as a fenced code block.
func renderCodeBlock(sel *goquery.Selection, buf *strings.Builder) {
	buf.WriteString("\n\n```\n")
	text := sel.Text()
	// Remove trailing newline for cleaner output
	text = strings.TrimRight(text, "\n")
	buf.WriteString(text)
	buf.WriteString("\n```\n\n")
}
