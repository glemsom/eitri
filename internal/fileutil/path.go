// Package fileutil provides file path validation, workspace checks, and
// line operations (ReadFile, EditFile, InsertLine, WriteFile, ListDirectory).
package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateWorkspacePath validates and cleans a file path against a workspace root.
// It rejects ../ escapes, absolute paths outside workspace, and empty paths.
func ValidateWorkspacePath(path, workspace string) (string, error) {
	return ValidatePathWithAllowed(path, workspace, nil)
}

// ValidateBrowsePath validates a path for the directory browser endpoint.
// It prevents ../ escapes, confirms the path exists, and returns the cleaned
// absolute path. Unlike ValidateWorkspacePath, it does not constrain the path
// to a specific workspace root — the browser can navigate anywhere on disk.
// Returns an error if the path is empty, escapes via .., does not exist, or
// is not a directory.
func ValidateBrowsePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}

	cleaned := filepath.Clean(path)

	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("path %q must be absolute", path)
	}

	// Reject .. escapes from the cleaned absolute path (belt-and-suspenders)
	if strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("path %q escapes via ..", path)
	}

	// Confirm the path exists and is a directory
	info, err := os.Stat(cleaned)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("path %q does not exist", path)
		}
		return "", fmt.Errorf("cannot access path %q: %w", path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", path)
	}

	return cleaned, nil
}

// ValidatePathWithAllowed validates a path against a workspace root and optional
// additional allowed roots (e.g., skill directories). The path must be within
// at least one of the allowed roots.
func ValidatePathWithAllowed(path, workspace string, allowedRoots []string) (string, error) {
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
