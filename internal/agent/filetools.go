package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileViewerResult holds the result of reading a file.
type FileViewerResult struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

// FileEditorResult holds the result of writing a file.
type FileEditorResult struct {
	Path         string   `json:"path"`
	Mode         string   `json:"mode"`
	BytesWritten int      `json:"bytes_written"`
	OldContent   string   `json:"old_content,omitempty"`
	NewContent   string   `json:"new_content,omitempty"`
	DirsCreated  []string `json:"dirs_created,omitempty"`
}

// ReadFile reads a file with optional line offset and limit.
// offset is 1-indexed line offset. limit caps the number of lines returned.
// Returns the content and whether it was truncated.
func ReadFile(absPath string, offset, limit int) (FileViewerResult, error) {
	// Check not a directory
	info, err := os.Stat(absPath)
	if err != nil {
		return FileViewerResult{}, fmt.Errorf("cannot read file: %w", err)
	}
	if info.IsDir() {
		return FileViewerResult{}, fmt.Errorf("path %q is a directory", absPath)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return FileViewerResult{}, fmt.Errorf("cannot read file: %w", err)
	}

	// Reject binary / non-UTF-8 content (NUL bytes)
	if strings.ContainsRune(string(data), '\x00') {
		return FileViewerResult{}, fmt.Errorf("file %q is not a text file (contains NUL bytes)", absPath)
	}

	// Split into lines
	content := string(data)
	lines := strings.Split(content, "\n")

	// Apply offset (1-indexed)
	startLine := offset
	if startLine < 1 {
		startLine = 1
	}
	if startLine > len(lines) {
		startLine = len(lines)
	}

	endLine := len(lines)
	if limit > 0 && startLine+limit-1 < endLine {
		endLine = startLine + limit - 1
	}

	result := strings.Join(lines[startLine-1:endLine], "\n")
	truncated := limit > 0 && endLine < len(lines)

	return FileViewerResult{
		Path:      absPath,
		Content:   result,
		Truncated: truncated,
	}, nil
}

// WriteFile creates or overwrites a file. Returns old content for overwrite mode.
// mode must be "create" or "overwrite".
func WriteFile(absPath, content, mode string) (FileEditorResult, error) {
	// Validate mode
	if mode != "create" && mode != "overwrite" {
		return FileEditorResult{}, fmt.Errorf("mode must be 'create' or 'overwrite', got %q", mode)
	}

	// Reject binary / NUL bytes
	if strings.ContainsRune(content, '\x00') {
		return FileEditorResult{}, fmt.Errorf("content contains NUL bytes (not allowed in text files)")
	}

	switch mode {
	case "create":
		// Check file doesn't already exist
		if _, err := os.Stat(absPath); err == nil {
			return FileEditorResult{}, fmt.Errorf("file %q already exists (use overwrite mode)", absPath)
		}

		// Create parent directories
		parentDir := filepath.Dir(absPath)
		var dirsCreated []string
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			return FileEditorResult{}, fmt.Errorf("failed to create parent directories: %w", err)
		}

		// Record created dirs (only newly created ones)
		// Return nil for v1 — MkdirAll's created dirs can't be easily determined post-hoc.

		if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
			return FileEditorResult{}, fmt.Errorf("failed to write file: %w", err)
		}

		return FileEditorResult{
			Path:         absPath,
			Mode:         "create",
			BytesWritten: len(content),
			NewContent:   content,
			DirsCreated:  dirsCreated,
		}, nil

	case "overwrite":
		// Check file exists
		info, err := os.Stat(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				return FileEditorResult{}, fmt.Errorf("file %q does not exist (use create mode)", absPath)
			}
			return FileEditorResult{}, fmt.Errorf("cannot stat file: %w", err)
		}
		if info.IsDir() {
			return FileEditorResult{}, fmt.Errorf("path %q is a directory", absPath)
		}

		// Capture old content
		oldData, err := os.ReadFile(absPath)
		if err != nil {
			return FileEditorResult{}, fmt.Errorf("failed to read existing file: %w", err)
		}

		// Write new content
		if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
			return FileEditorResult{}, fmt.Errorf("failed to write file: %w", err)
		}

		return FileEditorResult{
			Path:         absPath,
			Mode:         "overwrite",
			BytesWritten: len(content),
			OldContent:   string(oldData),
			NewContent:   content,
		}, nil
	}

	return FileEditorResult{}, fmt.Errorf("unknown mode: %q", mode)
}


