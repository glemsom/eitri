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

// defaultRetention is the default maximum total size (1 GiB) for persisted data.
const defaultRetention = 1 * 1024 * 1024 * 1024

// iso8601Dashes is the time format used for timestamped filenames.
// Colons are replaced by dashes for cross-platform filesystem compatibility.
const iso8601Dashes = "2006-01-02T15-04-05"

// HistorySchema is the canonical JSON schema for history files.
// Deprecated: The snapshot file (session.json) is now the single source of truth.
// Kept for reading old-format data on disk.
type HistorySchema struct {
	Version      int           `json:"version"`
	SystemPrompt string        `json:"system_prompt"`
	Messages     []llm.Message `json:"messages"`
}

// RestoredState holds all data recovered from disk on startup.
type RestoredState struct {
	Sessions map[string]*session.UISession
	// Histories are derived from the session snapshot messages.
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

	// Create sessions directory tree
	sessionsDir := filepath.Join(rootDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		return nil, fmt.Errorf("cannot create sessions dir %s: %w", sessionsDir, err)
	}

	return p, nil
}

// TimelineSchema is the canonical JSON schema marker for timeline files.
// The actual schema is defined in the runstate package as runstate.Timeline.

// TimelineMeta holds metadata about a persisted timeline file.
type TimelineMeta struct {
	Filename  string    `json:"filename"`
	StartedAt time.Time `json:"started_at"`
	RunID     string    `json:"run_id"`
}

// ListTimelines returns metadata for all timeline files for a session.
func (p *Persister) ListTimelines(sessionID string) ([]TimelineMeta, error) {
	timelineDir := filepath.Join(p.rootDir, "sessions", sessionID, "timeline")
	entries, err := os.ReadDir(timelineDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot list timeline dir %s: %w", timelineDir, err)
	}

	var metas []TimelineMeta
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		// Quick parse: read just the run_id and started_at from the JSON file.
		path := filepath.Join(timelineDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue // skip unreadable files
		}
		var partial struct {
			RunID     string    `json:"run_id"`
			StartedAt time.Time `json:"started_at"`
		}
		if err := json.Unmarshal(data, &partial); err != nil {
			continue
		}
		metas = append(metas, TimelineMeta{
			Filename:  entry.Name(),
			StartedAt: partial.StartedAt,
			RunID:     partial.RunID,
		})
	}
	return metas, nil
}

// LoadTimeline reads and parses a single timeline file for a session.
func (p *Persister) LoadTimeline(sessionID, filename string) ([]byte, error) {
	path := filepath.Join(p.rootDir, "sessions", sessionID, "timeline", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read timeline file %s: %w", path, err)
	}
	return data, nil
}

// LoadSession reads the current session snapshot from disk.
// Returns nil, nil if no snapshot exists for the session.
// This is the replacement for LoadLatestSessionSnapshot and LoadLatestHistory.
func (p *Persister) LoadSession(sessionID string) ([]byte, error) {
	sessionFile := filepath.Join(p.rootDir, "sessions", sessionID, "session.json")
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read session file %s: %w", sessionFile, err)
	}
	return data, nil
}

// SessionInfo holds the metadata about a persisted session needed for listing.
type SessionInfo struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
	Messages  int       `json:"-"` // message count, derived from the full data
}

// LoadSessionInfo reads just the metadata of a session snapshot from disk
// without loading full message content. Returns nil, nil if no snapshot exists.
func (p *Persister) LoadSessionInfo(sessionID string) (*SessionInfo, error) {
	data, err := p.LoadSession(sessionID)
	if err != nil || data == nil {
		return nil, err
	}

	// Parse into a minimal struct that extracts only top-level fields
	// plus the message count.
	raw := struct {
		ID        string     `json:"id"`
		Title     string     `json:"title"`
		CreatedAt time.Time  `json:"created_at"`
		UpdatedAt time.Time  `json:"updated_at"`
		ClosedAt  *time.Time `json:"closed_at,omitempty"`
		Messages  []any      `json:"messages"`
	}{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("cannot parse session metadata: %w", err)
	}

	return &SessionInfo{
		ID:        raw.ID,
		Title:     raw.Title,
		CreatedAt: raw.CreatedAt,
		UpdatedAt: raw.UpdatedAt,
		ClosedAt:  raw.ClosedAt,
		Messages:  len(raw.Messages),
	}, nil
}

// ListTraces returns the filenames (without .json) of all trace files for a session.
func (p *Persister) ListTraces(sessionID string) ([]string, error) {
	tracesDir := filepath.Join(p.rootDir, "sessions", sessionID, "traces")
	entries, err := os.ReadDir(tracesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot list traces dir %s: %w", tracesDir, err)
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(entry.Name(), ".json"))
	}
	return ids, nil
}

// LoadTrace reads a single trace file for a session.
func (p *Persister) LoadTrace(sessionID, traceID string) ([]byte, error) {
	path := filepath.Join(p.rootDir, "sessions", sessionID, "traces", traceID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read trace file %s: %w", path, err)
	}
	return data, nil
}

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

// SnapshotSession writes the full session snapshot to disk as a single
// session.json file, overwritten atomically on each turn.
// The snapshot now carries all LLM-oriented fields (tool calls, etc.)
// so no separate history file is needed.
func (p *Persister) SnapshotSession(sessionID string, s *session.UISession) error {
	sessionDir := filepath.Join(p.rootDir, "sessions", sessionID)
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		return fmt.Errorf("cannot create session dir %s: %w", sessionDir, err)
	}

	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("cannot marshal session: %w", err)
	}

	sessionFile := filepath.Join(sessionDir, "session.json")
	if err := atomicWrite(sessionFile, data, 0600); err != nil {
		return fmt.Errorf("cannot write session file: %w", err)
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

// Flush writes any pending session snapshots, unpersisted HTTP traces to disk.
// It is intended for use during graceful shutdown to ensure no data is lost.
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
func (p *Persister) Flush(sessions []*session.UISession, traces []*debug.HTTPTrace) error {
	if p == nil {
		return nil
	}

	var flushErr error

	// Snapshot each session.
	for _, s := range sessions {
		if err := p.SnapshotSession(s.ID, s); err != nil {
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

// RootDir returns the root data directory of the persister.
func (p *Persister) RootDir() string {
	return p.rootDir
}

// DeleteSession removes all persisted data for a session from disk:
// <root>/sessions/<id>/.
// If the directory doesn't exist, the call is a no-op.
func (p *Persister) DeleteSession(sessionID string) error {
	dir := filepath.Join(p.rootDir, "sessions", sessionID)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ListOnDiskSessionIDs returns the session IDs (directory names) found under
// <root>/sessions/. Only directories are returned; the list is not sorted.
func (p *Persister) ListOnDiskSessionIDs() ([]string, error) {
	sessionsDir := filepath.Join(p.rootDir, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot list sessions dir %s: %w", sessionsDir, err)
	}

	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	return ids, nil
}

// DiskUsageBytes returns the total size in bytes of all files under
// <root>/sessions/. If the directory doesn't exist, returns 0.
func (p *Persister) DiskUsageBytes() (int64, error) {
	sessionsDir := filepath.Join(p.rootDir, "sessions")
	var total int64
	err := filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return nil
			}
			total += info.Size()
		}
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return total, err
}

// Prune scans sessions/ and history/ for total size. If total exceeds the
// retention cap (1 GiB by default), it removes the oldest timestamped
// snapshot files across all sessions until the cap is met. The latest
// snapshot (symlink target) for any session is never removed.
// Prune scans sessions/ for total size. If total exceeds the retention cap
// (1 GiB by default), it removes the oldest timeline and trace files until
// the cap is met. The session.json file for any session is never removed.
func (p *Persister) Prune() error {
	type candidateFile struct {
		path  string
		modTS time.Time
		size  int64
	}

	var candidates []candidateFile
	var totalSize int64

	baseDir := filepath.Join(p.rootDir, "sessions")

	err := filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() {
			return nil
		}

		name := d.Name()
		if !strings.HasSuffix(name, ".json") {
			return nil
		}

		// Protect session.json files — never remove the single source of truth.
		if name == "session.json" {
			info, err := d.Info()
			if err != nil {
				return nil
			}
			totalSize += info.Size()
			return nil
		}

		// Timeline and trace files are candidates for pruning.
		info, err := d.Info()
		if err != nil {
			return nil
		}
		candidates = append(candidates, candidateFile{
			path:  path,
			modTS: info.ModTime(),
			size:  info.Size(),
		})
		totalSize += info.Size()
		return nil
	})
	if err != nil {
		return err
	}

	if totalSize <= p.retention {
		return nil // under cap
	}

	// Sort by modification time (oldest first)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTS.Before(candidates[j].modTS)
	})

	// Remove oldest files until under cap
	overage := totalSize - p.retention
	for _, cf := range candidates {
		if overage <= 0 {
			break
		}
		if err := os.Remove(cf.path); err != nil {
			continue
		}
		overage -= cf.size
	}

	return nil
}

// sessionSnapshotPath resolves the path to the session.json file for a session.
// In the new single-file format, it's just <dir>/session.json.
// For backward compatibility, if session.json is a symlink (old format),
// it resolves the symlink to find the actual file.
func sessionSnapshotPath(sessionsDir, sessionID string) (string, error) {
	sessionFile := filepath.Join(sessionsDir, sessionID, "session.json")
	info, err := os.Lstat(sessionFile)
	if err != nil {
		return "", err
	}
	// If it's a symlink (old format), resolve it
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(sessionFile)
		if err != nil {
			return "", err
		}
		resolved := filepath.Join(sessionsDir, sessionID, target)
		return resolved, nil
	}
	return sessionFile, nil
}

// sessionMessagesToHistory converts session.Message slice to []llm.Message
// by extracting the LLM-relevant fields.
func sessionMessagesToHistory(msgs []session.Message) []llm.Message {
	result := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		llmMsg := llm.Message{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			ToolCalls:  m.ToolCalls,
		}
		result = append(result, llmMsg)
	}
	return result
}

// Restore reads all persisted session snapshots and HTTP traces from disk and
// returns them as a RestoredState struct.
// If no persisted data exists (first run), returns an empty RestoredState with no error.
// All restored sessions have Status set to StatusIdle regardless of what the snapshot says.
//
// The restored Histories are derived from the session snapshots' Messages field,
// which now contains all LLM-oriented fields.
//
// For backward compatibility, if old-format history data exists under history/
// and no session.json was found, it attempts to read from the old format.
func (p *Persister) Restore() (*RestoredState, error) {
	state := &RestoredState{
		Sessions:  make(map[string]*session.UISession),
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
		snapshotPath, err := sessionSnapshotPath(sessionsDir, sessionID)
		if err != nil {
			if os.IsNotExist(err) {
				// No session.json — try old-format history as fallback
				state.loadOldFormatHistory(sessionID, historyDir)
				continue
			}
			return nil, fmt.Errorf("cannot resolve session snapshot for %s: %w", sessionID, err)
		}

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

		// Derive history from session messages
		state.Histories[sessionID] = sessionMessagesToHistory(s.Messages)

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

	// Backward compatibility: scan history/ for sessions not yet migrated
	// to the new single-file format.
	state.loadOldFormatHistories(sessionsDir, historyDir)

	return state, nil
}

// loadOldFormatHistory reads a single session's history from the old-format
// history/ directory. Used as fallback when no session.json exists.
func (state *RestoredState) loadOldFormatHistory(sessionID, historyDir string) {
	historyLink := filepath.Join(historyDir, sessionID, "history.json")
	linkTarget, err := os.Readlink(historyLink)
	if err != nil {
		return // no old-format data either
	}
	histPath := filepath.Join(historyDir, sessionID, linkTarget)
	histData, err := os.ReadFile(histPath)
	if err != nil {
		return
	}

	var histSchema HistorySchema
	if err := json.Unmarshal(histData, &histSchema); err != nil {
		return
	}

	var messages []llm.Message
	if histSchema.SystemPrompt != "" {
		messages = append(messages, llm.Message{Role: "system", Content: histSchema.SystemPrompt})
	}
	messages = append(messages, histSchema.Messages...)
	state.Histories[sessionID] = messages
}

// loadOldFormatHistories scans history/ for sessions not yet in sessions/.
func (state *RestoredState) loadOldFormatHistories(sessionsDir, historyDir string) {
	historyEntries, err := os.ReadDir(historyDir)
	if err != nil {
		return // history/ doesn't exist or can't be read
	}
	for _, entry := range historyEntries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		// Only load if not already loaded from sessions/
		if _, exists := state.Sessions[sessionID]; exists {
			continue
		}
		state.loadOldFormatHistory(sessionID, historyDir)
	}
}

// timestampFilename generates an ISO8601 filename with dashes instead of colons.
func timestampFilename(ts time.Time) string {
	return ts.Format(iso8601Dashes) + ".json"
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
