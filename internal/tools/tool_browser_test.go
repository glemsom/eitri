package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordingBrowser is a BrowserLauncher seam that records every target handed
// to it, so open_in_browser can be asserted on without launching a real browser.
type recordingBrowser struct {
	targets []string
}

func (b *recordingBrowser) Open(_ context.Context, target string) error {
	b.targets = append(b.targets, target)
	return nil
}

// TestOpenInBrowserLaunchesHostSideTarget verifies open_in_browser launches the
// given target through the BrowserLauncher seam.
func TestOpenInBrowserLaunchesHostSideTarget(t *testing.T) {
	r, _ := newWebFetchRegistry(t, &stubFetcher{body: "<html><body></body></html>"})
	br := r.browser.(*recordingBrowser)
	res, err := r.Run(context.Background(), "open_in_browser", argMap("path", "https://example.com"))
	out := res.Text
	if err != nil {
		t.Fatalf("open_in_browser error = %v, want nil", err)
	}
	if out == "" {
		t.Fatal("open_in_browser returned no confirmation")
	}
	if len(br.targets) != 1 || br.targets[0] != "https://example.com" {
		t.Fatalf("browser targets = %v, want [https://example.com]", br.targets)
	}
}

// TestOpenInBrowserTranslatesSessionTempToHost verifies that when the model asks
// to open a file in the session temp (sandbox /tmp), open_in_browser translates
// the sandbox path to the host /tmp/eitri-<GUID> form before launching.
func TestOpenInBrowserTranslatesSessionTempToHost(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	top := filepath.Join(home, ".eitri-test-br-"+strings.ReplaceAll(t.Name(), "/", "_"))
	ws := filepath.Join(top, "proj")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(top) })
	r := NewRegistry(Deps{
		Workspace: ws,
		TempHost:  "/tmp/eitri-g",
		GUID:      GUID("g"),
		Runner:    &recordingRunner{},
		Fetcher:   &stubFetcher{body: "<html><body></body></html>"},
		Browser:   &recordingBrowser{},
	})
	br := r.browser.(*recordingBrowser)
	res, err := r.Run(context.Background(), "open_in_browser", argMap("path", "file:///tmp/report.html"))
	out := res.Text
	if err != nil {
		t.Fatalf("open_in_browser error = %v, want nil", err)
	}
	if out == "" {
		t.Fatal("open_in_browser returned no confirmation")
	}
	if len(br.targets) != 1 || br.targets[0] != "file:///tmp/eitri-g/report.html" {
		t.Fatalf("browser targets = %v, want translated host path file:///tmp/eitri-g/report.html", br.targets)
	}
}
