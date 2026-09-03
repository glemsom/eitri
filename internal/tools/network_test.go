package tools

import (
	"context"
	"strings"
	"testing"
)

// missingBackendEnv empties PATH so no browser launcher (xdg-open) resolves,
// deterministically simulating a host without the soft dependency.
func missingBackendEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func TestXdgBrowserMissingLauncherIsContainedError(t *testing.T) {
	missingBackendEnv(t)
	err := (xdgBrowser{}).Open(context.Background(), "https://example.com")
	if err == nil {
		t.Fatal("Open() = nil error, want a contained error when the browser launcher is missing")
	}
	msg := err.Error()
	if !strings.Contains(msg, "xdg-open") {
		t.Fatalf("error %q does not name the missing launcher xdg-open", msg)
	}
	// The error names the fix so a human or agent sees how to enable the tool.
	if !strings.Contains(msg, "xdg-utils") {
		t.Fatalf("error %q lacks the install hint for the soft dependency", msg)
	}
}

func TestOpenInBrowserMissingBackendSurfacesOnlyAtRun(t *testing.T) {
	missingBackendEnv(t)
	o := &openInBrowserTool{br: xdgBrowser{}}
	_, err := o.Run(context.Background(), argMap("path", "https://example.com"))
	if err == nil {
		t.Fatal("Run() = nil error, want the missing launcher surfaced as a contained tool error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "open_in_browser https://example.com") {
		t.Fatalf("Run() error %q does not name the tool and target", msg)
	}
	if !strings.Contains(msg, "xdg-utils") {
		t.Fatalf("Run() error %q lacks the install hint for the soft dependency", msg)
	}
}
