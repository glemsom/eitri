package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/persist"
	"github.com/glemsom/eitri/internal/runner/loop"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
)

// unsetEnv removes an environment variable for the duration of a test,
// restoring the previous value (or absence) afterwards. Unlike t.Setenv it
// can express "variable unset" (vs "set to empty"), which matters for
// EITRI_BATCH_SESSION_ID where unset means default.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv(%s): %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

// batchTestPersister builds a Persister on a temp dir wired to the debug
// recorder exactly as cmd/eitri/main.go does in batch mode.
func batchTestPersister(t *testing.T, rec *debug.Recorder) *persist.Persister {
	t.Helper()
	p, err := persist.New(t.TempDir())
	if err != nil {
		t.Fatalf("persist.New: %v", err)
	}
	if rec != nil {
		rec.OnComplete = func(trace *debug.HTTPTrace) {
			p.SaveTraceAsync(trace.SessionID, trace)
		}
	}
	return p
}

// twoTurnLLM returns an httptest server driving a two-turn batch run: turn 1
// requests a grep tool call, turn 2 returns a plain answer. If onSecondRequest
// is non-nil it runs before the second response is written, so callers can
// inspect the on-disk snapshot at a deterministic mid-run point.
func twoTurnLLM(t *testing.T, onSecondRequest func()) *httptest.Server {
	t.Helper()
	var mu int
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu++
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		switch mu {
		case 1:
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"grep","arguments":"{\"pattern\":\"foo\"}"}}]},"finish_reason":"tool_calls"}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			if onSecondRequest != nil {
				onSecondRequest()
			}
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"done"},"index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	t.Cleanup(llm.Close)
	return llm
}

// singleTurnLLM returns an httptest server answering with a single plain-text
// assistant turn (no tool calls).
func singleTurnLLM(t *testing.T) *httptest.Server {
	t.Helper()
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"ok"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(llm.Close)
	return llm
}

func batchRunConfig(baseURL, workspace string) RunConfig {
	return RunConfig{
		ProviderID:  "opencode_go",
		BaseURL:     baseURL,
		APIKey:      "test-key",
		ModelName:   "test-model",
		Workspace:   workspace,
		MaxTurns:    5,
		RetryPolicy: loop.RetryPolicy{}, // zero: single attempt, no retry sleeps
	}
}

// TestBatchRun_PersistsSessionTrail verifies a successful batch run writes
// the full reviewable trail under ~/.eitri/sessions/<id>/: a per-turn
// session.json snapshot (rewritten after each complete agent turn with
// running status), a terminal snapshot with idle status, per-call HTTP
// traces, and a completed timeline — all in the existing UI session shape.
func TestBatchRun_PersistsSessionTrail(t *testing.T) {
	t.Setenv("EITRI_BATCH_SESSION_ID", "test-batch")
	workspace := t.TempDir()

	rec := debug.NewRecorder(20)
	persister := batchTestPersister(t, rec)
	svc := NewRunService(RunServiceDeps{
		HistorySessionMgr: history.NewSessionManager(50),
		DebugRecorder:     rec,
		Persister:         persister,
	})

	// Capture the snapshot at a deterministic mid-run point: the moment the
	// second LLM request arrives, turn 1 has completed (assistant tool call +
	// tool result appended) and the per-turn completion seam must have
	// persisted a running-status snapshot.
	var midRun *uisession.UISession
	llm := twoTurnLLM(t, func() {
		data, err := persister.LoadSession("test-batch")
		if err != nil || data == nil {
			t.Errorf("mid-run: LoadSession = %v, %v; want snapshot on disk", data, err)
			return
		}
		var s uisession.UISession
		if err := json.Unmarshal(data, &s); err != nil {
			t.Errorf("mid-run: unmarshal snapshot: %v", err)
			return
		}
		midRun = &s
	})

	cfg := batchRunConfig(llm.URL, workspace)
	content, err := svc.BatchRun(context.Background(), "hello world", cfg, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("batch run failed: %v", err)
	}
	if !strings.Contains(content, "done") {
		t.Fatalf("content = %q, want final assistant answer", content)
	}

	// ── Mid-run (per-turn) snapshot ──
	if midRun == nil {
		t.Fatal("mid-run snapshot was never captured")
	}
	if midRun.Status != uisession.StatusRunning {
		t.Errorf("mid-run status = %q, want %q", midRun.Status, uisession.StatusRunning)
	}
	if got := len(midRun.Messages); got != 3 {
		t.Fatalf("mid-run message count = %d, want 3 (user + assistant tool call + tool result)", got)
	}
	if midRun.Messages[1].Role != "assistant" || len(midRun.Messages[1].ToolCalls) != 1 {
		t.Errorf("mid-run second message = %+v, want assistant with one tool call", midRun.Messages[1])
	}
	if midRun.Messages[2].Role != "tool" || midRun.Messages[2].ToolCallID != "call_1" {
		t.Errorf("mid-run third message = %+v, want tool result for call_1", midRun.Messages[2])
	}

	// ── Terminal snapshot ──
	data, err := persister.LoadSession("test-batch")
	if err != nil || data == nil {
		t.Fatalf("LoadSession: %v, %v", data, err)
	}
	var final uisession.UISession
	if err := json.Unmarshal(data, &final); err != nil {
		t.Fatalf("unmarshal terminal snapshot: %v", err)
	}
	if final.ID != "test-batch" {
		t.Errorf("ID = %q, want %q", final.ID, "test-batch")
	}
	if final.Title != "hello world" {
		t.Errorf("Title = %q, want %q (derived from prompt)", final.Title, "hello world")
	}
	if final.Status != uisession.StatusIdle {
		t.Errorf("Status = %q, want %q", final.Status, uisession.StatusIdle)
	}
	if final.SystemPrompt == "" {
		t.Error("SystemPrompt is empty, want the assembled system prompt")
	}
	if final.Workspace != workspace {
		t.Errorf("Workspace = %q, want %q", final.Workspace, workspace)
	}
	if len(final.Messages) != 4 {
		t.Fatalf("terminal message count = %d, want 4 (user + tool call + tool result + final answer)", len(final.Messages))
	}
	if final.Messages[3].Role != "assistant" || final.Messages[3].Content != "done" {
		t.Errorf("final message = %+v, want assistant 'done'", final.Messages[3])
	}
	// Tool-call fields must survive the snapshot round-trip.
	tc := final.Messages[1].ToolCalls
	if len(tc) != 1 || tc[0].Function.Name != "grep" {
		t.Errorf("terminal snapshot tool calls = %+v, want grep", tc)
	}

	// The snapshot is loadable by the existing consumer (report/on-demand load).
	info, err := persister.LoadSessionInfo("test-batch")
	if err != nil {
		t.Fatalf("LoadSessionInfo: %v", err)
	}
	if info == nil || info.Title != "hello world" || info.Messages != 4 {
		t.Errorf("LoadSessionInfo = %+v, want title hello world, 4 messages", info)
	}

	// ── Traces (drained as main.go does on exit) ──
	if err := persister.Flush(nil, nil); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	traceIDs, err := persister.ListTraces("test-batch")
	if err != nil {
		t.Fatalf("ListTraces: %v", err)
	}
	if len(traceIDs) != 2 {
		t.Errorf("got %d persisted traces, want 2 (one per LLM call)", len(traceIDs))
	}
	traceData, err := persister.LoadTrace("test-batch", traceIDs[0])
	if err != nil || len(traceData) == 0 {
		t.Fatalf("LoadTrace: %v, %v", traceData, err)
	}

	// ── Timeline ──
	metas, err := persister.ListTimelines("test-batch")
	if err != nil {
		t.Fatalf("ListTimelines: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("got %d timeline(s), want 1", len(metas))
	}
	tlData, err := persister.LoadTimeline("test-batch", metas[0].Filename)
	if err != nil {
		t.Fatalf("LoadTimeline: %v", err)
	}
	var tl runstate.Timeline
	if err := json.Unmarshal(tlData, &tl); err != nil {
		t.Fatalf("unmarshal timeline: %v", err)
	}
	if tl.SessionID != "test-batch" {
		t.Errorf("timeline SessionID = %q, want %q", tl.SessionID, "test-batch")
	}
	if tl.Termination == nil || tl.Termination.Reason != runstate.TerminationCompleted {
		t.Errorf("timeline termination = %+v, want reason %q", tl.Termination, runstate.TerminationCompleted)
	}
	// The timeline must carry the turn↔trace correlation events (issue #988).
	var llmCalls, toolCalls int
	for _, evt := range tl.Events {
		switch evt.Type {
		case "llm_call":
			llmCalls++
			if evt.TraceID == "" {
				t.Errorf("llm_call event %+v missing trace_id", evt)
			}
		case "tool_call":
			toolCalls++
		}
	}
	if llmCalls != 2 {
		t.Errorf("timeline has %d llm_call events, want 2", llmCalls)
	}
	if toolCalls != 1 {
		t.Errorf("timeline has %d tool_call events, want 1", toolCalls)
	}
}

// TestBatchRun_FailurePathPersistsErrorSnapshotAndDrainsTraces verifies a
// failing batch run writes an error-status terminal snapshot and that the
// async trace queue is drained (as main.go's failure path now does) so no
// queued traces are dropped. The timeline records the error termination.
func TestBatchRun_FailurePathPersistsErrorSnapshotAndDrainsTraces(t *testing.T) {
	t.Setenv("EITRI_BATCH_SESSION_ID", "test-fail")
	workspace := t.TempDir()

	rec := debug.NewRecorder(20)
	persister := batchTestPersister(t, rec)
	svc := NewRunService(RunServiceDeps{
		HistorySessionMgr: history.NewSessionManager(50),
		DebugRecorder:     rec,
		Persister:         persister,
	})

	var reqs int
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs++
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		switch reqs {
		case 1:
			// First call succeeds with a tool call (records a trace and keeps
			// the loop going into a second turn).
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"grep","arguments":"{\"pattern\":\"foo\"}"}}]},"finish_reason":"tool_calls"}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			// Second call fails mid-run and records an error trace.
			http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
		}
	}))
	defer llm.Close()

	cfg := batchRunConfig(llm.URL, workspace)
	if _, err := svc.BatchRun(context.Background(), "hello", cfg, &bytes.Buffer{}); err == nil {
		t.Fatal("expected batch run to fail")
	}

	// Terminal snapshot must reflect the failure.
	data, err := persister.LoadSession("test-fail")
	if err != nil || data == nil {
		t.Fatalf("LoadSession: %v, %v", data, err)
	}
	var final uisession.UISession
	if err := json.Unmarshal(data, &final); err != nil {
		t.Fatalf("unmarshal terminal snapshot: %v", err)
	}
	if final.Status != uisession.StatusError {
		t.Errorf("Status = %q, want %q", final.Status, uisession.StatusError)
	}
	if len(final.Messages) != 3 {
		t.Errorf("message count = %d, want 3 (user + tool-call assistant + tool result)", len(final.Messages))
	}

	// Drain the queue exactly as main.go's failure path now does, then verify
	// both traces (the successful call and the failed call) reached disk.
	if err := persister.Flush(nil, nil); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	traceIDs, err := persister.ListTraces("test-fail")
	if err != nil {
		t.Fatalf("ListTraces: %v", err)
	}
	if len(traceIDs) != 2 {
		t.Errorf("got %d persisted traces, want 2 (both LLM calls)", len(traceIDs))
	}

	// Timeline records the error termination.
	metas, err := persister.ListTimelines("test-fail")
	if err != nil {
		t.Fatalf("ListTimelines: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("got %d timeline(s), want 1", len(metas))
	}
	tlData, err := persister.LoadTimeline("test-fail", metas[0].Filename)
	if err != nil {
		t.Fatalf("LoadTimeline: %v", err)
	}
	var tl runstate.Timeline
	if err := json.Unmarshal(tlData, &tl); err != nil {
		t.Fatalf("unmarshal timeline: %v", err)
	}
	if tl.Termination == nil || tl.Termination.Reason != runstate.TerminationError {
		t.Errorf("timeline termination = %+v, want reason %q", tl.Termination, runstate.TerminationError)
	}
}

// TestBatchRun_CancelledTermination verifies a context-cancelled batch run
// records the cancelled termination reason. The context is cancelled before
// the run starts so the agent loop aborts on its very first ctx.Err() check —
// deterministic, no hanging-stream timing involved.
func TestBatchRun_CancelledTermination(t *testing.T) {
	t.Setenv("EITRI_BATCH_SESSION_ID", "test-cancel")
	workspace := t.TempDir()

	rec := debug.NewRecorder(20)
	persister := batchTestPersister(t, rec)
	svc := NewRunService(RunServiceDeps{
		HistorySessionMgr: history.NewSessionManager(50),
		DebugRecorder:     rec,
		Persister:         persister,
	})

	cfg := batchRunConfig(unreachableURL(t), workspace)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.BatchRun(ctx, "hello", cfg, &bytes.Buffer{}); err == nil {
		t.Fatal("expected batch run to be cancelled")
	}

	metas, err := persister.ListTimelines("test-cancel")
	if err != nil {
		t.Fatalf("ListTimelines: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("got %d timeline(s), want 1", len(metas))
	}
	tlData, err := persister.LoadTimeline("test-cancel", metas[0].Filename)
	if err != nil {
		t.Fatalf("LoadTimeline: %v", err)
	}
	var tl runstate.Timeline
	if err := json.Unmarshal(tlData, &tl); err != nil {
		t.Fatalf("unmarshal timeline: %v", err)
	}
	if tl.Termination == nil || tl.Termination.Reason != runstate.TerminationCancelled {
		t.Errorf("timeline termination = %+v, want reason %q", tl.Termination, runstate.TerminationCancelled)
	}

	// The terminal snapshot reflects the failure with error status.
	data, err := persister.LoadSession("test-cancel")
	if err != nil || data == nil {
		t.Fatalf("LoadSession: %v, %v", data, err)
	}
	var final uisession.UISession
	if err := json.Unmarshal(data, &final); err != nil {
		t.Fatalf("unmarshal terminal snapshot: %v", err)
	}
	if final.Status != uisession.StatusError {
		t.Errorf("Status = %q, want %q", final.Status, uisession.StatusError)
	}
}

// TestBatchRun_MaxTurnsTermination verifies a batch run that exhausts its
// turn budget records the max_turns termination reason.
func TestBatchRun_MaxTurnsTermination(t *testing.T) {
	t.Setenv("EITRI_BATCH_SESSION_ID", "test-maxturns")
	workspace := t.TempDir()

	rec := debug.NewRecorder(20)
	persister := batchTestPersister(t, rec)
	svc := NewRunService(RunServiceDeps{
		HistorySessionMgr: history.NewSessionManager(50),
		DebugRecorder:     rec,
		Persister:         persister,
	})

	// Every turn requests another tool call, so the run always hits the cap.
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"grep","arguments":"{\"pattern\":\"foo\"}"}}]},"finish_reason":"tool_calls"}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer llm.Close()

	cfg := batchRunConfig(llm.URL, workspace)
	cfg.MaxTurns = 2
	if _, err := svc.BatchRun(context.Background(), "hello", cfg, &bytes.Buffer{}); err == nil {
		t.Fatal("expected batch run to exhaust max turns")
	}

	metas, err := persister.ListTimelines("test-maxturns")
	if err != nil {
		t.Fatalf("ListTimelines: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("got %d timeline(s), want 1", len(metas))
	}
	tlData, err := persister.LoadTimeline("test-maxturns", metas[0].Filename)
	if err != nil {
		t.Fatalf("LoadTimeline: %v", err)
	}
	var tl runstate.Timeline
	if err := json.Unmarshal(tlData, &tl); err != nil {
		t.Fatalf("unmarshal timeline: %v", err)
	}
	if tl.Termination == nil || tl.Termination.Reason != runstate.TerminationMaxTurns {
		t.Errorf("timeline termination = %+v, want reason %q", tl.Termination, runstate.TerminationMaxTurns)
	}
}

// TestBatchSessionID verifies the session ID resolution: default
// batch-<unixnano>, valid env override, and rejection of path separators and
// ".." in the override.
func TestBatchSessionID(t *testing.T) {
	t.Run("defaults to batch-unixnano", func(t *testing.T) {
		unsetEnv(t, "EITRI_BATCH_SESSION_ID")
		id, err := batchSessionID()
		if err != nil {
			t.Fatalf("batchSessionID: %v", err)
		}
		if !strings.HasPrefix(id, "batch-") {
			t.Errorf("id = %q, want batch- prefix", id)
		}
	})

	t.Run("valid override", func(t *testing.T) {
		t.Setenv("EITRI_BATCH_SESSION_ID", "issue-42")
		id, err := batchSessionID()
		if err != nil {
			t.Fatalf("batchSessionID: %v", err)
		}
		if id != "issue-42" {
			t.Errorf("id = %q, want %q", id, "issue-42")
		}
	})

	t.Run("explicitly empty override rejected", func(t *testing.T) {
		t.Setenv("EITRI_BATCH_SESSION_ID", "")
		if _, err := batchSessionID(); err == nil {
			t.Error("batchSessionID() with set-but-empty override succeeded, want validation error")
		}
	})

	for _, bad := range []string{"a/b", `a\b`, "..", "../escape", "a..b"} {
		t.Run("invalid "+bad, func(t *testing.T) {
			t.Setenv("EITRI_BATCH_SESSION_ID", bad)
			if _, err := batchSessionID(); err == nil {
				t.Errorf("batchSessionID(%q) succeeded, want validation error", bad)
			}
		})
	}
}

// TestBatchRun_InvalidSessionIDEnvRejected verifies BatchRun surfaces the
// validation error for an invalid EITRI_BATCH_SESSION_ID before any LLM call.
func TestBatchRun_InvalidSessionIDEnvRejected(t *testing.T) {
	t.Setenv("EITRI_BATCH_SESSION_ID", "a/b")
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", ModelName: "m", BaseURL: "http://127.0.0.1:1"}
	if _, err := svc.BatchRun(context.Background(), "hello", cfg, &bytes.Buffer{}); err == nil {
		t.Fatal("expected validation error for invalid EITRI_BATCH_SESSION_ID")
	} else if !strings.Contains(err.Error(), "EITRI_BATCH_SESSION_ID") {
		t.Fatalf("error = %v, want EITRI_BATCH_SESSION_ID validation error", err)
	}
}

// TestBatchTitle verifies title derivation uses the UI rule (session.TitlePreview)
// with a fallback to the session ID for blank prompts.
func TestBatchTitle(t *testing.T) {
	tests := []struct {
		name     string
		prompt   string
		fallback string
		want     string
	}{
		{"short prompt derives from prompt", "hello world", "batch-1", "hello world"},
		{"long prompt truncates with ellipsis", strings.Repeat("word ", 40), "batch-1", uisession.TitlePreview(strings.Repeat("word ", 40))},
		{"blank prompt falls back to session id", "   ", "batch-1", "batch-1"},
		{"empty prompt falls back to session id", "", "batch-1", "batch-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := batchTitle(tt.prompt, tt.fallback); got != tt.want {
				t.Errorf("batchTitle(%q, %q) = %q, want %q", tt.prompt, tt.fallback, got, tt.want)
			}
		})
	}
	if uisession.TitlePreview(strings.Repeat("word ", 40)) == strings.Repeat("word ", 40) {
		t.Fatal("test precondition: long prompt must actually be truncated")
	}
}

// TestBatchSession_RetentionInteraction verifies batch sessions follow the
// existing retention policy: session.json is never pruned while traces and
// timelines are evicted under the global cap.
func TestBatchSession_RetentionInteraction(t *testing.T) {
	t.Setenv("EITRI_BATCH_SESSION_ID", "test-retention")
	workspace := t.TempDir()

	rec := debug.NewRecorder(20)
	persister := batchTestPersister(t, rec)
	svc := NewRunService(RunServiceDeps{
		HistorySessionMgr: history.NewSessionManager(50),
		DebugRecorder:     rec,
		Persister:         persister,
	})

	llm := twoTurnLLM(t, nil)
	cfg := batchRunConfig(llm.URL, workspace)
	if _, err := svc.BatchRun(context.Background(), "hello", cfg, &bytes.Buffer{}); err != nil {
		t.Fatalf("batch run failed: %v", err)
	}
	// Drain the trace queue so the worker is done writing before we prune.
	if err := persister.Flush(nil, nil); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	snapshot := filepath.Join(persister.RootDir(), "sessions", "test-retention", "session.json")
	if _, err := os.Stat(snapshot); err != nil {
		t.Fatalf("snapshot missing before prune: %v", err)
	}
	if traces, _ := persister.ListTraces("test-retention"); len(traces) == 0 {
		t.Fatal("no traces persisted before prune")
	}
	if timelines, _ := persister.ListTimelines("test-retention"); len(timelines) == 0 {
		t.Fatal("no timeline persisted before prune")
	}

	// Force pruning with a 1-byte cap (the same technique the persister tests
	// use to exercise eviction).
	persister.SetRetention(1)
	if err := persister.Prune(); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	if _, err := os.Stat(snapshot); err != nil {
		t.Errorf("session.json was pruned, want snapshot to survive the cap")
	}
	if traces, _ := persister.ListTraces("test-retention"); len(traces) != 0 {
		t.Errorf("got %d traces after prune, want 0 (evicted under cap)", len(traces))
	}
	if timelines, _ := persister.ListTimelines("test-retention"); len(timelines) != 0 {
		t.Errorf("got %d timelines after prune, want 0 (evicted under cap)", len(timelines))
	}
}

// TestBatchTermination verifies the exit-path classification for the batch
// timeline matches UI behaviour (completed / cancelled / max-turns / error).
func TestBatchTermination(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		term := batchTermination(nil, context.Background())
		if term.Reason != runstate.TerminationCompleted {
			t.Errorf("reason = %q, want %q", term.Reason, runstate.TerminationCompleted)
		}
	})
	t.Run("cancelled via error", func(t *testing.T) {
		term := batchTermination(context.Canceled, context.Background())
		if term.Reason != runstate.TerminationCancelled {
			t.Errorf("reason = %q, want %q", term.Reason, runstate.TerminationCancelled)
		}
	})
	t.Run("cancelled via context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		term := batchTermination(errors.New("boom"), ctx)
		if term.Reason != runstate.TerminationCancelled {
			t.Errorf("reason = %q, want %q", term.Reason, runstate.TerminationCancelled)
		}
	})
	t.Run("max turns", func(t *testing.T) {
		term := batchTermination(&loop.MaxTurnsExceededError{Limit: 3}, context.Background())
		if term.Reason != runstate.TerminationMaxTurns {
			t.Errorf("reason = %q, want %q", term.Reason, runstate.TerminationMaxTurns)
		}
		if term.Message == "" {
			t.Error("message is empty, want max-turns message")
		}
	})
	t.Run("error", func(t *testing.T) {
		term := batchTermination(errors.New("boom"), context.Background())
		if term.Reason != runstate.TerminationError {
			t.Errorf("reason = %q, want %q", term.Reason, runstate.TerminationError)
		}
		if term.Message != "boom" {
			t.Errorf("message = %q, want %q", term.Message, "boom")
		}
	})
}
