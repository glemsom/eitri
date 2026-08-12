package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// openInBrowserTool is the open_in_browser tool: host-side, outside the bwrap
// cage (ADR-0001 decision 4). It opens a single URL or a file:// target in the
// host browser. A file:// target living in the session temp (sandbox /tmp) is
// translated to the host /tmp/eitri-<GUID> path through the shared
// PathTranslator before launch, so the host browser sees the real file.
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

func (o *openInBrowserTool) Run(ctx context.Context, args map[string]any) (string, error) {
	target, err := strArg(args, "path")
	if err != nil {
		return "", err
	}
	host, err := o.translate(target)
	if err != nil {
		return "", err
	}
	if err := o.br.Open(ctx, host); err != nil {
		return "", fmt.Errorf("open_in_browser %s: %w", target, err)
	}
	return fmt.Sprintf("Opened %s in the host browser", host), nil
}

// translate maps the model-facing target to the host launch form: a plain URL
// passes through; a file URL or filesystem path resolves through the shared
// PathTranslator so a session-temp (/tmp) file opens at its host
// /tmp/eitri-<GUID> location (ADR-0001 decision 4, ADR-0002).
func (o *openInBrowserTool) translate(target string) (string, error) {
	if u, err := url.Parse(target); err == nil && u.Scheme != "" && u.Scheme != "file" {
		// A non-file URL (http/https/…) is opened verbatim.
		return target, nil
	}
	// file:// scheme (or a bare path): translate the underlying filesystem path.
	var prefix string
	path := target
	if strings.HasPrefix(target, "file://") {
		// Preserve the exact file URL so the host browser opens a file:// URL.
		if u, err := url.Parse(target); err == nil {
			path = u.Path
			prefix = "file://"
		}
	}
	host, _ := o.tr.SandboxToHost(path)
	return prefix + host, nil
}
