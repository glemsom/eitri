package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
)

// Fetcher is the web_fetch network seam. Fetch returns a reader over the
// fetched resource body (already an HTTP 2xx). It is injectable so web_fetch
// is testable without a live network; the production impl is an http.Client
// GET. web_fetch is its own execution path (ADR-0001 decision 2), never a bash
// invocation.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (io.ReadCloser, error)
}

// httpFetcher is the production Fetcher: a plain net/http client.
type httpFetcher struct{}

// Fetch performs an HTTP GET and returns the response body, erroring on a
// non-2xx status so untrusted error pages never masquerade as content.
func (httpFetcher) Fetch(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "eitri/0.1 web_fetch")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("web_fetch %s: HTTP %d %s", url, resp.StatusCode, resp.Status)
	}
	return resp.Body, nil
}

// BrowserLauncher is the open_in_browser host-side seam. Open launches the
// given target (a URL or a host filesystem path) in the host browser. It is
// injectable so open_in_browser is testable without launching a real browser;
// the production impl is xdg-open (ADR-0001 decision 4).
type BrowserLauncher interface {
	Open(ctx context.Context, target string) error
}

// xdgBrowser is the production BrowserLauncher, delegating to the host
// xdg-open (or a browser registered for the URL scheme).
type xdgBrowser struct{}

// Open launches the target in the host browser via xdg-open.
func (xdgBrowser) Open(ctx context.Context, target string) error {
	return exec.CommandContext(ctx, "xdg-open", target).Run()
}
