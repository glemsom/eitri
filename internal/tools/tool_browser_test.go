package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordingBrowser struct {
	targets []string
}

func (b *recordingBrowser) Open(_ context.Context, target string) error {
	b.targets = append(b.targets, target)
	return nil
}

func TestOpenInBrowserLaunchesHostSideTarget(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	ws := filepath.Join(home, ".eitri-test-brhost-"+strings.ReplaceAll(t.Name(), "/", "_"))
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ws) })
	r := NewRegistry(Deps{
		Workspace: ws,
		TempHost:  filepath.Join(ws, "tmp"),
		GUID:      GUID("g"),
		Runner:    &recordingRunner{},
		Browser:   &recordingBrowser{},
	})
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
		TempHost:  filepath.Join(top, "sessions", "g", "tmp"),
		GUID:      GUID("g"),
		Runner:    &recordingRunner{},
		Browser:   &recordingBrowser{},
	})
	br := r.browser.(*recordingBrowser)
	res, err := r.Run(context.Background(), "open_in_browser", argMap("path", "file://"+filepath.Join(top, "sessions", "g", "tmp", "report.html")))
	out := res.Text
	if err != nil {
		t.Fatalf("open_in_browser error = %v, want nil", err)
	}
	if out == "" {
		t.Fatal("open_in_browser returned no confirmation")
	}
	if len(br.targets) != 1 || br.targets[0] != "file://"+filepath.Join(top, "sessions", "g", "tmp", "report.html") {
		t.Fatalf("browser targets = %v, want session temp path unchanged", br.targets)
	}
}

func TestOpenInBrowserTranslateErrorsOnMalformedFileURL(t *testing.T) {
	t.Parallel()
	o := &openInBrowserTool{br: &recordingBrowser{}, tr: NewPathTranslator()}
	host, err := o.translate("file://%zz")
	if err == nil {
		t.Fatalf("translate(file://%%zz) error = nil, want error; host = %q", host)
	}
	if !strings.Contains(err.Error(), "file://%zz") {
		t.Fatalf("translate error = %q, want it to name the offending target", err)
	}
}

type failingBrowser struct {
	err error
}

func (b *failingBrowser) Open(_ context.Context, _ string) error { return b.err }

func TestOpenInBrowserStableMeta(t *testing.T) {
	t.Parallel()
	o := &openInBrowserTool{br: &recordingBrowser{}, tr: NewPathTranslator()}
	if got := o.Name(); got != "open_in_browser" {
		t.Fatalf("Name() = %q, want open_in_browser", got)
	}
	if got := o.Description(); got == "" {
		t.Fatal("Description() = \"\", want a non-empty description")
	}
}

func TestOpenInBrowserRunRejectsMissingPath(t *testing.T) {
	t.Parallel()
	br := &recordingBrowser{}
	o := &openInBrowserTool{br: br, tr: NewPathTranslator()}
	if _, err := o.Run(context.Background(), map[string]any{}); err == nil {
		t.Fatal("Run() without path = nil error, want error")
	}
	if len(br.targets) != 0 {
		t.Fatalf("launcher touched with %v, want none for a missing arg", br.targets)
	}
}

func TestOpenInBrowserRunSurfacesLauncherError(t *testing.T) {
	t.Parallel()
	br := &failingBrowser{err: errors.New("xdg-open: no app found")}
	o := &openInBrowserTool{br: br, tr: NewPathTranslator()}
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

func TestOpenInBrowserRunSurfacesTranslateError(t *testing.T) {
	t.Parallel()
	br := &recordingBrowser{}
	o := &openInBrowserTool{br: br, tr: NewPathTranslator()}
	if _, err := o.Run(context.Background(), argMap("path", "file://%zz")); err == nil {
		t.Fatal("Run(file://%%zz) = nil error, want a translate error")
	}
	if len(br.targets) != 0 {
		t.Fatalf("launcher touched with %v, want none for a malformed file URL", br.targets)
	}
}

func TestOpenInBrowserTranslateSchemeURLPassesThrough(t *testing.T) {
	t.Parallel()
	o := &openInBrowserTool{br: &recordingBrowser{}, tr: NewPathTranslator()}
	host, err := o.translate("https://example.com/page")
	if err != nil {
		t.Fatalf("translate(https URL) error = %v, want nil", err)
	}
	if host != "https://example.com/page" {
		t.Fatalf("translate(https URL) = %q, want it passed through verbatim", host)
	}
}

func TestOpenInBrowserTranslateBareTempPath(t *testing.T) {
	t.Parallel()
	o := &openInBrowserTool{br: &recordingBrowser{}, tr: NewPathTranslator()}
	host, err := o.translate("/tmp/report.html")
	if err != nil {
		t.Fatalf("translate(/tmp path) error = %v, want nil", err)
	}
	if host != "/tmp/report.html" {
		t.Fatalf("translate(/tmp path) = %q, want /tmp/report.html", host)
	}
}

func TestOpenInBrowserTranslateFileNonTempPath(t *testing.T) {
	t.Parallel()
	o := &openInBrowserTool{br: &recordingBrowser{}, tr: NewPathTranslator()}
	host, err := o.translate("file:///etc/hosts")
	if err != nil {
		t.Fatalf("translate(file:// non-temp) error = %v, want nil", err)
	}
	if host != "file:///etc/hosts" {
		t.Fatalf("translate(file:// non-temp) = %q, want file:///etc/hosts unchanged", host)
	}
}

func TestOpenInBrowserTranslateAlreadyHostPath(t *testing.T) {
	t.Parallel()
	o := &openInBrowserTool{br: &recordingBrowser{}, tr: NewPathTranslator()}
	host, err := o.translate("/tmp/eitri-g/report.html")
	if err != nil {
		t.Fatalf("translate(already-host path) error = %v, want nil", err)
	}
	if host != "/tmp/eitri-g/report.html" {
		t.Fatalf("translate(already-host path) = %q, want unchanged", host)
	}
}
