package runner

import (
	"log/slog"
	"os"
	"path/filepath"
)

const maxRepoInstructionsSize = 4096

// readRepositoryInstructions reads <workspace>/AGENTS.md, caps content at 4KB,
// wraps in <repository_instructions>...</repository_instructions> tags, and
// logs a warning when truncation occurs. Missing file = empty string, no error.
func readRepositoryInstructions(workspace string) (string, error) {
	path := filepath.Join(workspace, "AGENTS.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	content := string(data)
	if content == "" {
		return "", nil
	}

	if len(content) > maxRepoInstructionsSize {
		content = content[:maxRepoInstructionsSize] + "[truncated...]"
		slog.Warn("AGENTS.md truncated", slog.Int("max_bytes", maxRepoInstructionsSize))
	}

	return "<repository_instructions>\n" + content + "\n</repository_instructions>", nil
}
