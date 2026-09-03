package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// openInBrowserTool is the open_in_browser tool: host-side, outside the bwrap cage.
type openInBrowserTool struct {
	br BrowserLauncher
}

func (o *openInBrowserTool) Name() string {
	return "open_in_browser"
}

func (o *openInBrowserTool) Description() string {
	return "Open a URL or file:// target in the host browser. A file in the session temp is opened at its host path."
}

func (o *openInBrowserTool) Schema() map[string]any {
	return strictSchema(map[string]any{
		"path": map[string]any{
			"type":        "string",
			"description": "A URL (e.g. https://…), a file:// URL, or a filesystem path. For session-temp files, pass the concrete path written under $TMPDIR.",
		},
	}, []string{"path"})
}

func (o *openInBrowserTool) Run(ctx context.Context, args map[string]any) (ToolResult, error) {
	target, err := strArg(args, "path")
	if err != nil {
		return ToolResult{}, err
	}
	if strings.HasPrefix(target, "file://") {
		if _, err := url.Parse(target); err != nil {
			return ToolResult{}, fmt.Errorf("open_in_browser %s: %w", target, err)
		}
	}
	if err := o.br.Open(ctx, target); err != nil {
		return ToolResult{}, fmt.Errorf("open_in_browser %s: %w", target, err)
	}
	return ToolResult{Text: fmt.Sprintf("Opened %s in the host browser", target)}, nil
}
