package tui

import (
	"os"
	"path/filepath"
	"strings"
)

// GitBranch returns the workspace's checked-out git branch name, or "" when
// the workspace is not inside a git worktree or HEAD is detached/unreadable.
// It walks up from the workspace to find the repo root (.git/HEAD) and is a
// pure file read — no git subprocess — so the TUI never spawns tools just to
// show branch context in the statusline (benchmark §4.1 statusline telemetry:
// git branch).
func GitBranch(workspace string) string {
	if workspace == "" {
		return ""
	}
	dir := workspace
	for {
		head, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
		if err == nil {
			s := strings.TrimSpace(string(head))
			if ref, ok := strings.CutPrefix(s, "ref: refs/heads/"); ok {
				return ref
			}
			return "" // detached HEAD or bare repo: no branch name to show
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
