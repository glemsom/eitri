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
	tr *PathTranslator
}

func (o *openInBrowserTool) Name() string {
	return "open_in_browser"
}

func (o *openInBrowserTool) Description() string {
	return "Open a URL or file:// target in the host browser. A file in the session temp (/tmp) is opened at its host path."
}

func (o *openInBrowserTool) Schema() map[string]any {
	return strictSchema(map[string]any{
		"path": map[string]any{
			"type":        "string",
			"description": "A URL (e.g. https://…), or a file:// URL (e.g. file:///tmp/report.html).",
		},
	}, []string{"path"})
}

func (o *openInBrowserTool) Run(ctx context.Context, args map[string]any) (ToolResult, error) {
	target, err := strArg(args, "path")
	if err != nil {
		return ToolResult{}, err
	}
	host, err := o.translate(target)
	if err != nil {
		return ToolResult{}, err
	}
	if err := o.br.Open(ctx, host); err != nil {
		return ToolResult{}, fmt.Errorf("open_in_browser %s: %w", target, err)
	}
	return ToolResult{Text: fmt.Sprintf("Opened %s in the host browser", host)}, nil
}

// translate maps the model-facing target to the host launch form: a plain URL passes through; a file URL or filesystem path resolves through the shared PathTranslator so a session-temp (/tmp) file opens at its host /tmp/eitri-<GUID> location.
func (o *openInBrowserTool) translate(target string) (string, error) {
	if u, err := url.Parse(target); err == nil && u.Scheme != "" && u.Scheme != "file" {
		return target, nil
	}
	if strings.HasPrefix(target, "file://") {
		u, err := url.Parse(target)
		if err != nil {
			return "", fmt.Errorf("open_in_browser %s: %w", target, err)
		}
		host, _ := o.tr.SandboxToHost(u.Path)
		return "file://" + host, nil
	}
	host, _ := o.tr.SandboxToHost(target)
	return host, nil
}
