package executor

import (
	"errors"
	"os/exec"
)

// TmuxPath is the path to the tmux binary, overridable for testing.
var TmuxPath = "tmux"

// RunAudit checks that tmux is available on $PATH.
// Returns nil if found, or an actionable error if not.
func RunAudit() error {
	_, err := exec.LookPath(TmuxPath)
	if err != nil {
		return errors.New("tmux not found on $PATH. Install it with your package manager:\n  Debian/Ubuntu: sudo apt install tmux\n  Fedora: sudo dnf install tmux\n  Arch: sudo pacman -S tmux\n  Alpine: sudo apk add tmux")
	}
	return nil
}
