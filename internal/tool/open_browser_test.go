package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newOpenBrowserTool builds a tool with no tmpdir mapping (tmp rewrite disabled).
func newOpenBrowserTool(t *testing.T) *OpenBrowserTool {
	t.Helper()
	return NewOpenBrowserTool(t.TempDir(), nil)
}

// newOpenBrowserToolWithTmp maps every non-empty sessionID to hostDir.
func newOpenBrowserToolWithTmp(workspace, hostDir string) *OpenBrowserTool {
	return NewOpenBrowserTool(workspace, func(string) (string, bool) { return hostDir, true })
}

func TestOpenBrowser_RequiresURL(t *testing.T) {
	tool := newOpenBrowserTool(t)
	ctx := context.WithValue(context.Background(), SessionIDKey, "sess-1")

	res, _ := tool.Call(ctx, []byte(`{"url": ""}`))
	if !res.IsError {
		t.Error("Call with empty url: expected IsError")
	}
}

func TestOpenBrowser_SchemeValidation(t *testing.T) {
	tool := newOpenBrowserTool(t)
	ctx := context.WithValue(context.Background(), SessionIDKey, "sess-1")

	cases := []string{
		"javascript:alert(1)",
		"javascript:void(0)",
		"mailto:foo@bar.com",
		"data:text/html,<script>alert(1)</script>",
		"ftp://example.com/file",
		"gopher://example.com",
	}
	for _, raw := range cases {
		if _, err := tool.resolveURL(ctx, raw); err == nil {
			t.Errorf("resolveURL(%q): expected error, got nil", raw)
		} else if !strings.Contains(err.Error(), "scheme") {
			t.Errorf("resolveURL(%q): error %v should mention unsupported scheme", raw, err)
		}
	}
}

func TestOpenBrowser_HTTPAllowed(t *testing.T) {
	tool := newOpenBrowserTool(t)
	ctx := context.WithValue(context.Background(), SessionIDKey, "sess-1")

	for _, raw := range []string{"https://example.com", "http://example.com/path?q=1"} {
		got, err := tool.resolveURL(ctx, raw)
		if err != nil {
			t.Fatalf("resolveURL(%q): unexpected error %v", raw, err)
		}
		if got != raw {
			t.Errorf("resolveURL(%q) = %q, want unchanged", raw, got)
		}
	}
}

func TestOpenBrowser_BareRelativePathExists(t *testing.T) {
	workspace := t.TempDir()
	report := filepath.Join(workspace, "report.html")
	if err := os.WriteFile(report, []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	tool := NewOpenBrowserTool(workspace, nil)
	ctx := context.WithValue(context.Background(), SessionIDKey, "sess-1")

	got, err := tool.resolveURL(ctx, "./report.html")
	if err != nil {
		t.Fatalf("resolveURL: unexpected error %v", err)
	}
	want := "file://" + report
	if got != want {
		t.Errorf("resolveURL = %q, want %q", got, want)
	}
}

func TestOpenBrowser_BarePathMissing(t *testing.T) {
	tool := newOpenBrowserTool(t)
	ctx := context.WithValue(context.Background(), SessionIDKey, "sess-1")

	if _, err := tool.resolveURL(ctx, "./nonexistent.html"); err == nil {
		t.Error("resolveURL(nonexistent): expected error, got nil")
	}
}

func TestOpenBrowser_TmpRewrite(t *testing.T) {
	hostTmp := t.TempDir()
	hostFile := filepath.Join(hostTmp, "foo.html")
	if err := os.WriteFile(hostFile, []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write hostTmp file: %v", err)
	}
	tool := newOpenBrowserToolWithTmp(t.TempDir(), hostTmp)
	ctx := context.WithValue(context.Background(), SessionIDKey, "sess-tmp")

	got, err := tool.resolveURL(ctx, "/tmp/foo.html")
	if err != nil {
		t.Fatalf("resolveURL: unexpected error %v", err)
	}
	want := "file://" + hostFile
	if got != want {
		t.Errorf("resolveURL = %q, want %q", got, want)
	}
}

func TestOpenBrowser_TmpRewritePassesThroughWhenMissing(t *testing.T) {
	hostTmp := t.TempDir()
	tool := newOpenBrowserToolWithTmp(t.TempDir(), hostTmp)
	ctx := context.WithValue(context.Background(), SessionIDKey, "sess-missing")

	// Specific file missing inside an existing mapped host dir → the bare path
	// rewrites to a nonexistent host path, surfaced as a file-not-found error.
	if _, err := tool.resolveURL(ctx, "/tmp/nope.html"); err == nil {
		t.Error("resolveURL(/tmp/nope.html): expected error (file not found), got nil")
	}
}

func TestOpenBrowser_FileSchemeTmpRewrite(t *testing.T) {
	hostTmp := t.TempDir()
	hostFile := filepath.Join(hostTmp, "foo.html")
	if err := os.WriteFile(hostFile, []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write hostTmp file: %v", err)
	}
	tool := newOpenBrowserToolWithTmp(t.TempDir(), hostTmp)
	ctx := context.WithValue(context.Background(), SessionIDKey, "sess-tmp")

	got, err := tool.resolveURL(ctx, "file:///tmp/foo.html")
	if err != nil {
		t.Fatalf("resolveURL: unexpected error %v", err)
	}
	want := "file://" + hostFile
	if got != want {
		t.Errorf("resolveURL = %q, want %q", got, want)
	}
}
