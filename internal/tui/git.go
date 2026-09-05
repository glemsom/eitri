package tui

import (
	"os"
	"path/filepath"
	"strings"
)

// GitBranch returns the workspace's checked-out git branch name, or "" when the workspace is not inside a git worktree or HEAD is detached/unreadable.
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
