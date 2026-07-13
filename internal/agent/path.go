package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

// validateWorkspacePath validates and cleans a file path against a workspace root.
// It rejects ../ escapes, absolute paths outside workspace, and empty paths.
func validateWorkspacePath(path, workspace string) (string, error) {
	return validatePathWithAllowed(path, workspace, nil)
}

// validatePathWithAllowed validates a path against a workspace root and optional
// additional allowed roots (e.g., skill directories). The path must be within
// at least one of the allowed roots.
func validatePathWithAllowed(path, workspace string, allowedRoots []string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}

	cleaned := filepath.Clean(path)

	// Build list of roots to validate against
	roots := []string{workspace}
	roots = append(roots, allowedRoots...)

	// For absolute paths, check against each root
	if filepath.IsAbs(cleaned) {
		for _, root := range roots {
			rel, err := filepath.Rel(root, cleaned)
			if err == nil && !strings.HasPrefix(rel, "..") {
				return cleaned, nil
			}
		}
		return "", fmt.Errorf("path %q is outside allowed directories", path)
	}

	// Reject ../ at start of relative paths
	if strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("path %q escapes via ..", path)
	}

	// Try resolving against each root
	for _, root := range roots {
		absPath := filepath.Join(root, cleaned)
		rel, err := filepath.Rel(root, absPath)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return absPath, nil
		}
	}

	return "", fmt.Errorf("path %q is outside allowed directories", path)
}
