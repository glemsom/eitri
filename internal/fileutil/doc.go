// Package fileutil provides workspace-scoped file operations for the agent's
// built-in file tools (read, write, edit, grep, glob) and directory listing.
//
// It owns file path validation (preventing directory traversal outside the
// workspace), file reading with line-hash anchors, text editing (insert/replace),
// file writing with atomic rename, directory listing, and workspace tree walking.
//
// Key types:
//   - FileViewerResult — result of reading a file (content, truncation info)
//   - FileEditorResult — result of writing/editing a file
//   - ListDirResult — result of listing a directory
//   - WalkStop — sentinel error to abort WalkWorkspace early
//
// Key functions:
//   - ValidateWorkspacePath / ValidatePathWithAllowed — path safety checks
//   - ReadFile — read file at offset with line hash
//   - EditFile — replace text anchored by line hash
//   - InsertLine — insert text anchored by line hash
//   - WriteFile — write file with atomic rename
//   - ListDirectory — list directory entries
//   - WalkWorkspace — walk workspace tree with callback
//
// Dependencies: none (stdlib only)
//
// Extension points:
//   - Add new file operation functions following the same validation pattern
//   - Adjust defaultReadLimit for larger default file reads
package fileutil
