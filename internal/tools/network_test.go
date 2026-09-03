package tools

import (
	"context"
	"strings"
	"testing"
)

func TestXdgBrowserReturnsRawCommandError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := (xdgBrowser{}).Open(context.Background(), "https://example.com")
	if err == nil {
		t.Fatal("Open() = nil error, want command launch error")
	}
	if strings.Contains(err.Error(), "soft dependency") || strings.Contains(err.Error(), "install") {
		t.Fatalf("Open() error %q carries a special missing-launcher fallback", err)
	}
}
