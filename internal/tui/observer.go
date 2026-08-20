package tui

import (
	"bytes"
	"encoding/json"
	"os"
)

// DeltaObserver computes the file line-delta and before/after content a file-mutating tool call (edit/write) performed, entirely on the TUI side of the engine seam.
type DeltaObserver struct {
	resolve func(sandboxPath string) string
	pending map[string]fileSnapshot
}

// fileSnapshot is the pre-edit state captured on tool-call start.
type fileSnapshot struct {
	path    string
	content string
	lines   int
}

// NewDeltaObserver builds a DeltaObserver that reads files at the host paths produced by resolve.
func NewDeltaObserver(resolve func(sandboxPath string) string) *DeltaObserver {
	if resolve == nil {
		resolve = func(string) string { return "" }
	}
	return &DeltaObserver{resolve: resolve, pending: map[string]fileSnapshot{}}
}

// Start snapshots the pre-edit state of an edit/write tool call's target file.
func (o *DeltaObserver) Start(id, name, argsJSON string) {
	if name != "edit" && name != "write" {
		return
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil || args.Path == "" {
		return
	}
	host := o.resolve(args.Path)
	if host == "" {
		return
	}
	content, err := os.ReadFile(host)
	o.pending[id] = fileSnapshot{path: host, content: string(content), lines: countLines(content, err)}
}

// Result computes the added/removed line counts and the before/after full content + host path a file-mutating tool call performed, by diffing the snapshot taken at Start against the current on-disk file.
func (o *DeltaObserver) Result(id, name string) (added, removed int, before, after, path string) {
	if name != "edit" && name != "write" {
		return 0, 0, "", "", ""
	}
	snap, ok := o.pending[id]
	if !ok {
		return 0, 0, "", "", ""
	}
	delete(o.pending, id)
	data, err := os.ReadFile(snap.path)
	if err != nil {
		return 0, 0, "", "", ""
	}
	newLines := countLines(data, nil)
	if newLines > snap.lines {
		added = newLines - snap.lines
	} else if newLines < snap.lines {
		removed = snap.lines - newLines
	}
	return added, removed, snap.content, string(data), snap.path
}

// countLines returns the number of lines in file content, honoring a read error: a failed or missing read yields zero (best-effort telemetry degrade, never a failure).
func countLines(data []byte, readErr error) int {
	if readErr != nil || len(data) == 0 {
		return 0
	}
	return bytes.Count(data, []byte{'\n'}) + 1
}
