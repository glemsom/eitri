package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/voocel/litellm"
)

// browserArgs defines the JSON schema for the browser tool.
type browserArgs struct {
	Action string          `json:"action" jsonschema:"Action to perform on the browser (list_targets, navigate, get_dom, click, type, screenshot)"`
	Args   json.RawMessage `json:"args,omitempty" jsonschema:"Action-specific JSON parameters"`
}

// navigateArgs defines the JSON schema for the navigate action.
type navigateArgs struct {
	TargetID string `json:"target_id" jsonschema:"Target tab ID to navigate"`
	URL      string `json:"url" jsonschema:"Full URL to navigate to"`
	Timeout  int    `json:"timeout,omitempty" jsonschema:"Navigation timeout in seconds, default 30"`
}

// typeArgs defines the JSON schema for the type action.
type typeArgs struct {
	TargetID string `json:"target_id" jsonschema:"Target tab ID to operate on"`
	Selector string `json:"selector" jsonschema:"CSS selector for the element to type into"`
	Text     string `json:"text" jsonschema:"Text to type into the element"`
}

// screenshotArgs defines the JSON schema for the screenshot action.
type screenshotArgs struct {
	TargetID string `json:"target_id" jsonschema:"Target tab ID to capture screenshot of"`
}

// remoteConnection holds the allocator context and cancel func for one session.
type remoteConnection struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// NativeBrowserTool implements ToolHandler for controlling a remote Chrome
// instance via the Chrome DevTools Protocol.
type NativeBrowserTool struct {
	mu        sync.Mutex
	conns     map[string]remoteConnection // keyed by session ID
	wsURL     string                       // remote Chrome WS URL
	workspace string                       // workspace root for saving files
	schema    litellm.Schema
}

// NewBrowserTool creates a new NativeBrowserTool.
// wsURL is the WebSocket URL of a remote Chrome DevTools Protocol endpoint.
// workspace is the workspace root directory where screenshot files are saved.
// If wsURL is empty, the tool returns a descriptive error asking the user to configure it.
func NewBrowserTool(wsURL, workspace string) *NativeBrowserTool {
	return &NativeBrowserTool{
		conns:     make(map[string]remoteConnection),
		wsURL:     wsURL,
		workspace: workspace,
		schema:    SchemaOf[browserArgs](),
	}
}

func (t *NativeBrowserTool) Name() string {
	return "browser"
}

func (t *NativeBrowserTool) Description() string {
	return "Control a remote Chrome browser via CDP. " +
		"Supports actions: list_targets, navigate, get_dom, click, type, screenshot. " +
		"Requires a configured browser_ws_url (remote Chrome DevTools Protocol WebSocket endpoint)."
}

func (t *NativeBrowserTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *NativeBrowserTool) Call(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var parsed browserArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ToolResult{}, fmt.Errorf("browser: invalid args: %w", err)
	}

	if parsed.Action == "" {
		return ToolError(TextBlocks("Error: 'action' is required (list_targets, navigate, get_dom, click, type, screenshot)")), nil
	}

	// Check WS URL is configured
	if t.wsURL == "" {
		return ToolError(TextBlocks("Error: browser_ws_url is not configured. Set 'browser_ws_url' in your Eitri configuration to a Chrome DevTools Protocol WebSocket URL (e.g. ws://127.0.0.1:9222/devtools/browser/...). " +
			"To get this URL, launch Chrome with --remote-debugging-port=9222 and visit http://127.0.0.1:9222/json/version to find the webSocketDebuggerUrl.")), nil
	}

	sessionID, ok := ctx.Value(SessionIDKey).(string)
	if !ok {
		return ToolResult{}, fmt.Errorf("browser: no session ID in context")
	}

	// Get or create the allocator connection for this session
	allocCtx, err := t.getOrCreateAllocator(sessionID)
	if err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to connect to browser: %v", err))), nil
	}

	switch parsed.Action {
	case "list_targets":
		return t.listTargets(allocCtx)
	case "navigate":
		return t.navigate(allocCtx, parsed.Args)
	case "type":
		return t.typeText(allocCtx, parsed.Args)
	case "screenshot":
		return t.screenshot(allocCtx, parsed.Args)
	default:
		return ToolError(TextBlocks(fmt.Sprintf("Error: unknown action %q. Valid actions: list_targets, navigate, get_dom, click, type, screenshot", parsed.Action))), nil
	}
}

// getOrCreateAllocator returns the allocator context for the given session ID,
// creating a new one via chromedp.NewRemoteAllocator if none exists.
func (t *NativeBrowserTool) getOrCreateAllocator(sessionID string) (context.Context, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if conn, exists := t.conns[sessionID]; exists {
		return conn.ctx, nil
	}

	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), t.wsURL)
	t.conns[sessionID] = remoteConnection{
		ctx:    allocCtx,
		cancel: allocCancel,
	}
	return allocCtx, nil
}

// EndSession closes the allocator connection for the given session ID.
// Called by the agent loop when a session ends.
func (t *NativeBrowserTool) EndSession(sessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if conn, exists := t.conns[sessionID]; exists {
		conn.cancel()
		delete(t.conns, sessionID)
	}
}

// listTargets returns all open browser targets (tabs/pages) with their
// target ID, title, and URL.
func (t *NativeBrowserTool) listTargets(allocCtx context.Context) (ToolResult, error) {
	// Create a new tab context for the operation
	tabCtx, tabCancel := chromedp.NewContext(allocCtx)
	defer tabCancel()

	// List all browser targets via chromedp.Targets
	targets, err := chromedp.Targets(tabCtx)
	if err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to list targets: %v", err))), nil
	}

	type targetInfo struct {
		TargetID target.ID `json:"target_id"`
		Title    string    `json:"title"`
		URL      string    `json:"url"`
	}

	infos := make([]targetInfo, 0, len(targets))
	for _, t := range targets {
		infos = append(infos, targetInfo{
			TargetID: t.TargetID,
			Title:    t.Title,
			URL:      t.URL,
		})
	}

	data, err := json.Marshal(infos)
	if err != nil {
		return ToolResult{}, fmt.Errorf("browser: marshal targets: %w", err)
	}

	return TextResult(string(data)), nil
}

// navigate navigates the specified target tab to a URL and returns
// the final URL, page title, and a brief DOM structural summary.
func (t *NativeBrowserTool) navigate(allocCtx context.Context, rawArgs json.RawMessage) (ToolResult, error) {
	var args navigateArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: invalid navigate action args: %v", err))), nil
	}

	if args.TargetID == "" {
		return ToolError(TextBlocks("Error: 'target_id' is required for navigate action")), nil
	}
	if args.URL == "" {
		return ToolError(TextBlocks("Error: 'url' is required for navigate action")), nil
	}

	// Basic URL validation: must have a scheme
	if !strings.HasPrefix(args.URL, "http://") && !strings.HasPrefix(args.URL, "https://") {
		return ToolError(TextBlocks(fmt.Sprintf("Error: invalid URL %q. URL must start with http:// or https://", args.URL))), nil
	}

	timeout := args.Timeout
	if timeout <= 0 {
		timeout = 30
	}

	// Attach to the target tab with a timeout context
	tabCtx, tabCancel := chromedp.NewContext(allocCtx, chromedp.WithTargetID(target.ID(args.TargetID)))
	defer tabCancel()

	// Apply timeout for navigation
	navCtx, navCancel := context.WithTimeout(tabCtx, time.Duration(timeout)*time.Second)
	defer navCancel()

	// Navigate and wait for DOMContentLoaded
	var finalURL string
	var pageTitle string
	var bodyHTML string

	if err := chromedp.Run(navCtx,
		chromedp.Navigate(args.URL),
		chromedp.WaitReady("body"),
		chromedp.Location(&finalURL),
		chromedp.Title(&pageTitle),
		chromedp.OuterHTML("body", &bodyHTML),
	); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return ToolError(TextBlocks(fmt.Sprintf("Error: page did not finish loading within %ds", timeout))), nil
		}
		return ToolError(TextBlocks(fmt.Sprintf("Error: navigation failed: %v", err))), nil
	}

	// Build a brief DOM structural summary
	summary := buildDOMSummary(bodyHTML)

	result := fmt.Sprintf("Navigation successful\n\nFinal URL: %s\nTitle: %s\n\n%s", finalURL, pageTitle, summary)
	return TextResult(result), nil
}

// typeText types text into an element identified by CSS selector.
func (t *NativeBrowserTool) typeText(allocCtx context.Context, rawArgs json.RawMessage) (ToolResult, error) {
	var args typeArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: invalid type action args: %v", err))), nil
	}

	if args.TargetID == "" {
		return ToolError(TextBlocks("Error: 'target_id' is required for type action")), nil
	}
	if args.Selector == "" {
		return ToolError(TextBlocks("Error: 'selector' is required for type action")), nil
	}

	// Empty text is a no-op
	if args.Text == "" {
		return TextResult("No text provided, skipped typing"), nil
	}

	// Attach to the target tab
	tabCtx, tabCancel := chromedp.NewContext(allocCtx, chromedp.WithTargetID(target.ID(args.TargetID)))
	defer tabCancel()

	// Run the type sequence: wait for element, clear existing value, then type
	if err := chromedp.Run(tabCtx,
		chromedp.WaitVisible(args.Selector, chromedp.ByQuery),
		chromedp.Clear(args.Selector, chromedp.ByQuery),
		chromedp.SendKeys(args.Selector, args.Text, chromedp.ByQuery),
	); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to type into element matching selector %q: %v", args.Selector, err))), nil
	}

	return TextResult(fmt.Sprintf("Typed text into element matching selector %q", args.Selector)), nil
}

// screenshot captures a screenshot of the specified target tab's viewport.
// It saves the PNG to the workspace root with a timestamped filename and
// returns both a text message and the image data for vision-capable models.
func (t *NativeBrowserTool) screenshot(allocCtx context.Context, rawArgs json.RawMessage) (ToolResult, error) {
	var args screenshotArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: invalid screenshot action args: %v", err))), nil
	}

	if args.TargetID == "" {
		return ToolError(TextBlocks("Error: 'target_id' is required for screenshot action")), nil
	}

	// Attach to the target tab
	tabCtx, tabCancel := chromedp.NewContext(allocCtx, chromedp.WithTargetID(target.ID(args.TargetID)))
	defer tabCancel()

	// Capture the screenshot (viewport only)
	var pngData []byte
	if err := chromedp.Run(tabCtx,
		chromedp.CaptureScreenshot(&pngData),
	); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: screenshot failed: %v", err))), nil
	}

	if len(pngData) == 0 {
		return ToolError(TextBlocks("Error: screenshot returned empty data")), nil
	}

	// Save to workspace root
	timestamp := time.Now().UnixNano()
	filename := fmt.Sprintf("browser-screenshot-%d.png", timestamp)
	filePath := filepath.Join(t.workspace, filename)

	if err := os.WriteFile(filePath, pngData, 0644); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to save screenshot to %s: %v", filePath, err))), nil
	}

	// Return both a text message and the image data
	return Success([]litellm.Block{
		litellm.TextBlock{Text: fmt.Sprintf("Screenshot saved to %s", filename)},
		litellm.ImageBlock{Data: pngData, MIME: "image/png"},
	}), nil
}

// buildDOMSummary produces a concise structural summary of page HTML content.
// It returns headings (h1-h6), link count, input/button count, and paragraph text snippets.
func buildDOMSummary(bodyHTML string) string {
	var summary strings.Builder

	// Extract and count headings using separate regexps for each level
	for level := 1; level <= 6; level++ {
		re := regexp.MustCompile(fmt.Sprintf(`<h%d[^>]*>(.*?)</h%d>`, level, level))
		matches := re.FindAllStringSubmatch(bodyHTML, -1)
		for _, m := range matches {
			text := stripTags(m[1])
			if text != "" {
				if summary.Len() == 0 {
					summary.WriteString("Page structure:\n")
				}
				summary.WriteString(fmt.Sprintf("  h%d: %s\n", level, text))
			}
		}
	}

	// Count links
	linkCount := strings.Count(bodyHTML, "<a ")
	if linkCount > 0 {
		summary.WriteString(fmt.Sprintf("\nLinks: %d\n", linkCount))
	}

	// Count inputs and buttons
	inputCount := strings.Count(bodyHTML, "<input ")
	buttonCount := strings.Count(bodyHTML, "<button")
	if inputCount > 0 {
		summary.WriteString(fmt.Sprintf("Inputs: %d\n", inputCount))
	}
	if buttonCount > 0 {
		summary.WriteString(fmt.Sprintf("Buttons: %d\n", buttonCount))
	}

	if summary.Len() == 0 {
		summary.WriteString("No significant DOM elements found.")
	}

	return summary.String()
}

// stripTags removes HTML tags from a string.
func stripTags(s string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	return strings.TrimSpace(re.ReplaceAllString(s, ""))
}
