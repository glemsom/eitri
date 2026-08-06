package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/voocel/litellm"
)

// browserArgs is the on-wire envelope for the browser tool. The schema exposed
// to the model is a discriminated union built by buildBrowserSchema, so the
// action-specific parameters (the args blob) are typed per action rather than
// a free-form object.
type browserArgs struct {
	Action string          `json:"action"`
	Args   json.RawMessage `json:"args,omitempty"`
}

// browserActions is the canonical list of valid browser tool actions, in
// dispatch order. It feeds both the schema's action enum and the args
// discriminated union.
var browserActions = []string{
	"list_targets",
	"navigate",
	"get_dom",
	"click",
	"type",
	"screenshot",
	"new_tab",
	"close_tab",
	"select",
	"get_value",
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

// getDOMArgs defines the JSON schema for the get_dom action.
type getDOMArgs struct {
	TargetID string `json:"target_id" jsonschema:"Target tab ID to get DOM from"`
	Selector string `json:"selector,omitempty" jsonschema:"Optional CSS selector to get outerHTML of a specific element"`
}

// clickArgs defines the JSON schema for the click action.
type clickArgs struct {
	TargetID string `json:"target_id" jsonschema:"Target tab ID to click in"`
	Selector string `json:"selector" jsonschema:"CSS selector for the element to click"`
}

// newTabArgs defines the JSON schema for the new_tab action. It takes no
// arguments — the browser tool opens a fresh tab.
type newTabArgs struct{}

// closeTabArgs defines the JSON schema for the close_tab action.
type closeTabArgs struct {
	TargetID string `json:"target_id" jsonschema:"Target tab ID to close"`
}

// selectArgs defines the JSON schema for the select action.
type selectArgs struct {
	TargetID string `json:"target_id" jsonschema:"Target tab ID to operate on"`
	Selector string `json:"selector" jsonschema:"CSS selector for the <select> element"`
	Value    string `json:"value" jsonschema:"Option value to select"`
}

// getValueArgs defines the JSON schema for the get_value action.
type getValueArgs struct {
	TargetID string `json:"target_id" jsonschema:"Target tab ID to operate on"`
	Selector string `json:"selector" jsonschema:"CSS selector for the element to read the value of"`
}

// listTargetsArgs is the schema for the list_targets action, which takes no
// arguments. It exists so every action has a typed schema entry in the args
// discriminated union; an empty object schema accepts the empty/missing args
// the action already tolerates.
type listTargetsArgs struct{}

// actionSchema returns the typed object schema for a single browser action
// from its args struct, for use as one branch of the args discriminated union.
func actionSchema[T any]() JSONSchema {
	var js JSONSchema
	if err := json.Unmarshal(SchemaOf[T](), &js); err != nil {
		// SchemaOf is compile-time typed; this only fails on a builder bug.
		panic(fmt.Sprintf("browser: parse action schema: %v", err))
	}
	return js
}

// buildBrowserSchema builds the model-facing schema for the browser tool.
//
// Instead of exposing a free-form args blob, each browser action gets its own
// typed object schema: the args property is a discriminated union (oneOf) whose
// branches carry the exact required parameters for each action, and the action
// property is an enum of the valid actions. The on-wire envelope
// {"action": ..., "args": {...}} is unchanged.
func buildBrowserSchema() (litellm.Schema, error) {
	argsDesc := "Action-specific parameters. The required fields depend on 'action' — each action has its own typed schema: " +
		"list_targets (none), navigate {target_id, url, timeout?}, get_dom {target_id, selector?} (selector mode capped at 32k chars; " +
		"structural summary capped at 24k chars), click {target_id, selector}, " +
		"type {target_id, selector, text}, screenshot {target_id}, new_tab (none), close_tab {target_id}, select {target_id, selector, value}, " +
		"get_value {target_id, selector}. The selector field is called 'selector', not 'query'."

	schema := JSONSchema{
		Type: "object",
		Properties: map[string]SchemaProp{
			"action": {
				Type:        "string",
				Description: "Action to perform on the browser",
				Enum:        browserActions,
			},
			"args": {
				Description: argsDesc,
				OneOf: []JSONSchema{
					actionSchema[listTargetsArgs](),
					actionSchema[navigateArgs](),
					actionSchema[getDOMArgs](),
					actionSchema[clickArgs](),
					actionSchema[typeArgs](),
					actionSchema[screenshotArgs](),
					actionSchema[newTabArgs](),
					actionSchema[closeTabArgs](),
					actionSchema[selectArgs](),
					actionSchema[getValueArgs](),
				},
			},
		},
		Required: []string{"action"},
	}

	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("browser: marshal schema: %w", err)
	}
	return litellm.Schema(raw), nil
}

// domElement represents a single DOM element in the structural summary.
type domElement struct {
	Type        string `json:"type"`
	Level       string `json:"level,omitempty"`
	Text        string `json:"text,omitempty"`
	Href        string `json:"href,omitempty"`
	InputType   string `json:"input_type,omitempty"`
	Value       string `json:"value,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Name        string `json:"name,omitempty"`
	Selector    string `json:"selector"`
}

// remoteConnection holds the allocator context and cancel func for one session.
type remoteConnection struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// targetContext holds a context attached to a specific browser tab (target).
// The context is cached so we can send multiple commands to the same tab
// without closing it between operations.
type targetContext struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// NativeBrowserTool implements ToolHandler for controlling a remote Chrome
// instance via the Chrome DevTools Protocol.
type NativeBrowserTool struct {
	mu        sync.Mutex
	conns     map[string]remoteConnection // keyed by session ID
	wsURL     string                      // remote Chrome WS URL
	workspace string                      // workspace root for saving files
	schema    litellm.Schema

	// actionTimeout bounds browser target operations that have no other
	// explicit timeout (type, get_dom, screenshot, and the click init
	// handshake). It prevents a hung CDP connection from blocking the agent
	// loop indefinitely — the reported failure mode where a browser tool call
	// ran for many minutes without returning.
	actionTimeout time.Duration

	targetsMu sync.Mutex
	targets   map[string]map[string]*targetContext // sessionID -> targetID -> cached context
}

// NewBrowserTool creates a new NativeBrowserTool.
// wsURL is the WebSocket URL of a remote Chrome DevTools Protocol endpoint.
// workspace is the workspace root directory where screenshot files are saved.
// If wsURL is empty, the tool returns a descriptive error asking the user to configure it.
func NewBrowserTool(wsURL, workspace string) *NativeBrowserTool {
	schema, err := buildBrowserSchema()
	if err != nil {
		// Schema construction is fully deterministic; only fails on a bug.
		panic(err)
	}
	return &NativeBrowserTool{
		conns:         make(map[string]remoteConnection),
		targets:       make(map[string]map[string]*targetContext),
		wsURL:         wsURL,
		workspace:     workspace,
		actionTimeout: 30 * time.Second,
		schema:        schema,
	}
}

func (t *NativeBrowserTool) Name() string {
	return "browser"
}

func (t *NativeBrowserTool) Description() string {
	return "Control a remote Chrome browser via CDP. " +
		"Supports actions: " + strings.Join(browserActions, ", ") + ". " +
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
		return ToolError(TextBlocks("Error: 'action' is required (" + strings.Join(browserActions, ", ") + ")")), nil
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
		return t.navigate(allocCtx, sessionID, parsed.Args)
	case "type":
		return t.typeText(allocCtx, sessionID, parsed.Args)
	case "screenshot":
		return t.screenshot(allocCtx, sessionID, parsed.Args)
	case "get_dom":
		return t.getDOM(allocCtx, sessionID, parsed.Args)
	case "click":
		return t.click(allocCtx, sessionID, parsed.Args)
	case "new_tab":
		return t.newTab(allocCtx, sessionID, parsed.Args)
	case "close_tab":
		return t.closeTab(allocCtx, sessionID, parsed.Args)
	case "select":
		return t.selectOption(allocCtx, sessionID, parsed.Args)
	case "get_value":
		return t.getValue(allocCtx, sessionID, parsed.Args)
	default:
		return ToolError(TextBlocks(fmt.Sprintf("Error: unknown action %q. Valid actions: %s", parsed.Action, strings.Join(browserActions, ", ")))), nil
	}
}

// resolveWebSocketURL resolves a user-provided WebSocket URL to a form suitable
// for chromedp.NewRemoteAllocator.
//
// It tries two strategies in order:
//  1. If the URL already contains "/devtools/browser/", it is used as-is.
//  2. Otherwise, it attempts auto-discovery by fetching http://host:port/json/version
//     and extracting the webSocketDebuggerUrl from the JSON response.
//  3. If auto-discovery fails, it falls back to ws://host:port/devtools/browser/,
//     which works with Chrome instances that serve WebSocket connections directly
//     at that path without a browser GUID (e.g. sandboxed Chrome).
func (t *NativeBrowserTool) resolveWebSocketURL(ctx context.Context, rawURL string) (string, error) {
	// If the URL already points to a browser websocket endpoint, use it directly.
	if strings.Contains(rawURL, "/devtools/browser/") {
		return rawURL, nil
	}

	// Try standard auto-discovery via /json/version (same approach as chromedp's modifyURL).
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("browser: invalid wsURL %q: %w", rawURL, err)
	}
	u.Scheme = "http"
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		// If there's no port, assume the host is just a hostname and use default port 9222
		host = u.Host
		port = "9222"
	}
	u.Host = net.JoinHostPort(host, port)
	u.Path = "/json/version"

	discCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(discCtx, "GET", u.String(), nil)
	if err != nil {
		// Can't even build the request — fall through to fallback
	}
	if err == nil {
		resp, reqErr := http.DefaultClient.Do(req)
		if reqErr == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var versionInfo map[string]any
				if decodeErr := json.NewDecoder(resp.Body).Decode(&versionInfo); decodeErr == nil {
					if wsURL, ok := versionInfo["webSocketDebuggerUrl"].(string); ok && wsURL != "" {
						return wsURL, nil
					}
				}
			}
		}
	}

	// Fallback: some Chrome instances (e.g. sandboxed/bwrap) serve WebSocket at
	// /devtools/browser/ without requiring a browser GUID and without serving
	// the HTTP /json/version endpoint. Try that.
	u2, _ := url.Parse(rawURL)
	u2.Path = "/devtools/browser/"
	return u2.String(), nil
}

// getOrCreateAllocator returns the allocator context for the given session ID,
// creating a new one via chromedp.NewRemoteAllocator if none exists.
func (t *NativeBrowserTool) getOrCreateAllocator(sessionID string) (context.Context, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if conn, exists := t.conns[sessionID]; exists {
		return conn.ctx, nil
	}

	// Resolve the WebSocket URL ourselves, then pass it directly with NoModifyURL
	// to avoid chromedp's own modifyURL, which would also try auto-discovery but
	// with no fallback.
	resolvedURL, err := t.resolveWebSocketURL(context.Background(), t.wsURL)
	if err != nil {
		return nil, err
	}

	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), resolvedURL, chromedp.NoModifyURL)
	t.conns[sessionID] = remoteConnection{
		ctx:    allocCtx,
		cancel: allocCancel,
	}
	return allocCtx, nil
}

// EndSession closes the allocator connection for the given session ID,
// and releases all cached target contexts (without closing the target tabs).
// Called by the agent loop when a session ends.
func (t *NativeBrowserTool) EndSession(sessionID string) {
	// Release all cached target contexts for this session
	t.targetsMu.Lock()
	if targets, ok := t.targets[sessionID]; ok {
		for _, tc := range targets {
			tc.cancel()
		}
		delete(t.targets, sessionID)
	}
	t.targetsMu.Unlock()

	t.mu.Lock()
	defer t.mu.Unlock()

	if conn, exists := t.conns[sessionID]; exists {
		conn.cancel()
		delete(t.conns, sessionID)
	}
}

// getOrCreateTargetCtx returns a cached chromedp context for the given target
// in the given session, creating one if it doesn't exist. This avoids the
// problem of creating a new context per operation (which would close the target
// when cancelled).
func (t *NativeBrowserTool) getOrCreateTargetCtx(allocCtx context.Context, sessionID, targetID string) (context.Context, error) {
	t.targetsMu.Lock()
	defer t.targetsMu.Unlock()

	if t.targets[sessionID] == nil {
		t.targets[sessionID] = make(map[string]*targetContext)
	}

	if tc, ok := t.targets[sessionID][targetID]; ok {
		return tc.ctx, nil
	}

	ctx, cancel := chromedp.NewContext(allocCtx, chromedp.WithTargetID(target.ID(targetID)))
	t.targets[sessionID][targetID] = &targetContext{
		ctx:    ctx,
		cancel: cancel,
	}
	return ctx, nil
}

// releaseTargetCtx removes and cancels a cached target context for the given
// target. This is called by actions that want to detach from a target without
// waiting for session end (e.g. after navigation the target may no longer
// exist).
func (t *NativeBrowserTool) releaseTargetCtx(sessionID, targetID string) {
	t.targetsMu.Lock()
	defer t.targetsMu.Unlock()

	if targets, ok := t.targets[sessionID]; ok {
		if tc, ok := targets[targetID]; ok {
			tc.cancel()
			delete(targets, targetID)
		}
	}
}

// prepareTarget ensures the browser is initialized for the given target and
// returns a deadline-bounded context suited to a single target operation.
//
// Initialization runs under the tool's actionTimeout; a connection that cannot
// attach within that window is treated as unhealthy: the cached target context
// is torn down (released) and an error is returned so the browser tool call
// cannot block the agent loop indefinitely. On success a deadline child of the
// long-lived tabCtx is returned (per commit 1cecff1, the first successful
// chromedp.Run uses the long-lived context so the RemoteAllocator does not
// register its cancel handler on a short-lived timeout context and close the
// tab).
func (t *NativeBrowserTool) prepareTarget(allocCtx context.Context, sessionID, targetID string) (context.Context, context.CancelFunc, error) {
	tabCtx, err := t.getOrCreateTargetCtx(allocCtx, sessionID, targetID)
	if err != nil {
		return nil, nil, err
	}

	initDone := make(chan error, 1)
	go func() {
		initDone <- chromedp.Run(tabCtx)
	}()
	select {
	case err := <-initDone:
		if err != nil {
			return nil, nil, err
		}
	case <-time.After(t.actionTimeout):
		t.releaseTargetCtx(sessionID, targetID)
		return nil, nil, fmt.Errorf("browser did not initialize within %s", t.actionTimeout)
	}

	opCtx, cancel := context.WithTimeout(tabCtx, t.actionTimeout)
	return opCtx, cancel, nil
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
func (t *NativeBrowserTool) navigate(allocCtx context.Context, sessionID string, rawArgs json.RawMessage) (ToolResult, error) {
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

	// Get or create a cached context for this target (avoids closing the tab on cancel)
	tabCtx, err := t.getOrCreateTargetCtx(allocCtx, sessionID, args.TargetID)
	if err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to attach to target: %v", err))), nil
	}
	// Initialize the browser on the long-lived tabCtx so chromedp's RemoteAllocator
	// registers its cancel handler on tabCtx rather than on a short-lived timeout
	// context. If we let the first Run happen on navCtx, Allocate would watch
	// navCtx.Done() and Cancel(ctx) would cascade up to cancel tabCtx, closing the
	// browser tab when navCtx expires.
	if err := chromedp.Run(tabCtx); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to initialize browser: %v", err))), nil
	}

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
func (t *NativeBrowserTool) typeText(allocCtx context.Context, sessionID string, rawArgs json.RawMessage) (ToolResult, error) {
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

	// Get or create a cached context for this target, initialize the browser on
	// the long-lived context, and bound the operation by a deadline so a hung
	// CDP connection cannot block the agent loop indefinitely.
	opCtx, opCancel, err := t.prepareTarget(allocCtx, sessionID, args.TargetID)
	if err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to attach to target: %v", err))), nil
	}
	defer opCancel()

	// Run the type sequence: wait for element, clear existing value, then type
	if err := chromedp.Run(opCtx,
		chromedp.WaitVisible(args.Selector, chromedp.ByQuery),
		chromedp.Clear(args.Selector, chromedp.ByQuery),
		chromedp.SendKeys(args.Selector, args.Text, chromedp.ByQuery),
	); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to type into element matching selector %q: %v", args.Selector, err))), nil
	}

	return TextResult(fmt.Sprintf("Typed text into element matching selector %q", args.Selector)), nil
}

// click clicks an element identified by CSS selector in the specified target tab.
// It waits for the element to become visible before clicking.
func (t *NativeBrowserTool) click(allocCtx context.Context, sessionID string, rawArgs json.RawMessage) (ToolResult, error) {
	var args clickArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: invalid click action args: %v", err))), nil
	}

	if args.TargetID == "" {
		return ToolError(TextBlocks("Error: 'target_id' is required for click action")), nil
	}

	if args.Selector == "" {
		return ToolError(TextBlocks("Error: 'selector' is required for click action")), nil
	}

	// Get or create a cached context for this target, initialize the browser on
	// the long-lived context, and bound the operation by a deadline so a hung
	// CDP connection cannot block the agent loop indefinitely.
	opCtx, opCancel, err := t.prepareTarget(allocCtx, sessionID, args.TargetID)
	if err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to attach to target: %v", err))), nil
	}
	defer opCancel()

	// Wait for the element to be visible (default 10s timeout), then click it.
	// The per-element timeout is a child of the prepared op context so both the
	// init handshake (op deadline) and the element interaction stay bounded.
	clickCtx, clickCancel := context.WithTimeout(opCtx, 10*time.Second)
	defer clickCancel()

	if err := chromedp.Run(clickCtx,
		chromedp.WaitVisible(args.Selector, chromedp.ByQuery),
		chromedp.Click(args.Selector, chromedp.ByQuery),
	); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return ToolError(TextBlocks(fmt.Sprintf("Error: Element matching selector %q did not become visible within 10s", args.Selector))), nil
		}
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to click element matching selector %q: %v", args.Selector, err))), nil
	}

	return TextResult(fmt.Sprintf("Clicked element matching selector %q", args.Selector)), nil
}

// screenshot captures a screenshot of the specified target tab's viewport.
// It saves the PNG to the workspace root with a timestamped filename and
// returns both a text message and the image data for vision-capable models.
func (t *NativeBrowserTool) screenshot(allocCtx context.Context, sessionID string, rawArgs json.RawMessage) (ToolResult, error) {
	var args screenshotArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: invalid screenshot action args: %v", err))), nil
	}

	if args.TargetID == "" {
		return ToolError(TextBlocks("Error: 'target_id' is required for screenshot action")), nil
	}

	// Get or create a cached context for this target, initialize the browser on
	// the long-lived context, and bound the operation by a deadline so a hung
	// CDP connection cannot block the agent loop indefinitely.
	opCtx, opCancel, err := t.prepareTarget(allocCtx, sessionID, args.TargetID)
	if err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to attach to target: %v", err))), nil
	}
	defer opCancel()

	// Capture the screenshot (viewport only)
	var pngData []byte
	if err := chromedp.Run(opCtx,
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

// newTab opens a fresh browser tab and returns its target_id so subsequent
// actions can operate on it. The tab is created on the long-lived allocator
// context (not a short-lived timeout child) so the RemoteAllocator does not
// register its cancel handler on a context that expires and closes the tab;
// creation is instead bounded by the action timeout externally.
func (t *NativeBrowserTool) newTab(allocCtx context.Context, sessionID string, rawArgs json.RawMessage) (ToolResult, error) {
	// new_tab takes no required arguments; accept missing/null/empty args.
	if len(rawArgs) > 0 && string(rawArgs) != "null" {
		var args newTabArgs
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return ToolError(TextBlocks(fmt.Sprintf("Error: invalid new_tab action args: %v", err))), nil
		}
	}

	// Create a new chromedp context on the allocator. Its first Run creates a
	// brand new tab via target.CreateTarget.
	tabCtx, cancel := chromedp.NewContext(allocCtx)

	initDone := make(chan error, 1)
	go func() {
		initDone <- chromedp.Run(tabCtx)
	}()
	select {
	case err := <-initDone:
		if err != nil {
			cancel()
			return ToolError(TextBlocks(fmt.Sprintf("Error: failed to open new tab: %v", err))), nil
		}
	case <-time.After(t.actionTimeout):
		cancel()
		return ToolError(TextBlocks(fmt.Sprintf("Error: browser did not open a new tab within %s", t.actionTimeout))), nil
	}

	cdpCtx := chromedp.FromContext(tabCtx)
	if cdpCtx == nil || cdpCtx.Target == nil {
		cancel()
		return ToolError(TextBlocks("Error: new tab was created but its target could not be identified")), nil
	}
	targetID := cdpCtx.Target.TargetID

	// Cache the context so the tab stays open for subsequent actions and is
	// released when the session ends.
	t.targetsMu.Lock()
	if t.targets[sessionID] == nil {
		t.targets[sessionID] = make(map[string]*targetContext)
	}
	t.targets[sessionID][string(targetID)] = &targetContext{ctx: tabCtx, cancel: cancel}
	t.targetsMu.Unlock()

	return TextResult(fmt.Sprintf("Opened new tab with target_id: %s", targetID)), nil
}

// closeTab closes the target tab. It attaches via the deadline-bounded
// prepareTarget path and issues target.CloseTarget through the browser
// connection, then releases the cached target context.
func (t *NativeBrowserTool) closeTab(allocCtx context.Context, sessionID string, rawArgs json.RawMessage) (ToolResult, error) {
	var args closeTabArgs
	if len(rawArgs) > 0 && string(rawArgs) != "null" {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return ToolError(TextBlocks(fmt.Sprintf("Error: invalid close_tab action args: %v", err))), nil
		}
	}

	if args.TargetID == "" {
		return ToolError(TextBlocks("Error: 'target_id' is required for close_tab action")), nil
	}

	opCtx, opCancel, err := t.prepareTarget(allocCtx, sessionID, args.TargetID)
	if err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to attach to target: %v", err))), nil
	}
	defer opCancel()

	if err := target.CloseTarget(target.ID(args.TargetID)).Do(cdp.WithExecutor(opCtx, chromedp.FromContext(opCtx).Browser)); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to close tab %s: %v", args.TargetID, err))), nil
	}
	t.releaseTargetCtx(sessionID, args.TargetID)

	return TextResult(fmt.Sprintf("Closed tab %s", args.TargetID)), nil
}

// selectOption sets a <select> element to the given option value. It waits for
// the element to become visible and reports errors like click/type.
func (t *NativeBrowserTool) selectOption(allocCtx context.Context, sessionID string, rawArgs json.RawMessage) (ToolResult, error) {
	var args selectArgs
	if len(rawArgs) > 0 && string(rawArgs) != "null" {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return ToolError(TextBlocks(fmt.Sprintf("Error: invalid select action args: %v", err))), nil
		}
	}

	if args.TargetID == "" {
		return ToolError(TextBlocks("Error: 'target_id' is required for select action")), nil
	}

	if args.Selector == "" {
		return ToolError(TextBlocks("Error: 'selector' is required for select action")), nil
	}

	if args.Value == "" {
		return ToolError(TextBlocks("Error: 'value' is required for select action")), nil
	}

	opCtx, opCancel, err := t.prepareTarget(allocCtx, sessionID, args.TargetID)
	if err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to attach to target: %v", err))), nil
	}
	defer opCancel()

	// Wait for the element to be visible (default 10s timeout), then set its
	// value. SetValue dispatches input + change events so page logic wired to
	// the dropdown fires, matching a real user selection.
	selCtx, selCancel := context.WithTimeout(opCtx, 10*time.Second)
	defer selCancel()

	if err := chromedp.Run(selCtx,
		chromedp.WaitVisible(args.Selector, chromedp.ByQuery),
		chromedp.SetValue(args.Selector, args.Value, chromedp.ByQuery),
	); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return ToolError(TextBlocks(fmt.Sprintf("Error: Element matching selector %q did not become visible within 10s", args.Selector))), nil
		}
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to set select element matching selector %q to value %q: %v", args.Selector, args.Value, err))), nil
	}

	return TextResult(fmt.Sprintf("Set select element matching selector %q to value %q", args.Selector, args.Value)), nil
}

// getValue reads back the current value of the form element identified by the
// CSS selector (input, textarea, select, or any element with a .value field).
func (t *NativeBrowserTool) getValue(allocCtx context.Context, sessionID string, rawArgs json.RawMessage) (ToolResult, error) {
	var args getValueArgs
	if len(rawArgs) > 0 && string(rawArgs) != "null" {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return ToolError(TextBlocks(fmt.Sprintf("Error: invalid get_value action args: %v", err))), nil
		}
	}

	if args.TargetID == "" {
		return ToolError(TextBlocks("Error: 'target_id' is required for get_value action")), nil
	}

	if args.Selector == "" {
		return ToolError(TextBlocks("Error: 'selector' is required for get_value action")), nil
	}

	opCtx, opCancel, err := t.prepareTarget(allocCtx, sessionID, args.TargetID)
	if err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to attach to target: %v", err))), nil
	}
	defer opCancel()

	var value string
	if err := chromedp.Run(opCtx,
		chromedp.Value(args.Selector, &value, chromedp.ByQuery),
	); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to read value of element matching selector %q: %v", args.Selector, err))), nil
	}

	return TextResult(fmt.Sprintf("Value of element matching selector %q: %q", args.Selector, value)), nil
}

// getDOM returns the DOM content of a page in two modes:
// - Without selector: a structural summary with headings, links, buttons, inputs, and their CSS selectors
// - With selector: the cleaned outerHTML of the matched element
func (t *NativeBrowserTool) getDOM(allocCtx context.Context, sessionID string, rawArgs json.RawMessage) (ToolResult, error) {
	var args getDOMArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: invalid get_dom action args: %v", err))), nil
	}

	if args.TargetID == "" {
		return ToolError(TextBlocks("Error: 'target_id' is required for get_dom action")), nil
	}

	// Get or create a cached context for this target, initialize the browser on
	// the long-lived context, and bound the operation by a deadline so a hung
	// CDP connection cannot block the agent loop indefinitely.
	opCtx, opCancel, err := t.prepareTarget(allocCtx, sessionID, args.TargetID)
	if err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to attach to target: %v", err))), nil
	}
	defer opCancel()

	if args.Selector != "" {
		return t.getDOMBySelector(opCtx, args.Selector)
	}

	return t.getDOMStructuralSummary(opCtx)
}

// getDOMBySelector returns the cleaned outerHTML of the element matching the given CSS selector.
func (t *NativeBrowserTool) getDOMBySelector(tabCtx context.Context, selector string) (ToolResult, error) {
	// Check if the element exists first
	var exists bool
	if err := chromedp.Run(tabCtx,
		chromedp.Evaluate(fmt.Sprintf(`document.querySelector(%q) !== null`, selector), &exists),
	); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: get_dom failed: %v", err))), nil
	}

	if !exists {
		return ToolError(TextBlocks(fmt.Sprintf("Error: No element matching selector %q", selector))), nil
	}

	// Get the outerHTML
	var outerHTML string
	if err := chromedp.Run(tabCtx,
		chromedp.OuterHTML(selector, &outerHTML, chromedp.ByQuery),
	); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to get outerHTML for selector %q: %v", selector, err))), nil
	}

	// Clean the HTML: strip <script>, <style>, comments, and extra whitespace
	cleaned := cleanDOMHTML(outerHTML)

	// Cap at approximately 8K tokens (roughly 32K chars for safety)
	const maxChars = 32000
	if len(cleaned) > maxChars {
		cleaned = cleaned[:maxChars] + "\n\n... [output truncated: DOM content exceeded 8K token limit]"
	}

	return TextResult(cleaned), nil
}

// getDOMStructuralSummary traverses the page DOM and returns a compressed structural summary.
func (t *NativeBrowserTool) getDOMStructuralSummary(tabCtx context.Context) (ToolResult, error) {
	// Use JavaScript to extract the DOM structure
	js := `
(function() {
	var results = [];
	var maxDepth = 12;
	var maxElements = 50;
	var count = 0;

	function getCSSSelector(el) {
		if (el.id) {
			return '#' + el.id;
		}
		var path = [];
		var current = el;
		while (current && current !== document.body && current !== document.documentElement) {
			var selector = current.tagName.toLowerCase();
			if (current.id) {
				selector = '#' + current.id;
				path.unshift(selector);
				break;
			}
			if (current.className && typeof current.className === 'string') {
				selector += '.' + current.className.trim().split(/\\s+/).join('.');
			}
			var sibling = current;
			var nth = 1;
			while ((sibling = sibling.previousElementSibling)) {
				if (sibling.tagName === current.tagName) nth++;
			}
			if (nth > 1) selector += ':nth-of-type(' + nth + ')';
			path.unshift(selector);
			current = current.parentElement;
		}
		return path.join(' > ');
	}

	function extractInfo(el, depth) {
		if (count >= maxElements) return;
		if (depth > maxDepth) return;
		if (!el || el.nodeType !== 1) return;

		var tag = el.tagName.toLowerCase();
		var sel = getCSSSelector(el);

		// Headings
		if (/^h[1-6]$/.test(tag)) {
			var text = el.textContent.trim();
			if (text) {
				results.push({ type: 'heading', level: tag, text: text, selector: sel });
				count++;
			}
		}

		// Links
		if (tag === 'a' && el.href) {
			var text = el.textContent.trim();
			results.push({ type: 'link', text: text, href: el.href, selector: sel });
			count++;
		}

		// Buttons
		if (tag === 'button') {
			var text = el.textContent.trim() || el.value || '';
			results.push({ type: 'button', text: text, selector: sel });
			count++;
		}
		if (tag === 'input' && el.type === 'submit') {
			var text = el.value || 'Submit';
			results.push({ type: 'button', text: text, selector: sel });
			count++;
		}

		// Inputs
		// Skip hidden elements (display:none, type=hidden)
		if (el.offsetParent === null && tag !== 'select') return;
		if (tag === 'input' && el.type === 'hidden') return;
		if ((tag === 'input' && el.type !== 'submit' && el.type !== 'button' && el.type !== 'hidden') || tag === 'textarea' || tag === 'select') {
			var val = el.value || '';
			var placeholder = el.placeholder || '';
			var inputType = el.type || tag;
				results.push({ type: 'input', input_type: inputType, value: val, placeholder: placeholder, name: el.name || '', selector: sel });
			count++;
		}

		// Recurse into children
		for (var i = 0; i < el.children.length; i++) {
			extractInfo(el.children[i], depth + 1);
		}
	}

	// Get title
	var title = document.title || '';

	// Start traversal from body
	if (document.body) {
		extractInfo(document.body, 0);
	}

	return JSON.stringify({ title: title, elements: results });
})();
`

	var resultJSON string
	if err := chromedp.Run(tabCtx,
		chromedp.Evaluate(js, &resultJSON),
	); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: get_dom failed: %v", err))), nil
	}

	var domData struct {
		Title    string       `json:"title"`
		Elements []domElement `json:"elements"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &domData); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to parse DOM data: %v", err))), nil
	}

	return t.formatDOMSummary(domData.Title, domData.Elements), nil
}

// formatDOMSummary formats the extracted DOM data into a text summary.
func (t *NativeBrowserTool) formatDOMSummary(title string, elements []domElement) ToolResult {
	var b strings.Builder

	if title != "" {
		fmt.Fprintf(&b, "Title: %s\n", title)
	}

	// Group by type
	var headings, links, buttons, inputs []domElement
	for _, el := range elements {
		switch el.Type {
		case "heading":
			headings = append(headings, el)
		case "link":
			links = append(links, el)
		case "button":
			buttons = append(buttons, el)
		case "input":
			inputs = append(inputs, el)
		}
	}

	if len(headings) > 0 {
		b.WriteString("\n--- Headings ---\n")
		for _, h := range headings {
			fmt.Fprintf(&b, "  %s: %s\n    Selector: %s\n", h.Level, h.Text, h.Selector)
		}
	}

	if len(links) > 0 {
		b.WriteString("\n--- Links ---\n")
		for _, l := range links {
			text := l.Text
			if text == "" {
				text = "(no text)"
			}
			fmt.Fprintf(&b, "  %s\n    URL: %s\n    Selector: %s\n", text, l.Href, l.Selector)
		}
	}

	if len(buttons) > 0 {
		b.WriteString("\n--- Buttons ---\n")
		for _, btn := range buttons {
			text := btn.Text
			if text == "" {
				text = "(no text)"
			}
			fmt.Fprintf(&b, "  %s\n    Selector: %s\n", text, btn.Selector)
		}
	}

	if len(inputs) > 0 {
		b.WriteString("\n--- Inputs ---\n")
		for _, inp := range inputs {
			desc := fmt.Sprintf("<%s>", inp.InputType)
			if inp.Placeholder != "" {
				desc = fmt.Sprintf("<%s placeholder=%q>", inp.InputType, inp.Placeholder)
			}
			if inp.Value != "" {
				desc += fmt.Sprintf(" value=%q", inp.Value)
			}
			if inp.Name != "" {
				desc += fmt.Sprintf(" name=%q", inp.Name)
			}
			fmt.Fprintf(&b, "  %s\n    Selector: %s\n", desc, inp.Selector)
		}
	}

	if b.Len() == 0 {
		b.WriteString("No significant DOM elements found.")
	}

	result := b.String()

	// Cap at ~6K tokens (roughly 24K chars)
	const maxChars = 24000
	if len(result) > maxChars {
		result = result[:maxChars] + "\n\n... [output truncated: DOM summary exceeded 6K token limit]"
	}

	return TextResult(result)
}

// Pre-compiled regular expressions for DOM parsing and sanitization.
var (
	scriptTagRE    = regexp.MustCompile(`(?si)<script[^>]*>.*?</script>`)
	styleTagRE     = regexp.MustCompile(`(?si)<style[^>]*>.*?</style>`)
	htmlCommentRE  = regexp.MustCompile(`(?si)<!--.*?-->`)
	whitespaceRE   = regexp.MustCompile(`\s+`)
	htmlTagRE      = regexp.MustCompile(`<[^>]*>`)
	headingREs     = []*regexp.Regexp{
		regexp.MustCompile(`<h1[^>]*>(.*?)</h1>`),
		regexp.MustCompile(`<h2[^>]*>(.*?)</h2>`),
		regexp.MustCompile(`<h3[^>]*>(.*?)</h3>`),
		regexp.MustCompile(`<h4[^>]*>(.*?)</h4>`),
		regexp.MustCompile(`<h5[^>]*>(.*?)</h5>`),
		regexp.MustCompile(`<h6[^>]*>(.*?)</h6>`),
	}
)

// cleanDOMHTML removes <script>, <style>, HTML comments, and extra whitespace from HTML.
func cleanDOMHTML(html string) string {
	// Remove <script>...</script>
	html = scriptTagRE.ReplaceAllString(html, "")
	// Remove <style>...</style>
	html = styleTagRE.ReplaceAllString(html, "")
	// Remove HTML comments
	html = htmlCommentRE.ReplaceAllString(html, "")
	// Collapse whitespace
	html = whitespaceRE.ReplaceAllString(html, " ")
	return strings.TrimSpace(html)
}

// buildDOMSummary produces a concise structural summary of page HTML content.
// It returns headings (h1-h6), link count, input/button count, and paragraph text snippets.
func buildDOMSummary(bodyHTML string) string {
	var summary strings.Builder

	// Extract and count headings using separate regexps for each level
	for level := 1; level <= 6; level++ {
		re := headingREs[level-1]
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
	return strings.TrimSpace(htmlTagRE.ReplaceAllString(s, ""))
}
