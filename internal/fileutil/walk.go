package fileutil

import (
	"os"
	"path/filepath"
	"strings"
)

// WalkStop is a sentinel error. Return it from the WalkWorkspace callback to
// stop the entire walk immediately. Unlike filepath.SkipDir (which only stops
// the current directory), WalkStop aborts the whole walk.
type WalkStop struct{}

func (e *WalkStop) Error() string { return "walk stop" }

// WalkWorkspaceFn is the callback signature for WalkWorkspace. It receives the
// absolute file path, the path relative to the workspace root, and the DirEntry.
// Return WalkStop to abort the entire walk. Any other error is passed through.
type WalkWorkspaceFn func(absPath, relPath string, d os.DirEntry) error

// WalkWorkspace walks the workspace directory tree, skipping hidden directories
// (names starting with ".") and the "vendor" directory. Only files (not
// directories) are passed to the callback. An optional filePattern (Go
// filepath.Match syntax) filters which files are visited; empty string means
// all files are visited.
//
// The callback receives the absolute path, the path relative to workspace, and
// the DirEntry. Return a *WalkStop error from the callback to stop the entire
// walk immediately (useful for output caps, unlike filepath.SkipDir which only
// stops the current directory).
func WalkWorkspace(workspace string, fn WalkWorkspaceFn, filePattern string) error {
	// Validate workspace exists
	info, err := os.Stat(workspace)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &os.PathError{Op: "walk", Path: workspace, Err: os.ErrInvalid}
	}

	walkErr := filepath.WalkDir(workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible files/dirs
		}

		// Skip hidden directories
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}

		// Skip vendor directory
		if d.IsDir() && d.Name() == "vendor" {
			return filepath.SkipDir
		}

		// Skip directories themselves, only process files
		if d.IsDir() {
			return nil
		}

		// Get relative path from workspace
		relPath, err := filepath.Rel(workspace, path)
		if err != nil {
			return nil // skip files we can't relativize
		}

		// Apply file pattern filter
		if filePattern != "" {
			matched, err := filepath.Match(filePattern, relPath)
			if err != nil || !matched {
				return nil
			}
		}

		err = fn(path, relPath, d)
		if err != nil {
			// WalkStop aborts the entire walk
			if _, ok := err.(*WalkStop); ok {
				return err
			}
			// Other errors from callback just skip the file
			return nil
		}

		return nil
	})
	if _, ok := walkErr.(*WalkStop); ok {
		return nil // WalkStop is not an error, it's a signal
	}
	return walkErr
}
