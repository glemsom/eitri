package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// BrowserLauncher is the open_in_browser host-side seam.
type BrowserLauncher interface {
	Open(ctx context.Context, target string) error
}

// xdgBrowser is the production BrowserLauncher, delegating to the host xdg-open (or a browser registered for the URL scheme).
type xdgBrowser struct{}

// Open launches the target in the host browser via xdg-open. A missing
// launcher (a soft dependency, never probed at boot) surfaces here as a
// contained error naming the fix.
func (xdgBrowser) Open(ctx context.Context, target string) error {
	if err := exec.CommandContext(ctx, "xdg-open", target).Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("xdg-open not found: install the xdg-utils package (a soft dependency) to enable open_in_browser: %w", err)
		}
		return err
	}
	return nil
}
