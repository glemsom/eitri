package fileutil

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const defaultReadLimit = 100

// FileViewerResult holds the result of reading a file.
type FileViewerResult struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Truncated  bool   `json:"truncated"`
	TotalLines int    `json:"total_lines,omitempty"`
	NextOffset int    `json:"next_offset,omitempty"`
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

// ListDirResult holds the result of listing a directory.
type ListDirResult struct {
	Path    string   `json:"path"`
	Entries []string `json:"entries"`
}

// LineHash returns the first 8 hex characters of the SHA-256 hash of line.
func LineHash(line string) string {
	h := sha256.Sum256([]byte(line))
	return fmt.Sprintf("%x", h)[:8]
}

// formatLineWithHash prefixes a line with LINE:HASH for anchor use.
func formatLineWithHash(lineNum int, line string) string {
	return fmt.Sprintf("%d:%s | %s", lineNum, LineHash(line), line)
}

// readFileLines reads a file and returns its lines with metadata.
// offset is 1-indexed; 0 or negative means start at line 1.
// limit caps returned lines; 0 means unlimited.
// includeLineInfo controls whether lines are prefixed with LINE:HASH.
func readFileLines(absPath string, offset, limit int, includeLineInfo bool) (FileViewerResult, error) {
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

	// Reject binary / non-UTF-8 content.
	if strings.ContainsRune(string(data), '\x00') {
		return FileViewerResult{}, fmt.Errorf("file %q is not a text file (contains NUL bytes)", absPath)
	}
	if !utf8.Valid(data) {
		return FileViewerResult{}, fmt.Errorf("file %q is not valid UTF-8 text", absPath)
	}

	// Handle empty file
	if len(data) == 0 {
		return FileViewerResult{
			Path:       absPath,
			Content:    "",
			Truncated:  false,
			TotalLines: 0,
			NextOffset: 0,
		}, nil
	}

	content := string(data)
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	// Apply offset (1-indexed)
	startLine := offset
	if startLine < 1 {
		startLine = 1
	}
	if startLine > totalLines {
		startLine = totalLines
	}

	// Apply limit: 0 means unlimited
	effectiveLimit := limit
	if effectiveLimit <= 0 {
		effectiveLimit = totalLines
	}

	endLine := totalLines
	if effectiveLimit > 0 && startLine+effectiveLimit-1 < endLine {
		endLine = startLine + effectiveLimit - 1
	}

	// Build result content
	selectedLines := lines[startLine-1 : endLine]
	var resultLines []string
	if includeLineInfo {
		for i, raw := range selectedLines {
			lineNum := startLine + i
			resultLines = append(resultLines, formatLineWithHash(lineNum, raw))
		}
	} else {
		resultLines = selectedLines
	}
	resultContent := strings.Join(resultLines, "\n")

	truncated := endLine < totalLines

	var nextOffset int
	if truncated {
		nextOffset = endLine + 1
	} else {
		nextOffset = 0
	}

	return FileViewerResult{
		Path:       absPath,
		Content:    resultContent,
		Truncated:  truncated,
		TotalLines: totalLines,
		NextOffset: nextOffset,
	}, nil
}

// ReadFile reads a file with optional line offset and limit.
// offset is 1-indexed line offset; 0 means start at line 1.
// limit caps returned lines; 0 means unlimited.
// Returns content, truncation status, total line count, and next offset.
func ReadFile(absPath string, offset, limit int) (FileViewerResult, error) {
	return readFileLines(absPath, offset, limit, false)
}

// ReadFileWithLineInfo reads a file and prefixes each line with LINE:HASH.
func ReadFileWithLineInfo(absPath string, offset, limit int) (FileViewerResult, error) {
	return readFileLines(absPath, offset, limit, true)
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

		parentDir := filepath.Dir(absPath)
		dirsCreated, err := createMissingDirs(parentDir)
		if err != nil {
			return FileEditorResult{}, fmt.Errorf("failed to create parent directories: %w", err)
		}

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

// ListDirectory lists the contents of a directory, returning sorted entries.
// Directories are suffixed with "/". Returns an error if path is a file.
func ListDirectory(absPath string) (ListDirResult, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return ListDirResult{}, fmt.Errorf("cannot list directory: %w", err)
	}
	if !info.IsDir() {
		return ListDirResult{}, fmt.Errorf("path %q is a file, use read mode", absPath)
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return ListDirResult{}, fmt.Errorf("cannot list directory: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}

	sort.Strings(names)

	return ListDirResult{
		Path:    absPath,
		Entries: names,
	}, nil
}

func createMissingDirs(parentDir string) ([]string, error) {
	if parentDir == "." || parentDir == string(filepath.Separator) {
		return nil, nil
	}

	missing := make([]string, 0)
	current := parentDir
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("parent path %q exists and is not a directory", current)
			}
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		missing = append(missing, current)
		next := filepath.Dir(current)
		if next == current {
			break
		}
		current = next
	}

	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0755); err != nil {
			if os.IsExist(err) {
				continue
			}
			return nil, err
		}
	}

	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	return missing, nil
}

// EditResult holds the result of a surgical edit operation.
type EditResult struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// parseAnchor parses a "LINE:HASH" anchor string.
// Returns line number (1-indexed) and hash, or error.
func parseAnchor(anchor string) (int, string, error) {
	parts := strings.SplitN(anchor, ":", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("invalid anchor format %q, want LINE:HASH", anchor)
	}
	var line int
	if _, err := fmt.Sscanf(parts[0], "%d", &line); err != nil {
		return 0, "", fmt.Errorf("invalid line number in anchor %q: %w", anchor, err)
	}
	return line, parts[1], nil
}

// EditFile performs a surgical text replacement in a file.
// old is the exact text to find, new is the replacement.
// If anchor is non-empty, it must be a "LINE:HASH" anchor. When anchor is provided:
//   - The anchor line's hash must match the line content hash.
//   - The old text must be found within the anchored line (for single-line edits).
//
// Returns an error if old matches 0 or >1 times.
func EditFile(absPath, old, new, anchor string) (EditResult, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return EditResult{}, fmt.Errorf("cannot edit file: %w", err)
	}
	if info.IsDir() {
		return EditResult{}, fmt.Errorf("path %q is a directory", absPath)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return EditResult{}, fmt.Errorf("cannot read file: %w", err)
	}

	content := string(data)

	if anchor != "" {
		line, hash, err := parseAnchor(anchor)
		if err != nil {
			return EditResult{}, err
		}

		lines := strings.Split(content, "\n")
		if line < 1 || line > len(lines) {
			return EditResult{}, fmt.Errorf("anchor line %d exceeds file length %d", line, len(lines))
		}

		actualHash := LineHash(lines[line-1])
		if actualHash != hash {
			return EditResult{}, fmt.Errorf("anchor hash mismatch at line %d: got %q, want %q", line, actualHash, hash)
		}

		if !strings.Contains(lines[line-1], old) {
			return EditResult{}, fmt.Errorf("anchor text %q not found at line %d", old, line)
		}

		lines[line-1] = strings.Replace(lines[line-1], old, new, 1)
		newContent := strings.Join(lines, "\n")

		if err := os.WriteFile(absPath, []byte(newContent), 0644); err != nil {
			return EditResult{}, fmt.Errorf("failed to write file: %w", err)
		}

		return EditResult{Path: absPath, Old: old, New: new}, nil
	}

	// Without anchor, search whole file
	count := strings.Count(content, old)
	if count == 0 {
		return EditResult{}, fmt.Errorf("text %q not found in file (0 matches)", old)
	}
	if count > 1 {
		return EditResult{}, fmt.Errorf("text %q found %d times in file, expected exactly 1 match", old, count)
	}

	newContent := strings.Replace(content, old, new, 1)
	if err := os.WriteFile(absPath, []byte(newContent), 0644); err != nil {
		return EditResult{}, fmt.Errorf("failed to write file: %w", err)
	}

	return EditResult{Path: absPath, Old: old, New: new}, nil
}

// InsertLine inserts content as new line(s) after the anchor line.
// Anchor must be in "LINE:HASH" format.
// If anchor hash doesn't match the line content, returns error.
func InsertLine(absPath, anchor, content string) (FileEditorResult, error) {
	line, hash, err := parseAnchor(anchor)
	if err != nil {
		return FileEditorResult{}, err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return FileEditorResult{}, fmt.Errorf("cannot insert into file: %w", err)
	}
	if info.IsDir() {
		return FileEditorResult{}, fmt.Errorf("path %q is a directory", absPath)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return FileEditorResult{}, fmt.Errorf("cannot read file: %w", err)
	}

	if len(data) == 0 {
		if line != 1 {
			return FileEditorResult{}, fmt.Errorf("anchor line %d exceeds file length 0", line)
		}
		// Empty file: insert as content
		newContent := content
		if err := os.WriteFile(absPath, []byte(newContent), 0644); err != nil {
			return FileEditorResult{}, fmt.Errorf("failed to write file: %w", err)
		}
		return FileEditorResult{
			Path:         absPath,
			Mode:         "insert",
			BytesWritten: len([]byte(newContent)),
			NewContent:   newContent,
		}, nil
	}

	fileContent := string(data)
	lines := strings.Split(fileContent, "\n")

	if line < 1 || line > len(lines) {
		return FileEditorResult{}, fmt.Errorf("anchor line %d exceeds file length %d", line, len(lines))
	}

	actualHash := LineHash(lines[line-1])
	if actualHash != hash {
		return FileEditorResult{}, fmt.Errorf("anchor hash mismatch at line %d: got %q, want %q", line, actualHash, hash)
	}

	// Insert content after the anchor line
	var newLines []string
	newLines = append(newLines, lines[:line]...)
	newLines = append(newLines, strings.Split(content, "\n")...)
	if line < len(lines) {
		newLines = append(newLines, lines[line:]...)
	}
	newFileContent := strings.Join(newLines, "\n")

	// Preserve trailing newline if original had one
	if strings.HasSuffix(fileContent, "\n") && !strings.HasSuffix(newFileContent, "\n") {
		newFileContent += "\n"
	}

	if err := os.WriteFile(absPath, []byte(newFileContent), 0644); err != nil {
		return FileEditorResult{}, fmt.Errorf("failed to write file: %w", err)
	}

	return FileEditorResult{
		Path:         absPath,
		Mode:         "insert",
		BytesWritten: len([]byte(newFileContent)),
		NewContent:   newFileContent,
	}, nil
}
