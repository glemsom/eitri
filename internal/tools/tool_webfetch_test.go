package tools

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubFetcher is a recording Fetcher seam returning a canned HTML body. It also
// records the URL asked for so tests can assert web_fetch hits the right target.
type stubFetcher struct {
	body string
	urls []string
}

func (s *stubFetcher) Fetch(_ context.Context, url string) (io.ReadCloser, error) {
	s.urls = append(s.urls, url)
	return io.NopCloser(strings.NewReader(s.body)), nil
}

// newWebFetchRegistry builds a registry whose web_fetch uses the given Fetcher
// seam, bypassing the network, and whose open_in_browser records launches.
func newWebFetchRegistry(t *testing.T, f Fetcher) (*Registry, string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	top := filepath.Join(home, ".eitri-test-wf-"+strings.ReplaceAll(t.Name(), "/", "_"))
	ws := filepath.Join(top, "proj")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(top) })
	r := NewRegistry(Deps{
		Workspace: ws,
		TempHost:  filepath.Join(t.TempDir(), "eitri-g"),
		GUID:      GUID("tguid"),
		Runner:    &recordingRunner{},
		Fetcher:   f,
		Browser:   &recordingBrowser{},
	})
	return r, ws
}

// TestWebFetchConvertsHTMLToMarkdown verifies web_fetch fetches the requested
// URL through the seam and returns the HTML rendered as Markdown on the
// tool-result channel (never a bash invocation, never a system message).
func TestWebFetchConvertsHTMLToMarkdown(t *testing.T) {
	f := &stubFetcher{body: `<html><body><h1>Title</h1><p>Hello <strong>bold</strong> world.</p><ul><li>one</li><li>two</li></ul></body></html>`}
	r, _ := newWebFetchRegistry(t, f)
	res, err := r.Run(context.Background(), "web_fetch", argMap("url", "https://example.com/doc"))
	out := res.Text
	if err != nil {
		t.Fatalf("web_fetch error = %v, want nil", err)
	}
	if len(f.urls) != 1 || f.urls[0] != "https://example.com/doc" {
		t.Fatalf("web_fetch fetched urls = %v, want exactly [https://example.com/doc]", f.urls)
	}
	if !strings.Contains(out, "# Title") {
		t.Fatalf("markdown output missing heading: %q", out)
	}
	if !strings.Contains(out, "**bold**") {
		t.Fatalf("markdown output missing bold: %q", out)
	}
	if !strings.Contains(out, "- one") || !strings.Contains(out, "- two") {
		t.Fatalf("markdown output missing list items: %q", out)
	}
}

// TestWebFetchIsOwnPathNotBash verifies web_fetch never goes through the bash
// runner: the recording runner gets no commands while the fetch completes.
func TestWebFetchIsOwnPathNotBash(t *testing.T) {
	f := &stubFetcher{body: `<html><body><p>plain</p></body></html>`}
	r, _ := newWebFetchRegistry(t, f)
	res, err := r.Run(context.Background(), "web_fetch", argMap("url", "https://example.com/x"))
	out := res.Text
	if err != nil {
		t.Fatalf("web_fetch error = %v, want nil", err)
	}
	if !strings.Contains(out, "plain") {
		t.Fatalf("web_fetch output = %q, want converted markdown", out)
	}
	if rr := r.sandbox.run.(*recordingRunner); len(rr.calls) != 0 {
		t.Fatalf("web_fetch touched the bash runner: recorded commands = %v", rr.calls)
	}
}
