package tools

import (
	"context"
	"errors"
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
	t.Parallel()
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
	t.Parallel()
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

// TestOpenInBrowserTranslateErrorsOnMalformedFileURL verifies translate surfaces
// a real error instead of silently passing an unparseable file:// URL through to
// the launcher (which would open garbage in the host browser).
func TestOpenInBrowserTranslateErrorsOnMalformedFileURL(t *testing.T) {
	t.Parallel()
	o := &openInBrowserTool{br: &recordingBrowser{}, tr: NewPathTranslator(GUID("g"))}
	host, err := o.translate("file://%zz")
	if err == nil {
		t.Fatalf("translate(file://%%zz) error = nil, want error; host = %q", host)
	}
	if !strings.Contains(err.Error(), "file://%zz") {
		t.Fatalf("translate error = %q, want it to name the offending target", err)
	}
}

// failingBrowser is a BrowserLauncher seam that always returns the configured
// error, so Open-in-browser's launcher-failure path is testable.
type failingBrowser struct {
	err error
}

func (b *failingBrowser) Open(_ context.Context, _ string) error { return b.err }

// TestOpenInBrowserStableMeta locks the tool's name and description so the
// provider-facing tool surface cannot drift.
func TestOpenInBrowserStableMeta(t *testing.T) {
	t.Parallel()
	o := &openInBrowserTool{br: &recordingBrowser{}, tr: NewPathTranslator(GUID("g"))}
	if got := o.Name(); got != "open_in_browser" {
		t.Fatalf("Name() = %q, want open_in_browser", got)
	}
	if got := o.Description(); got == "" {
		t.Fatal("Description() = \"\", want a non-empty description")
	}
}

// TestOpenInBrowserRunRejectsMissingPath verifies a call without the required
// path argument fails fast and never touches the launcher.
func TestOpenInBrowserRunRejectsMissingPath(t *testing.T) {
	t.Parallel()
	br := &recordingBrowser{}
	o := &openInBrowserTool{br: br, tr: NewPathTranslator(GUID("g"))}
	if _, err := o.Run(context.Background(), map[string]any{}); err == nil {
		t.Fatal("Run() without path = nil error, want error")
	}
	if len(br.targets) != 0 {
		t.Fatalf("launcher touched with %v, want none for a missing arg", br.targets)
	}
}

// TestOpenInBrowserRunSurfacesLauncherError verifies a launcher failure is
// wrapped with the tool name and target so the caller sees why the open failed.
func TestOpenInBrowserRunSurfacesLauncherError(t *testing.T) {
	t.Parallel()
	br := &failingBrowser{err: errors.New("xdg-open: no app found")}
	o := &openInBrowserTool{br: br, tr: NewPathTranslator(GUID("g"))}
	_, err := o.Run(context.Background(), argMap("path", "https://example.com"))
	if err == nil {
		t.Fatal("Run() = nil error, want the launcher error wrapped")
	}
	if !strings.Contains(err.Error(), "open_in_browser https://example.com") {
		t.Fatalf("Run() error = %q, want it to name the tool and target", err)
	}
	if !strings.Contains(err.Error(), "no app found") {
		t.Fatalf("Run() error = %q, want the underlying launcher cause", err)
	}
}

// TestOpenInBrowserRunSurfacesTranslateError verifies a malformed file URL
// surfaces from Run (via translate) without touching the launcher.
func TestOpenInBrowserRunSurfacesTranslateError(t *testing.T) {
	t.Parallel()
	br := &recordingBrowser{}
	o := &openInBrowserTool{br: br, tr: NewPathTranslator(GUID("g"))}
	if _, err := o.Run(context.Background(), argMap("path", "file://%zz")); err == nil {
		t.Fatal("Run(file://%%zz) = nil error, want a translate error")
	}
	if len(br.targets) != 0 {
		t.Fatalf("launcher touched with %v, want none for a malformed file URL", br.targets)
	}
}

// TestOpenInBrowserTranslateSchemeURLPassesThrough verifies a non-file URL
// (http/https/…) is opened verbatim, never remapped.
func TestOpenInBrowserTranslateSchemeURLPassesThrough(t *testing.T) {
	t.Parallel()
	o := &openInBrowserTool{br: &recordingBrowser{}, tr: NewPathTranslator(GUID("g"))}
	host, err := o.translate("https://example.com/page")
	if err != nil {
		t.Fatalf("translate(https URL) error = %v, want nil", err)
	}
	if host != "https://example.com/page" {
		t.Fatalf("translate(https URL) = %q, want it passed through verbatim", host)
	}
}

// TestOpenInBrowserTranslateBareTempPath verifies a bare session-temp path
// (no scheme) resolves to the host session-temp form.
func TestOpenInBrowserTranslateBareTempPath(t *testing.T) {
	t.Parallel()
	o := &openInBrowserTool{br: &recordingBrowser{}, tr: NewPathTranslator(GUID("g"))}
	host, err := o.translate("/tmp/report.html")
	if err != nil {
		t.Fatalf("translate(/tmp path) error = %v, want nil", err)
	}
	if host != "/tmp/eitri-g/report.html" {
		t.Fatalf("translate(/tmp path) = %q, want /tmp/eitri-g/report.html", host)
	}
}

// TestOpenInBrowserTranslateFileNonTempPath verifies a file:// URL outside the
// session temp is preserved unchanged (no GUID injected for non-/tmp targets).
func TestOpenInBrowserTranslateFileNonTempPath(t *testing.T) {
	t.Parallel()
	o := &openInBrowserTool{br: &recordingBrowser{}, tr: NewPathTranslator(GUID("g"))}
	host, err := o.translate("file:///etc/hosts")
	if err != nil {
		t.Fatalf("translate(file:// non-temp) error = %v, want nil", err)
	}
	if host != "file:///etc/hosts" {
		t.Fatalf("translate(file:// non-temp) = %q, want file:///etc/hosts unchanged", host)
	}
}

// TestOpenInBrowserTranslateAlreadyHostPath verifies an already-host-form temp
// path is left untouched (the translator is idempotent).
func TestOpenInBrowserTranslateAlreadyHostPath(t *testing.T) {
	t.Parallel()
	o := &openInBrowserTool{br: &recordingBrowser{}, tr: NewPathTranslator(GUID("g"))}
	host, err := o.translate("/tmp/eitri-g/report.html")
	if err != nil {
		t.Fatalf("translate(already-host path) error = %v, want nil", err)
	}
	if host != "/tmp/eitri-g/report.html" {
		t.Fatalf("translate(already-host path) = %q, want unchanged", host)
	}
}
