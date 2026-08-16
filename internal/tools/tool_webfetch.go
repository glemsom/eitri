package tools

import (
	"context"
	"fmt"
	"strings"
)

// webFetchTool is the web_fetch tool: it fetches a URL over HTTP and returns
// the page rendered as Markdown. It is its own execution path — never a bash
// invocation and not network-restricted. The result
// rides the normal tool-result channel so untrusted web content never reaches
// operator-level text.
type webFetchTool struct {
	f Fetcher
}

func (w *webFetchTool) Name() string {
	return "web_fetch"
}

func (w *webFetchTool) Description() string {
	return "Fetch a URL over HTTP and return the page content as Markdown. The fetch runs on its own network-unrestricted execution path (not through bash)."
}

func (w *webFetchTool) Schema() map[string]any {
	return strictSchema(map[string]any{
		"url": map[string]any{
			"type":        "string",
			"description": "The http(s) URL to fetch.",
		},
	}, []string{"url"})
}

func (w *webFetchTool) Run(ctx context.Context, args map[string]any) (ToolResult, error) {
	url, err := strArg(args, "url")
	if err != nil {
		return ToolResult{}, err
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return ToolResult{}, fmt.Errorf("web_fetch: %q is not an http(s) URL", url)
	}
	body, err := w.f.Fetch(ctx, url)
	if err != nil {
		return ToolResult{}, err
	}
	defer body.Close()
	md, err := htmlToMarkdown(body)
	if err != nil {
		return ToolResult{}, fmt.Errorf("web_fetch %s: %w", url, err)
	}
	// Prepend the origin so the model can attribute the fetched content.
	return ToolResult{Text: "Source: " + url + "\n\n" + md}, nil
}
