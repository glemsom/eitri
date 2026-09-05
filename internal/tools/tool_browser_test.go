package tools

import (
	"context"
	"errors"
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
	br := &recordingBrowser{}
	r, _ := NewRegistry(Deps{Workspace: t.TempDir(), TempHost: t.TempDir(), Runner: RealRunner, Browser: br})
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

func TestOpenInBrowserOpensCanonicalSessionTempPath(t *testing.T) {
	t.Parallel()
	target := "file:///home/user/.eitri/sessions/g/tmp/report.html"
	br := &recordingBrowser{}
	r, _ := NewRegistry(Deps{Runner: RealRunner, Workspace: t.TempDir(), TempHost: "/home/user/.eitri/sessions/g/tmp", Browser: br})
	res, err := r.Run(context.Background(), "open_in_browser", argMap("path", target))
	out := res.Text
	if err != nil {
		t.Fatalf("open_in_browser error = %v, want nil", err)
	}
	if out == "" {
		t.Fatal("open_in_browser returned no confirmation")
	}
	if len(br.targets) != 1 || br.targets[0] != target {
		t.Fatalf("browser targets = %v, want [%s]", br.targets, target)
	}
}

func TestOpenInBrowserRejectsMalformedFileURL(t *testing.T) {
	t.Parallel()
	br := &recordingBrowser{}
	o := &openInBrowserTool{br: br}
	if _, err := o.Run(context.Background(), argMap("path", "file://%zz")); err == nil {
		t.Fatal("Run(file://%zz) = nil error, want malformed URL error")
	}
	if len(br.targets) != 0 {
		t.Fatalf("launcher touched with %v, want none", br.targets)
	}
}

type failingBrowser struct {
	err error
}

func (b *failingBrowser) Open(_ context.Context, _ string) error { return b.err }

func TestOpenInBrowserStableMeta(t *testing.T) {
	t.Parallel()
	o := &openInBrowserTool{br: &recordingBrowser{}}
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
	o := &openInBrowserTool{br: br}
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
	o := &openInBrowserTool{br: br}
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
