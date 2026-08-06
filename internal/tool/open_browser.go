package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/voocel/litellm"
)

// browserOpenTimeout caps how long OpenURL waits for xdg-open to exit before
// giving up, so a wedged launcher never blocks the agent loop (ADR-0026).
const browserOpenTimeout = 10 * time.Second

// openBrowserArgs is the parameter schema for the open_in_browser tool.
type openBrowserArgs struct {
	URL string `json:"url" jsonschema:"URL or file path to open in the host browser. Accepts http, https, or file URLs; a bare path is normalized to file://. A sandbox /tmp/... path is rewritten to the host file."`
}

// TmpdirFor resolves a run's session-scoped sandbox tmpdir on the host, so
// /tmp/... paths written by bash can be mapped back to real host files
// (ADR-0026). It matches the callbacks used by sandbox.Manager.TmpdirFor.
type TmpdirFor func(sessionID string) (string, bool)

// OpenBrowserTool implements ToolHandler for opening a URL in the user's host
// browser via xdg-open. It runs in-process in the unsandboxed harness — unlike
// the bwrap-sandboxed bash tool — so it can reach the host X11/Wayland socket.
type OpenBrowserTool struct {
	schema       litellm.Schema
	workspace    string
	tmpdirLookup TmpdirFor
}

// NewOpenBrowserTool creates a ToolHandler that opens a single URL in the host
// browser. tmpdirLookup maps a session ID to the host path of that session's
// sandbox tmpdir; pass nil to disable /tmp rewriting (safe when no sandbox is
// in use — tmp paths simply pass through).
func NewOpenBrowserTool(workspace string, tmpdirLookup TmpdirFor) *OpenBrowserTool {
	return &OpenBrowserTool{
		schema:       SchemaOf[openBrowserArgs](),
		workspace:    workspace,
		tmpdirLookup: tmpdirLookup,
	}
}

func (t *OpenBrowserTool) Name() string {
	return "open_in_browser"
}

func (t *OpenBrowserTool) Description() string {
	return "Open a single URL or file in the user's host browser (http, https, or file). " +
		"A bare path like ./report.html is opened via file://. A sandbox /tmp/... path is " +
		"rewritten to the matching host file written by bash. Silent: no confirmation prompt; " +
		"the call is visible in the transcript."
}

func (t *OpenBrowserTool) JSONSchema() litellm.Schema {
	return t.schema
}

// Call opens the requested URL in the user's host browser. Scheme-validation,
// bare-path normalization, and /tmp rewriting happen in resolveURL; the resolved
// URL is then launched via OpenURL. Every failure mode is returned as an
// LLM-facing tool error (ToolResult with IsError), never a Go-level error, so a
// bad URL never terminates the agent loop.
func (t *OpenBrowserTool) Call(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var parsed openBrowserArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ToolResult{}, fmt.Errorf("open_in_browser: invalid args: %w", err)
	}

	if parsed.URL == "" {
		return ToolError(TextBlocks("Error: 'url' field is required and must be non-empty")), nil
	}

	resolved, err := t.resolveURL(ctx, parsed.URL)
	if err != nil {
		return ToolError(TextBlocks("Error: " + err.Error())), nil
	}

	if err := OpenURL(resolved); err != nil {
		return ToolError(TextBlocks("Error: " + err.Error())), nil
	}

	return TextResult("Opened " + resolved + " in the browser"), nil
}

// resolveURL validates, normalizes, and (for /tmp paths) rewrites a requested
// URL to the host path that OpenURL can launch. It never launches anything.
//
//   - schemes other than http/https/file are hard-rejected
//   - a bare path (no scheme) is normalized to file://, resolved against the
//     workspace when relative, and must exist on disk
//   - a path starting with /tmp/ is rewritten to the session's sandbox tmpdir
//     on the host when that host path exists; otherwise it passes through
func (t *OpenBrowserTool) resolveURL(ctx context.Context, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %v", raw, err)
	}

	switch u.Scheme {
	case "":
		// Bare path — normalize to file://. Resolve relative paths against the
		// workspace so ./report.html opens the right host file.
		path := u.Path
		if path == "" || strings.HasPrefix(u.String(), ".") {
			path = u.String()
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(t.workspace, path)
		}
		path = filepath.Clean(path)
		path = t.rewriteTmp(ctx, path)
		if path == "" {
			return "", fmt.Errorf("invalid path %q", raw)
		}
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("file not found: %q", path)
		}
		return "file://" + path, nil

	case "http", "https":
		// Explicit web URL — opened unchanged.
		return raw, nil

	case "file":
		path := t.rewriteTmp(ctx, u.Path)
		return "file://" + path, nil

	default:
		return "", fmt.Errorf("unsupported URL scheme %q — only http, https, and file are allowed", u.Scheme)
	}
}

// rewriteTmp maps a sandbox /tmp/... path to the matching host path for the
// run's session, but only when that host path exists; otherwise the input path
// is returned unchanged (ADR-0026).
func (t *OpenBrowserTool) rewriteTmp(ctx context.Context, path string) string {
	if !strings.HasPrefix(path, "/tmp/") || t.tmpdirLookup == nil {
		return path
	}
	sessionID, _ := ctx.Value(SessionIDKey).(string)
	if sessionID == "" {
		return path
	}
	hostDir, ok := t.tmpdirLookup(sessionID)
	if !ok || hostDir == "" {
		return path
	}
	hostPath := filepath.Join(hostDir, strings.TrimPrefix(path, "/tmp/"))
	if _, err := os.Stat(hostPath); err != nil {
		// Mapped host path doesn't exist → pass through unchanged.
		return path
	}
	return hostPath
}

// OpenURL launches url in the user's host browser. It is the shared launcher
// used both by the open_in_browser tool and the EITRI_OPEN_BROWSER startup
// auto-open (ADR-0026). xdg-open is detached into its own process group so a
// Ctrl+C to the foreground group never kills the user's browser, and OpenURL
// waits for it to exit, capped at ~10s.
func OpenURL(url string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("open_in_browser: unsupported platform %q (Linux-only)", runtime.GOOS)
	}
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return fmt.Errorf("open_in_browser: no DISPLAY or WAYLAND_DISPLAY set — cannot open a browser")
	}
	return openURL(url)
}

// openURL is the per-platform seam so open/start mappings for other OSes can be
// added later without touching OpenURL. Linux is the only supported platform.
func openURL(url string) error {
	name, args := openCommand("linux", url)
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = openProcAttr()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open_in_browser: starting %s: %w", name, err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("open_in_browser: %s failed: %v: %s",
				name, err, strings.TrimSpace(stderr.String()))
		}
		return nil
	case <-time.After(browserOpenTimeout):
		return fmt.Errorf("open_in_browser: %s timed out after %s", name, browserOpenTimeout)
	}
}

// openCommand maps a platform to the launcher executable and arguments.
func openCommand(goos, url string) (string, []string) {
	switch goos {
	case "linux":
		return "xdg-open", []string{url}
	default:
		return "xdg-open", []string{url}
	}
}
