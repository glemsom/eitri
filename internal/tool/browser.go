package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/voocel/litellm"
)

// browserArgs defines the JSON schema for the browser tool.
type browserArgs struct {
	Action string          `json:"action" jsonschema:"Action to perform on the browser (list_targets, navigate, get_dom, click, type, screenshot)"`
	Args   json.RawMessage `json:"args,omitempty" jsonschema:"Action-specific JSON parameters"`
}

// remoteConnection holds the allocator context and cancel func for one session.
type remoteConnection struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// NativeBrowserTool implements ToolHandler for controlling a remote Chrome
// instance via the Chrome DevTools Protocol.
type NativeBrowserTool struct {
	mu       sync.Mutex
	conns    map[string]remoteConnection // keyed by session ID
	wsURL    string                       // remote Chrome WS URL
	schema   litellm.Schema
}

// NewBrowserTool creates a new NativeBrowserTool.
// wsURL is the WebSocket URL of a remote Chrome DevTools Protocol endpoint.
// If empty, the tool returns a descriptive error asking the user to configure it.
func NewBrowserTool(wsURL string) *NativeBrowserTool {
	return &NativeBrowserTool{
		conns:  make(map[string]remoteConnection),
		wsURL:  wsURL,
		schema: SchemaOf[browserArgs](),
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
