package tools

import (
	"context"
	"os/exec"
)

// BrowserLauncher is the open_in_browser host-side seam.
type BrowserLauncher interface {
	Open(ctx context.Context, target string) error
}

// xdgBrowser is the production BrowserLauncher, delegating to the host xdg-open (or a browser registered for the URL scheme).
type xdgBrowser struct{}

// Open launches the target in the host browser via xdg-open.
func (xdgBrowser) Open(ctx context.Context, target string) error {
	return exec.CommandContext(ctx, "xdg-open", target).Run()
}
