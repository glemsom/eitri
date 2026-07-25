// Package persist provides disk persistence for session snapshots,
// LLM conversation history, and HTTP traces under a root data directory.
package persist

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/llm"
	"github.com/glemsom/eitri/internal/session"
)

// iso8601Dashes is the time format used for timestamped filenames.
// Colons are replaced by dashes for cross-platform filesystem compatibility.
const iso8601Dashes = "2006-01-02T15-04-05"

// defaultRetention is the default maximum total size (1 GiB) for persisted data.
const defaultRetention = 1 * 1024 * 1024 * 1024

// HistorySchema is the canonical JSON schema for history files.
type HistorySchema struct {
	Version      int           `json:"version"`
	SystemPrompt string        `json:"system_prompt"`
	Messages     []llm.Message `json:"messages"`
}

// RestoredState holds all data recovered from disk on startup.
type RestoredState struct {
	Sessions map[string]*session.UISession
	Histories map[string][]llm.Message // sessionID → conversation history with system prompt prepended
	Traces    []*debug.HTTPTrace
}

// Persister manages disk I/O for session snapshots, conversation history,
// and HTTP traces under a root data directory.
type Persister struct {
	rootDir         string
	retention       int64 // max total bytes before pruning; default 1 GiB
	mu              sync.Mutex
	persistedTraces map[debug.TraceID]bool // set of trace IDs already saved to disk
}

// New creates a Persister rooted at the given directory. If rootDir is empty,
// it defaults to ~/.eitri/. On construction, it creates the required directory
// tree: <root>/sessions/ and <root>/history/.
func New(rootDir string) (*Persister, error) {
	if rootDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot determine home directory: %w", err)
		}
		rootDir = filepath.Join(home, ".eitri")
	}

	p := &Persister{
		rootDir:         rootDir,
		retention:       defaultRetention,
		persistedTraces: make(map[debug.TraceID]bool),
	}

	// Create directory tree
	for _, dir := range []string{"sessions", "history"} {
		path := filepath.Join(rootDir, dir)
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, fmt.Errorf("cannot create %s: %w", path, err)
		}
	}

	return p, nil
}

// TimelineSchema is the canonical JSON schema marker for timeline files.
// The actual schema is defined in the runstate package as runstate.Timeline.

// SaveTimeline writes a condensed timeline for a single run to disk.
// The file is written to <root>/sessions/<sessionID>/timeline/<filename>.json
// atomically via temp-file + rename.
func (p *Persister) SaveTimeline(sessionID string, timeline any) error {
	timelineDir := filepath.Join(p.rootDir, "sessions", sessionID, "timeline")
	if err := os.MkdirAll(timelineDir, 0700); err != nil {
		return fmt.Errorf("cannot create timeline dir %s: %w", timelineDir, err)
	}

	data, err := json.Marshal(timeline)
	if err != nil {
		return fmt.Errorf("cannot marshal timeline: %w", err)
	}

	// Use start time as filename base. The timeline is passed from the caller
	// and we use the started_at field for the filename.
	var startedAt struct {
		StartedAt time.Time `json:"started_at"`
	}
	if err := json.Unmarshal(data, &startedAt); err != nil {
		return fmt.Errorf("cannot extract started_at from timeline: %w", err)
	}

	filename := timestampFilename(startedAt.StartedAt)
	timelineFile := filepath.Join(timelineDir, filename)

	if err := atomicWrite(timelineFile, data, 0600); err != nil {
		return fmt.Errorf("cannot write timeline file: %w", err)
	}

	return nil
}

// SnapshotSession writes a full session snapshot and its LLM conversation
// history to disk. Both are written as timestamped JSON files with atomic
// symlink updates pointing to the latest version.
func (p *Persister) SnapshotSession(sessionID string, s *session.UISession, history []llm.Message) error {
	ts := time.Now().UTC()
	filename := timestampFilename(ts)

	// --- Session snapshot ---
	sessionDir := filepath.Join(p.rootDir, "sessions", sessionID)
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		return fmt.Errorf("cannot create session dir %s: %w", sessionDir, err)
	}

	sessionData, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("cannot marshal session: %w", err)
	}

	sessionFile := filepath.Join(sessionDir, filename)
	if err := atomicWrite(sessionFile, sessionData, 0600); err != nil {
		return fmt.Errorf("cannot write session file: %w", err)
	}

	// Update symlink: <sessionDir>/session.json -> filename
	sessionLink := filepath.Join(sessionDir, "session.json")
	if err := updateSymlink(sessionLink, filename); err != nil {
		return fmt.Errorf("cannot update session symlink: %w", err)
	}

	// --- History snapshot ---
	historyDir := filepath.Join(p.rootDir, "history", sessionID)
	if err := os.MkdirAll(historyDir, 0700); err != nil {
		return fmt.Errorf("cannot create history dir %s: %w", historyDir, err)
	}

	schema := buildHistorySchema(history)
	histData, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("cannot marshal history: %w", err)
	}

	historyFile := filepath.Join(historyDir, filename)
	if err := atomicWrite(historyFile, histData, 0600); err != nil {
		return fmt.Errorf("cannot write history file: %w", err)
	}

	// Update symlink: <historyDir>/history.json -> filename
	historyLink := filepath.Join(historyDir, "history.json")
	if err := updateSymlink(historyLink, filename); err != nil {
		return fmt.Errorf("cannot update history symlink: %w", err)
	}

	return nil
}

// SaveTrace writes a single HTTP trace to disk as a JSON file.
// The file is written to <root>/sessions/<sessionID>/traces/<trace_id>.json.
func (p *Persister) SaveTrace(sessionID string, trace *debug.HTTPTrace) error {
	tracesDir := filepath.Join(p.rootDir, "sessions", sessionID, "traces")
	if err := os.MkdirAll(tracesDir, 0700); err != nil {
		return fmt.Errorf("cannot create traces dir %s: %w", tracesDir, err)
	}

	data, err := json.Marshal(trace)
	if err != nil {
		return fmt.Errorf("cannot marshal trace: %w", err)
	}

	traceFile := filepath.Join(tracesDir, string(trace.ID)+".json")
	if err := atomicWrite(traceFile, data, 0600); err != nil {
		return fmt.Errorf("cannot write trace file: %w", err)
	}

	// Mark this trace as persisted so Flush can skip it.
	p.mu.Lock()
	p.persistedTraces[trace.ID] = true
	p.mu.Unlock()

	return nil
}

// Flush writes any pending session snapshots, conversation histories, and
// unpersisted HTTP traces to disk. It is intended for use during graceful
// shutdown to ensure no data is lost.
//
// For each session, a final snapshot is always written (the write is cheap
// and guarantees consistency).
// For each trace, SaveTrace is called only if the trace has not already been
// persisted (e.g. via the OnComplete callback). In-flight traces are always
// persisted.
//
// If flush fails for any individual item, the error is logged but Flush
// continues with remaining items (best-effort). A combined error is returned
// if any failures occurred.
func (p *Persister) Flush(sessions []*session.UISession, histories map[string][]llm.Message, traces []*debug.HTTPTrace) error {
	if p == nil {
		return nil
	}

	var flushErr error

	// Snapshot each session and its history.
	for _, s := range sessions {
		// Look up history for this session; may be nil/empty.
		hist := histories[s.ID]
		if hist == nil {
			hist = []llm.Message{}
		}
		if err := p.SnapshotSession(s.ID, s, hist); err != nil {
			slog.Warn("flush: failed to snapshot session",
				slog.String("session_id", s.ID),
				slog.Any("error", err))
			if flushErr == nil {
				flushErr = err
			}
		}
	}

	// Persist any traces not yet saved to disk.
	for _, trace := range traces {
		p.mu.Lock()
		alreadyPersisted := p.persistedTraces[trace.ID]
		p.mu.Unlock()

		if alreadyPersisted {
			continue
		}
		if err := p.SaveTrace(trace.SessionID, trace); err != nil {
			slog.Warn("flush: failed to save trace",
				slog.String("trace_id", string(trace.ID)),
				slog.Any("error", err))
			if flushErr == nil {
				flushErr = err
			}
		}
	}

	return flushErr
}

// DeleteSession removes all persisted data for a session from disk:
// <root>/sessions/<id>/ and <root>/history/<id>/.
// If the directories don't exist, the call is a no-op.
func (p *Persister) DeleteSession(sessionID string) error {
	var firstErr error

	for _, dir := range []string{
		filepath.Join(p.rootDir, "sessions", sessionID),
		filepath.Join(p.rootDir, "history", sessionID),
	} {
		if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

// Prune scans sessions/ and history/ for total size. If total exceeds the
// retention cap (1 GiB by default), it removes the oldest timestamped
// snapshot files across all sessions until the cap is met. The latest
// snapshot (symlink target) for any session is never removed.
func (p *Persister) Prune() error {
	type snapshotFile struct {
		path  string
		modTS time.Time
	}

	var allSnapshots []snapshotFile
	protected := make(map[string]bool) // paths that must not be deleted

	// Collect all timestamped snapshot files under sessions/ and history/
	// and identify protected files (symlink targets).
	baseDirs := []string{
		filepath.Join(p.rootDir, "sessions"),
		filepath.Join(p.rootDir, "history"),
	}

	for _, base := range baseDirs {
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// Skip if directory doesn't exist
				if os.IsNotExist(err) {
					return filepath.SkipDir
				}
				return err
			}
			if d.IsDir() {
				return nil
			}

			name := d.Name()
			// Skip symlinks (session.json, history.json) and non-JSON files
			if !strings.HasSuffix(name, ".json") {
				return nil
			}
			if name == "session.json" || name == "history.json" {
				// Resolve the symlink target and protect it
				target, err := os.Readlink(path)
				if err == nil {
					targetPath := filepath.Join(filepath.Dir(path), target)
					protected[targetPath] = true
				}
				return nil
			}

			// It's a timestamped snapshot file
			info, err := d.Info()
			if err != nil {
				return err
			}
			allSnapshots = append(allSnapshots, snapshotFile{
				path:  path,
				modTS: info.ModTime(),
			})
			return nil
		})
		if err != nil {
			return err
		}
	}

	// Calculate total size
	var totalSize int64
	for _, sf := range allSnapshots {
		info, err := os.Stat(sf.path)
		if err != nil {
			continue
		}
		totalSize += info.Size()
	}

	if totalSize <= p.retention {
		return nil // under cap
	}

	// Sort by modification time (oldest first)
	sort.Slice(allSnapshots, func(i, j int) bool {
		return allSnapshots[i].modTS.Before(allSnapshots[j].modTS)
	})

	// Remove oldest files until under cap, skipping protected ones
	overage := totalSize - p.retention
	for _, sf := range allSnapshots {
		if overage <= 0 {
			break
		}
		if protected[sf.path] {
			continue
		}
		info, err := os.Stat(sf.path)
		if err != nil {
			continue
		}
		size := info.Size()
		if err := os.Remove(sf.path); err != nil {
			continue
		}
		overage -= size
	}

	return nil
}

// Restore reads all persisted session snapshots, conversation histories, and
// HTTP traces from disk and returns them as a RestoredState struct.
// If no persisted data exists (first run), returns an empty RestoredState with no error.
// All restored sessions have Status set to StatusIdle regardless of what the snapshot says.
func (p *Persister) Restore() (*RestoredState, error) {
	state := &RestoredState{
		Sessions: make(map[string]*session.UISession),
		Histories: make(map[string][]llm.Message),
		Traces:    make([]*debug.HTTPTrace, 0),
	}

	sessionsDir := filepath.Join(p.rootDir, "sessions")
	historyDir := filepath.Join(p.rootDir, "history")

	// Walk sessions/ directory to find all session IDs
	sessionEntries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil // first run, nothing to restore
		}
		return nil, fmt.Errorf("cannot list sessions dir %s: %w", sessionsDir, err)
	}

	for _, entry := range sessionEntries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()

		// --- Restore session snapshot ---
		sessionLink := filepath.Join(sessionsDir, sessionID, "session.json")
		linkTarget, err := os.Readlink(sessionLink)
		if err != nil {
			if os.IsNotExist(err) {
				continue // no snapshot for this session yet
			}
			return nil, fmt.Errorf("cannot read symlink %s: %w", sessionLink, err)
		}

		snapshotPath := filepath.Join(sessionsDir, sessionID, linkTarget)
		data, err := os.ReadFile(snapshotPath)
		if err != nil {
			return nil, fmt.Errorf("cannot read snapshot %s: %w", snapshotPath, err)
		}

		var s session.UISession
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, fmt.Errorf("cannot unmarshal session snapshot %s: %w", snapshotPath, err)
		}

		// Force status to idle — no half-running state on recovery
		s.Status = session.StatusIdle

		state.Sessions[sessionID] = &s

		// --- Restore conversation history ---
		historyLink := filepath.Join(historyDir, sessionID, "history.json")
		histLinkTarget, err := os.Readlink(historyLink)
		if err != nil {
			if os.IsNotExist(err) {
				continue // no history for this session yet
			}
			return nil, fmt.Errorf("cannot read history symlink %s: %w", historyLink, err)
		}

		histPath := filepath.Join(historyDir, sessionID, histLinkTarget)
		histData, err := os.ReadFile(histPath)
		if err != nil {
			return nil, fmt.Errorf("cannot read history file %s: %w", histPath, err)
		}

		var histSchema HistorySchema
		if err := json.Unmarshal(histData, &histSchema); err != nil {
			return nil, fmt.Errorf("cannot unmarshal history %s: %w", histPath, err)
		}

		// Reconstruct the full message list with system prompt prepended
		var messages []llm.Message
		if histSchema.SystemPrompt != "" {
			messages = append(messages, llm.Message{Role: "system", Content: histSchema.SystemPrompt})
		}
		messages = append(messages, histSchema.Messages...)
		state.Histories[sessionID] = messages

		// --- Restore HTTP traces ---
		tracesDir := filepath.Join(sessionsDir, sessionID, "traces")
		traceEntries, err := os.ReadDir(tracesDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue // no traces for this session
			}
			return nil, fmt.Errorf("cannot list traces dir %s: %w", tracesDir, err)
		}

		for _, traceEntry := range traceEntries {
			if traceEntry.IsDir() || !strings.HasSuffix(traceEntry.Name(), ".json") {
				continue
			}
			tracePath := filepath.Join(tracesDir, traceEntry.Name())
			traceData, err := os.ReadFile(tracePath)
			if err != nil {
				return nil, fmt.Errorf("cannot read trace file %s: %w", tracePath, err)
			}
			var trace debug.HTTPTrace
			if err := json.Unmarshal(traceData, &trace); err != nil {
				return nil, fmt.Errorf("cannot unmarshal trace %s: %w", tracePath, err)
			}
			state.Traces = append(state.Traces, &trace)
		}
	}

	return state, nil
}

// timestampFilename generates an ISO8601 filename with dashes instead of colons.
func timestampFilename(ts time.Time) string {
	return ts.Format(iso8601Dashes) + ".json"
}

// buildHistorySchema converts an []llm.Message slice (where the first message
// may be a system prompt) into a HistorySchema with separate system_prompt
// and messages fields.
func buildHistorySchema(messages []llm.Message) HistorySchema {
	schema := HistorySchema{
		Version:  1,
		Messages: make([]llm.Message, 0),
	}

	if len(messages) == 0 {
		return schema
	}

	// Extract system prompt if first message has role "system"
	if messages[0].Role == "system" {
		schema.SystemPrompt = messages[0].Content
		schema.Messages = messages[1:]
	} else {
		schema.Messages = messages
	}

	return schema
}

// atomicWrite writes data to a file atomically: write to a temp file in the
// same directory, then rename to the target path. This prevents partial writes.
func atomicWrite(targetPath string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(targetPath)
	base := filepath.Base(targetPath)
	tmpFile, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return fmt.Errorf("cannot create temp file in %s: %w", dir, err)
	}
	tmpPath := tmpFile.Name()

	// Ensure cleanup on failure
	removed := false
	defer func() {
		if !removed {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("cannot write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("cannot close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("cannot chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("cannot rename temp file to %s: %w", targetPath, err)
	}
	removed = true
	return nil
}

// updateSymlink atomically updates a symlink to point to a new target.
// It creates a temporary symlink and renames it over the existing one.
func updateSymlink(linkPath, target string) error {
	// If the symlink already exists, remove it first (os.Rename doesn't work
	// across different types, and we can't atomically swap symlinks on Linux).
	// We use a temp symlink in the same directory and rename.
	dir := filepath.Dir(linkPath)
	tmpLink := filepath.Join(dir, fmt.Sprintf(".%s.tmp-symlink", filepath.Base(linkPath)))

	// Clean up any leftover temp symlink
	os.Remove(tmpLink)

	if err := os.Symlink(target, tmpLink); err != nil {
		return fmt.Errorf("cannot create temp symlink: %w", err)
	}

	if err := os.Rename(tmpLink, linkPath); err != nil {
		os.Remove(tmpLink) // best-effort cleanup
		return fmt.Errorf("cannot rename symlink: %w", err)
	}

	return nil
}
