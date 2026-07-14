package templates

import "path/filepath"

// pathBase returns the last component of a file path.
// Used in templates to show only the basename of the workspace path.
func pathBase(path string) string {
	return filepath.Base(path)
}
