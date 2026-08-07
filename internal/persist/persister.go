// Package persist provides disk persistence for session snapshots and
// HTTP traces under a root data directory. Old-format history files
// (HistorySchema) are still readable on startup for backward compatibility.
package persist

import (
	"encoding/json"
	"errors"
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
	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/timeline"
)

// defaultRetention is the default maximum total size (1 GiB) for persisted data.
const defaultRetention = 1 * 1024 * 1024 * 1024

// traceQueueSize is the maximum number of completed traces that may be queued
// for async persistence. If the queue is full, new traces are dropped — they
// remain in the debug recorder and the shutdown Flush re-scans and persists
// them, so nothing is lost.
const traceQueueSize = 256

// traceJob is a unit of work for the async trace-persistence worker.
type traceJob struct {
	sessionID string
	trace     *debug.HTTPTrace
}

// iso8601Dashes is the time format used for timestamped filenames.
// Colons are replaced by dashes for cross-platform filesystem compatibility.
const iso8601Dashes = "2006-01-02T15-04-05"

// HistorySchema is the canonical JSON schema for history files.
// Deprecated: The snapshot file (session.json) is now the single source of truth.
// Kept for reading old-format data on disk.
type HistorySchema struct {
	Version      int           `json:"version"`
	SystemPrompt string        `json:"system_prompt"`
	Messages     []message.Message `json:"messages"`
}

// RestoredState holds all data recovered from disk on startup.
type RestoredState struct {
	Sessions map[string]*session.UISession
	// Histories are derived directly from session snapshot messages (canonical type).
	Histories map[string][]message.Message // sessionID → conversation history with system prompt prepended
	Traces    []*debug.HTTPTrace
}

// ErrCorruptSnapshot indicates a session snapshot file existed on disk but
// could not be parsed as a valid session snapshot. Callers that degrade on
// unreadable data (e.g. the reconstructed report path) can distinguish this
// from hard I/O failures with errors.Is.
var ErrCorruptSnapshot = errors.New("persist: corrupt session snapshot")

// ErrTraceExists indicates SaveTrace was asked to write a trace whose ID is
// already owned by an existing trace file on disk. Persisted traces are
// single-owner (issue #1236): a saved trace is never overwritten, so a
// collision is a hard error rather than a silent clobber.
var ErrTraceExists = errors.New("persist: trace already exists on disk")

// Persister manages disk I/O for session snapshots, conversation history,
// and HTTP traces under a root data directory.
type Persister struct {
	rootDir         string
	retention       int64 // max total bytes before pruning; default 1 GiB
	mu              sync.Mutex
	persistedTraces map[debug.TraceID]bool // set of trace IDs already saved to disk

	// Async trace persistence: completed traces are handed to a worker via
	// traceQueue so disk I/O never blocks the HTTP response path or other
	// trace recording. Flush (shutdown) closes the queue and waits for the
	// worker to drain it.
	traceQueue  chan traceJob
	traceClosed bool // guarded by mu; when true the queue is closed
	workerDone  chan struct{}
}

// New creates a Persister rooted at the given directory. If rootDir is empty,
// it defaults to ~/.eitri/. On construction, it creates the required directory
// tree: <root>/sessions/.
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
		traceQueue:      make(chan traceJob, traceQueueSize),
		workerDone:      make(chan struct{}),
	}

	// Create sessions directory tree
	sessionsDir := filepath.Join(rootDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		return nil, fmt.Errorf("cannot create sessions dir %s: %w", sessionsDir, err)
	}

	go p.traceWorker()
	return p, nil
}

// traceWorker drains traceQueue, persisting each trace to disk, and exits once
// the queue is closed (by Flush on shutdown) after writing every queued trace.
func (p *Persister) traceWorker() {
	defer close(p.workerDone)
	for job := range p.traceQueue {
		if err := p.SaveTrace(job.sessionID, job.trace); err != nil {
			slog.Warn("failed to save trace",
				slog.String("trace_id", string(job.trace.ID)),
				slog.Any("error", err))
		}
	}
}

// SaveTraceAsync persists a completed trace to disk without blocking the
// caller. The trace is enqueued to a bounded worker queue; if the queue is
// full the trace is dropped and the shutdown Flush (which re-scans the
// recorder) persists it instead. After the queue has been closed (shutdown in
// progress) the trace is written synchronously so a straggler cannot lose
// data.
func (p *Persister) SaveTraceAsync(sessionID string, trace *debug.HTTPTrace) {
	if p == nil {
		return
	}

	p.mu.Lock()
	if p.traceClosed {
		p.mu.Unlock()
		// Shutdown drain in progress — fall back to a synchronous write.
		if err := p.SaveTrace(sessionID, trace); err != nil {
			slog.Warn("failed to save trace",
				slog.String("trace_id", string(trace.ID)),
				slog.Any("error", err))
		}
		return
	}

	select {
	case p.traceQueue <- traceJob{sessionID: sessionID, trace: trace}:
	default:
		// Queue full — drop now; the shutdown Flush re-scans the recorder and
		// persists any trace that was never written.
		slog.Warn("trace persistence queue is full; deferring to shutdown flush",
			slog.String("trace_id", string(trace.ID)))
	}
	p.mu.Unlock()
}

// drainTraceQueue closes the async trace queue and waits for the worker to
// write every queued trace. Called from Flush on shutdown. After the queue is
// closed, SaveTraceAsync falls back to synchronous writes so in-flight
// stragglers still reach disk.
func (p *Persister) drainTraceQueue() {
	p.mu.Lock()
	if !p.traceClosed {
		p.traceClosed = true
		close(p.traceQueue)
	}
	p.mu.Unlock()
	<-p.workerDone
}

// TimelineSchema is the canonical JSON schema marker for timeline files.
// The actual schema is defined in the timeline package as timeline.Timeline.

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
func (p *Persister) LoadTimeline(sessionID, filename string) (*timeline.Timeline, error) {
	path := filepath.Join(p.rootDir, "sessions", sessionID, "timeline", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read timeline file %s: %w", path, err)
	}
	var tl timeline.Timeline
	if err := json.Unmarshal(data, &tl); err != nil {
		return nil, fmt.Errorf("cannot parse timeline file %s: %w", path, err)
	}
	return &tl, nil
}

// sessionExistsOnDisk reports whether the session has a session.json snapshot
// on disk. A session whose session.json is gone has been permanently deleted:
// this is the single owner of that check, used by the snapshot loader and every
// trace save / flush / query site (issue #1237).
func (p *Persister) sessionExistsOnDisk(sessionID string) bool {
	_, err := os.Stat(filepath.Join(p.rootDir, "sessions", sessionID, "session.json"))
	return err == nil
}

// readSessionSnapshot returns the raw bytes of a session's session.json
// snapshot file. Returns nil, nil when no snapshot exists for the session.
func (p *Persister) readSessionSnapshot(sessionID string) ([]byte, error) {
	if !p.sessionExistsOnDisk(sessionID) {
		return nil, nil
	}
	sessionFile := filepath.Join(p.rootDir, "sessions", sessionID, "session.json")
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		return nil, fmt.Errorf("cannot read session file %s: %w", sessionFile, err)
	}
	return data, nil
}

// LoadSession reads and parses the current session snapshot from disk.
// Returns nil, nil if no snapshot exists for the session. A snapshot file that
// exists but cannot be parsed returns an error wrapping ErrCorruptSnapshot.
func (p *Persister) LoadSession(sessionID string) (*session.UISession, error) {
	data, err := p.readSessionSnapshot(sessionID)
	if err != nil || data == nil {
		return nil, err
	}
	var snap session.UISession
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("cannot parse session snapshot for %s: %w: %v", sessionID, ErrCorruptSnapshot, err)
	}
	return &snap, nil
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
	data, err := p.readSessionSnapshot(sessionID)
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

// traceFilename returns the on-disk filename for a trace file with the given ID.
func traceFilename(id string) string {
	return id + ".json"
}

// ListTraceFilenames returns the stems (IDs, without the .json suffix) of every
// trace file on disk for a session, without reading or parsing them. Unlike
// ListTraces it includes unreadable or corrupt files, so callers that operate
// on raw files (e.g. clear-all-traces cleanup) see everything on disk.
// Returns nil, nil when the session has no traces directory.
func (p *Persister) ListTraceFilenames(sessionID string) ([]string, error) {
	tracesDir := filepath.Join(p.rootDir, "sessions", sessionID, "traces")
	entries, err := os.ReadDir(tracesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot list traces dir %s: %w", tracesDir, err)
	}
	var stems []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		stems = append(stems, strings.TrimSuffix(entry.Name(), ".json"))
	}
	return stems, nil
}

// ListTraces reads and parses all trace files for a session, in filename
// order. Trace files that cannot be read or parsed are skipped so one corrupt
// file never hides the rest. To enumerate every trace file on disk including
// unreadable ones, use ListTraceFilenames.
func (p *Persister) ListTraces(sessionID string) ([]*debug.HTTPTrace, error) {
	stems, err := p.ListTraceFilenames(sessionID)
	if err != nil {
		return nil, err
	}
	var traces []*debug.HTTPTrace
	for _, stem := range stems {
		trace, err := p.LoadTrace(sessionID, stem)
		if err != nil {
			continue // skip unreadable or corrupt trace files
		}
		traces = append(traces, trace)
	}
	return traces, nil
}

// LoadTrace reads and parses a single trace file for a session.
func (p *Persister) LoadTrace(sessionID, traceID string) (*debug.HTTPTrace, error) {
	path := filepath.Join(p.rootDir, "sessions", sessionID, "traces", traceFilename(traceID))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read trace file %s: %w", path, err)
	}
	var trace debug.HTTPTrace
	if err := json.Unmarshal(data, &trace); err != nil {
		return nil, fmt.Errorf("cannot parse trace file %s: %w", path, err)
	}
	return &trace, nil
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
//
// If the session has been permanently deleted (no session.json on disk),
// SaveTrace is a no-op — it returns nil without writing anything. This
// prevents both shutdown Flush and asynchronous OnComplete callbacks from
// recreating deleted session directories.
//
// Trace identity is single-owner (issue #1236): a trace file already on disk
// is never overwritten. If <trace_id>.json exists, SaveTrace returns
// ErrTraceExists without touching it — a freshly generated ID that collides
// with a restored archive file after a restart must not silently clobber the
// archived trace.
func (p *Persister) SaveTrace(sessionID string, trace *debug.HTTPTrace) error {
	// If session.json is gone the session was permanently deleted — do not
	// recreate the directory by writing a trace.
	if !p.sessionExistsOnDisk(sessionID) {
		return nil
	}

	sessionDir := filepath.Join(p.rootDir, "sessions", sessionID)
	tracesDir := filepath.Join(sessionDir, "traces")
	if err := os.MkdirAll(tracesDir, 0700); err != nil {
		return fmt.Errorf("cannot create traces dir %s: %w", tracesDir, err)
	}

	data, err := json.Marshal(trace)
	if err != nil {
		return fmt.Errorf("cannot marshal trace: %w", err)
	}

	traceFile := filepath.Join(tracesDir, traceFilename(string(trace.ID)))
	if _, err := os.Stat(traceFile); err == nil {
		return ErrTraceExists
	}
	if err := atomicWrite(traceFile, data, 0600); err != nil {
		return fmt.Errorf("cannot write trace file: %w", err)
	}

	// Mark this trace as persisted so Flush can skip it.
	p.mu.Lock()
	p.persistedTraces[trace.ID] = true
	p.mu.Unlock()

	return nil
}

// ClearAllTraces removes every trace file for a session — including unreadable
// or corrupt ones, which ListTraces skips — and returns the number of files
// removed. It is the cleanup counterpart to ListTraceFilenames: both operate on
// raw filenames so nothing on disk survives a clear.
func (p *Persister) ClearAllTraces(sessionID string) (int, error) {
	stems, err := p.ListTraceFilenames(sessionID)
	if err != nil {
		return 0, err
	}
	cleared := 0
	for _, stem := range stems {
		traceFile := filepath.Join(p.rootDir, "sessions", sessionID, "traces", traceFilename(stem))
		if err := os.Remove(traceFile); err == nil {
			cleared++
		}
	}
	return cleared, nil
}

// Flush writes any pending session snapshots, unpersisted HTTP traces to disk.
// It is intended for use during graceful shutdown to ensure no data is lost.
//
// It first drains the async trace-persistence queue so every trace enqueued by
// SaveTraceAsync is written, then handles the remaining work below.
//
// For each session, a final snapshot is always written (the write is cheap
// and guarantees consistency).
// For each trace, SaveTrace is called only if the trace has not already been
// persisted (e.g. via the OnComplete callback). In-flight traces are always
// persisted.
//
// Traces belonging to permanently deleted sessions are skipped: if a trace's
// session is not in the provided sessions list and its session.json is gone
// from disk, the session was permanently deleted and its trace data is discarded.
//
// If flush fails for any individual item, the error is logged but Flush
// continues with remaining items (best-effort). A combined error is returned
// if any failures occurred.
func (p *Persister) Flush(sessions []*session.UISession, traces []*debug.HTTPTrace) error {
	if p == nil {
		return nil
	}

	// Wait for the async trace worker to write every queued trace before
	// scanning recorder traces below, so each trace is persisted exactly once.
	p.drainTraceQueue()

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
	// Skip traces for sessions that have been permanently deleted
	// (not in live list and no session.json on disk).
	liveIDs := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		liveIDs[s.ID] = true
	}

	for _, trace := range traces {
		p.mu.Lock()
		alreadyPersisted := p.persistedTraces[trace.ID]
		p.mu.Unlock()

		if alreadyPersisted {
			continue
		}
		// If the session isn't live and its session.json is gone,
		// the session was permanently deleted — skip its traces.
		if !liveIDs[trace.SessionID] && !p.sessionExistsOnDisk(trace.SessionID) {
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

// SetRetention sets the maximum total bytes (across all sessions) retained on
// disk before Prune evicts the oldest trace and timeline files. Tests use it
// to exercise eviction without writing gigabytes; production keeps the
// default 1 GiB cap.
func (p *Persister) SetRetention(maxBytes int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.retention = maxBytes
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

// Restore reads all persisted session snapshots and HTTP traces from disk and
// returns them as a RestoredState struct.
// If no persisted data exists (first run), returns an empty RestoredState with no error.
// All restored sessions have Status set to StatusIdle regardless of what the snapshot says.
//
// The restored Histories are derived directly from the session snapshots' Messages
// field (canonical message.Message type). No conversion is needed since all consumers
// use the canonical type.
//
// For backward compatibility, if old-format history data exists under history/
// and no session.json was found, it attempts to read from the old format.
func (p *Persister) Restore() (*RestoredState, error) {
	state := &RestoredState{
		Sessions:  make(map[string]*session.UISession),
		Histories: make(map[string][]message.Message),
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

		// Derive history from session messages (canonical type, no conversion needed)
		state.Histories[sessionID] = s.Messages

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

			// Restored traces are already owned by their on-disk files: mark
			// them persisted so the shutdown Flush never re-writes them after
			// a restart (single-owner trace identity, issue #1236).
			p.mu.Lock()
			p.persistedTraces[trace.ID] = true
			p.mu.Unlock()
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

	var messages []message.Message
	if histSchema.SystemPrompt != "" {
		messages = append(messages, message.Message{Role: "system", Content: histSchema.SystemPrompt})
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
