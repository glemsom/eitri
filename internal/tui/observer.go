package tui

import (
	"bytes"
	"encoding/json"
	"os"
)

// DeltaObserver computes the file line-delta and before/after content a
// file-mutating tool call (edit/write) performed, entirely on the TUI side of
// the engine seam (issue #174). It is fed from the engine's tool-call event
// stream: snapshot the target file on tool-call start, diff it on tool result.
// The observer owns the file reading, behind an injected path-resolution seam,
// so unit tests drive it with a fake resolver and the app wires it to the
// registry's shared path translator. It is best-effort pure UI telemetry:
// unresolvable paths, read errors, and non-file tools degrade to a zero delta
// and no content, and it never affects the run or its message history.
type DeltaObserver struct {
	// resolve translates a tool-argument path to the host path to read. The
	// empty string means unresolvable (the observer reports zero delta and no
	// content). A nil seam degrades to unresolvable (fail-closed).
	resolve func(sandboxPath string) string
	// pending holds the pre-edit snapshot for each in-flight file-mutating tool
	// call, keyed by the provider-assigned tool_call id, so each result diffs
	// its own start.
	pending map[string]fileSnapshot
}

// fileSnapshot is the pre-edit state captured on tool-call start.
type fileSnapshot struct {
	path    string
	content string
	lines   int
}

// NewDeltaObserver builds a DeltaObserver that reads files at the host paths
// produced by resolve. A nil resolve degrades to unresolvable (zero delta, no
// content) — fail-closed, so a forgotten wiring never reads sandbox paths as
// host paths. The app always wires the registry-backed seam; nil only appears
// in tests that don't exercise real edits.
func NewDeltaObserver(resolve func(sandboxPath string) string) *DeltaObserver {
	if resolve == nil {
		resolve = func(string) string { return "" }
	}
	return &DeltaObserver{resolve: resolve, pending: map[string]fileSnapshot{}}
}

// Start snapshots the pre-edit state of an edit/write tool call's target file
// (issue #174). It resolves the tool's `path` argument through the injected
// seam and reads the file before the tool runs; a non-file tool, an
// unresolvable path, or a missing file leaves no pending snapshot (a zero delta
// is reported at Result).
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
	// A failed read degrades to empty content and a zero line count, mirroring
	// the removed engine seam: a write tool creating a brand-new file then
	// reports its full content as added.
	content, err := os.ReadFile(host)
	o.pending[id] = fileSnapshot{path: host, content: string(content), lines: countLines(content, err)}
}

// Result computes the added/removed line counts and the before/after full
// content + host path a file-mutating tool call performed, by diffing the
// snapshot taken at Start against the current on-disk file (issue #174). It
// backs the tool feed's `⊕ edit path [+N,-M]` tag and the card path's
// inline diff. Non-file tools, unmatched ids, and read
// errors degrade to zeros (best-effort telemetry, never a failure).
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

// countLines returns the number of lines in file content, honoring a read
// error: a failed or missing read yields zero (best-effort telemetry degrade,
// never a failure).
func countLines(data []byte, readErr error) int {
	if readErr != nil || len(data) == 0 {
		return 0
	}
	return bytes.Count(data, []byte{'\n'}) + 1
}
