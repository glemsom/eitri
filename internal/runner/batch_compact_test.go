// Tests for shared auto-compaction across UI and batch parent runs (issue
// #1093): batch runs compact their conversation history with the same
// thresholds, salience ordering, and tool-call retention as UI runs, and the
// compacted history is reflected in the batch session snapshot on disk.

package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/runner/loop"
	uisession "github.com/glemsom/eitri/internal/session"
)

// batchCompactLLMServer returns an httptest server that serves both the batch
// agent run and the compactor's summarization calls against the same URL.
// Agent requests get SSE-streamed responses (turn 1 asks to read a large
// file, later turns answer), while requests carrying the compactor's
// "Summarize" prompt get a plain JSON completion with the canned summary.
// onTurn2 fires when the second agent request arrives — after turn 1 has
// completed and the shared auto-compaction step has run, so callers can
// inspect the request body and the on-disk snapshot at a deterministic
// post-compaction point.
func batchCompactLLMServer(t *testing.T, onTurn2 func(turn2Body string)) *httptest.Server {
	t.Helper()
	var mu int
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		body := string(bodyBytes)

		if strings.Contains(body, "Summarize") {
			// Compactor summarization request → non-streaming JSON completion.
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"chatcmpl-compact","object":"chat.completion","created":123,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"large file of repeated characters"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
			return
		}

		mu++
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		switch mu {
		case 1:
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"huge.txt\"}"}}]},"finish_reason":"tool_calls"}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			if onTurn2 != nil {
				onTurn2(body)
			}
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"done"},"index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	t.Cleanup(llm.Close)
	return llm
}

// writeHugeFile writes a workspace file large enough to blow past any sane
// high-water mark: 100 lines of 5000 characters (~500 KB → ~125k estimated
// tokens at the default 4.0 chars/token).
func writeHugeFile(t *testing.T, workspace string) string {
	t.Helper()
	line := strings.Repeat("x", 5000)
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	path := filepath.Join(workspace, "huge.txt")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write huge.txt: %v", err)
	}
	return line[:64]
}

// hasCompactedMessage reports whether any message in the list carries the
// compactor's replacement marker.
func hasCompactedMessage(msgs []message.Message) bool {
	for i := range msgs {
		if strings.HasPrefix(msgs[i].Content, "[TOOL RESULT COMPACTED") ||
			strings.HasPrefix(msgs[i].Content, "[MESSAGE COMPACTED") {
			return true
		}
	}
	return false
}

// TestBatchRun_AutoCompaction verifies that a batch run whose history exceeds
// the configured high-water mark compacts it with the same thresholds and
// salience ordering as UI runs: the huge tool result is replaced by a compact
// summary before the next LLM request, and the session.json snapshot reflects
// the compacted history on disk (issue #1093).
func TestBatchRun_AutoCompaction(t *testing.T) {
	t.Setenv("EITRI_BATCH_SESSION_ID", "test-batch-compact")
	workspace := t.TempDir()
	bigLinePrefix := writeHugeFile(t, workspace)

	rec := debug.NewRecorder(20)
	persister := batchTestPersister(t, rec)
	svc := NewRunService(RunServiceDeps{
		HistorySessionMgr: history.NewSessionManager(50),
		DebugRecorder:     rec,
		Persister:         persister,
	})

	var turn2Body string
	var midRun *uisession.UISession
	llm := batchCompactLLMServer(t, func(body string) {
		turn2Body = body
		data, err := persister.LoadSession("test-batch-compact")
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
	cfg.CompactionEnabled = true
	cfg.CompactionThresholdPercent = 50
	cfg.CompactionLowWaterPercent = 10
	cfg.ContextWindowTokens = 1000
	cfg.CompactionMessageSizeThreshold = 1000 // only the huge tool result qualifies
	cfg.CompactionToolCallRetentionTurns = 5
	cfg.CompactionSalienceEnabled = true

	content, err := svc.BatchRun(context.Background(), "read the huge file", cfg, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("batch run failed: %v", err)
	}
	if !strings.Contains(content, "done") {
		t.Fatalf("content = %q, want final assistant answer", content)
	}

	// Turn 2's LLM request must be built from the compacted history: the huge
	// tool output is gone, replaced by a compacted marker.
	if turn2Body == "" {
		t.Fatal("turn-2 request body was never captured")
	}
	if strings.Contains(turn2Body, bigLinePrefix) {
		t.Error("turn-2 request still contains the huge tool output; history was not compacted")
	}
	if !strings.Contains(turn2Body, "[TOOL RESULT COMPACTED") {
		t.Errorf("turn-2 request missing compacted tool result (body %d bytes)", len(turn2Body))
	}

	// The on-disk snapshot at turn-2 time must already reflect the compacted
	// history (the per-turn snapshot runs before compaction, the re-snapshot
	// after it — both before the next turn's request).
	if midRun == nil {
		t.Fatal("mid-run snapshot was never captured")
	}
	if !hasCompactedMessage(midRun.Messages) {
		t.Errorf("mid-run snapshot has no compacted message; got %d messages", len(midRun.Messages))
	}

	// The terminal snapshot also reflects the compacted history.
	data, err := persister.LoadSession("test-batch-compact")
	if err != nil || data == nil {
		t.Fatalf("LoadSession: %v, %v", data, err)
	}
	var final uisession.UISession
	if err := json.Unmarshal(data, &final); err != nil {
		t.Fatalf("unmarshal terminal snapshot: %v", err)
	}
	if !hasCompactedMessage(final.Messages) {
		t.Errorf("terminal snapshot has no compacted message; got %d messages", len(final.Messages))
	}
}

// TestBatchRun_NoAutoCompactionWhenDisabled verifies the compaction_enabled
// setting is honored in batch mode: with compaction disabled, a run that
// blows past the context window keeps its full tool output in the next LLM
// request and in the snapshot.
func TestBatchRun_NoAutoCompactionWhenDisabled(t *testing.T) {
	t.Setenv("EITRI_BATCH_SESSION_ID", "test-batch-nocompact")
	workspace := t.TempDir()
	bigLinePrefix := writeHugeFile(t, workspace)

	rec := debug.NewRecorder(20)
	persister := batchTestPersister(t, rec)
	svc := NewRunService(RunServiceDeps{
		HistorySessionMgr: history.NewSessionManager(50),
		DebugRecorder:     rec,
		Persister:         persister,
	})

	var turn2Body string
	llm := batchCompactLLMServer(t, func(body string) {
		turn2Body = body
	})

	cfg := batchRunConfig(llm.URL, workspace)
	cfg.CompactionEnabled = false // default — batch runs never compact
	cfg.CompactionThresholdPercent = 50
	cfg.ContextWindowTokens = 1000

	if _, err := svc.BatchRun(context.Background(), "read the huge file", cfg, &bytes.Buffer{}); err != nil {
		t.Fatalf("batch run failed: %v", err)
	}
	if !strings.Contains(turn2Body, bigLinePrefix) {
		t.Error("turn-2 request lost the tool output despite compaction being disabled")
	}
	if strings.Contains(turn2Body, "[TOOL RESULT COMPACTED") {
		t.Error("turn-2 request contains a compacted marker despite compaction being disabled")
	}
	data, err := persister.LoadSession("test-batch-nocompact")
	if err != nil || data == nil {
		t.Fatalf("LoadSession: %v, %v", data, err)
	}
	var final uisession.UISession
	if err := json.Unmarshal(data, &final); err != nil {
		t.Fatalf("unmarshal terminal snapshot: %v", err)
	}
	if hasCompactedMessage(final.Messages) {
		t.Error("terminal snapshot contains compacted messages despite compaction being disabled")
	}
}

// TestAutoCompactAfterTurn_SkipConditions verifies the shared helper is a
// no-op when compaction is disabled, the context window is unset, or the
// history is at or below the high-water mark.
func TestAutoCompactAfterTurn_SkipConditions(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	sessionMgr := history.NewSessionManager(50)
	sessionMgr.Create("s1")
	sessionMgr.SetSystemPrompt("s1", "You are a test.")
	sessionMgr.AppendUser("s1", "hello")
	historyMgr := loop.NewSessionHistoryManager(sessionMgr, "s1")

	t.Run("disabled", func(t *testing.T) {
		cfg := compactRunConfig(unreachableURL(t))
		cfg.CompactionEnabled = false
		compacted, count, freed, pruned, err := svc.autoCompactAfterTurn(context.Background(), historyMgr, cfg)
		if err != nil {
			t.Fatalf("autoCompactAfterTurn: %v", err)
		}
		if compacted != nil || count != 0 || freed != 0 || pruned != 0 {
			t.Errorf("got (%v, %d, %d, %d), want (nil, 0, 0, 0)", compacted != nil, count, freed, pruned)
		}
	})

	t.Run("no context window", func(t *testing.T) {
		cfg := compactRunConfig(unreachableURL(t))
		cfg.CompactionEnabled = true
		cfg.ContextWindowTokens = 0
		compacted, _, _, _, err := svc.autoCompactAfterTurn(context.Background(), historyMgr, cfg)
		if err != nil {
			t.Fatalf("autoCompactAfterTurn: %v", err)
		}
		if compacted != nil {
			t.Error("compaction ran with no context window, want no-op")
		}
	})

	t.Run("below high-water", func(t *testing.T) {
		cfg := compactRunConfig(unreachableURL(t))
		compacted, count, freed, pruned, err := svc.autoCompactAfterTurn(context.Background(), historyMgr, cfg)
		if err != nil {
			t.Fatalf("autoCompactAfterTurn: %v", err)
		}
		if compacted != nil || count != 0 || freed != 0 || pruned != 0 {
			t.Errorf("got (%v, %d, %d, %d), want (nil, 0, 0, 0)", compacted != nil, count, freed, pruned)
		}
	})
}

// TestAutoCompactAfterTurn_CompactsAndRestoresHistory verifies the shared
// helper replaces the session manager's history with the compacted version
// when the high-water mark is exceeded, exactly as UI auto-compaction does.
func TestAutoCompactAfterTurn_CompactsAndRestoresHistory(t *testing.T) {
	fakeLLM := fakeCompactLLMServer(t, "summary")
	svc := NewRunService(RunServiceDeps{
		HistorySessionMgr: history.NewSessionManager(50),
	})
	sessionMgr := history.NewSessionManager(50)
	sessionMgr.Create("s1")
	sessionMgr.SetSystemPrompt("s1", "You are a test.")
	sessionMgr.AppendUser("s1", "run build")
	sessionMgr.AppendAssistant("s1", "let me look", []message.ToolCall{
		{ID: "call_1", Function: message.FunctionCall{Name: "read", Arguments: `{"path":"x"}`}},
	})
	sessionMgr.AppendTool("s1", "call_1", strings.Repeat("Build output with detail\n", 200), "", false)

	cfg := compactRunConfig(fakeLLM.URL)
	cfg.ContextWindowTokens = 2000
	cfg.CompactionThresholdPercent = 50 // high-water 1000 < ~1250-token tool result
	cfg.CompactionLowWaterPercent = 30

	historyMgr := loop.NewSessionHistoryManager(sessionMgr, "s1")
	compacted, count, freed, pruned, err := svc.autoCompactAfterTurn(context.Background(), historyMgr, cfg)
	if err != nil {
		t.Fatalf("autoCompactAfterTurn: %v", err)
	}
	if compacted == nil {
		t.Fatal("expected compaction to run, got nil")
	}
	if count == 0 && pruned == 0 {
		t.Fatal("expected at least one compacted message")
	}
	if freed <= 0 {
		t.Errorf("expected freed > 0, got %d", freed)
	}

	// The history manager must now serve the compacted history.
	hist := sessionMgr.History("s1")
	if hist == nil {
		t.Fatal("history is nil after compaction")
	}
	found := false
	for _, em := range hist {
		if strings.HasPrefix(em.Content(), "[TOOL RESULT COMPACTED") ||
			strings.HasPrefix(em.Content(), "[MESSAGE COMPACTED") {
			found = true
			break
		}
	}
	if !found {
		t.Error("history has no compacted message after auto-compaction")
	}
}

// hasMessageContent reports whether any message in the list contains the given
// substring in its content.
func hasMessageContent(msgs []message.Message, substr string) bool {
	for i := range msgs {
		if strings.Contains(msgs[i].Content, substr) {
			return true
		}
	}
	return false
}

// TestSubAgentTurnCompletion_Compaction verifies sub-agent turn completion
// now runs the shared auto-compaction step (issue #1096): with a
// compaction-enabled config inherited from the parent run config and a
// request-based history that exceeds the high-water mark, the snapshotter
// rewrites the run's request history with the compacted version (via the
// replace-history seam) and writes a follow-up snapshot reflecting the
// compacted history.
func TestSubAgentTurnCompletion_Compaction(t *testing.T) {
	fakeLLM := fakeCompactLLMServer(t, "build summary")
	rec := debug.NewRecorder(20)
	persister := batchTestPersister(t, rec)
	svc := NewRunService(RunServiceDeps{
		DebugRecorder: rec,
		Persister:     persister,
	})

	huge := strings.Repeat("Build output with detail\n", 200) // ~1250 tokens
	req := &litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: litellm.RoleSystem, Blocks: []litellm.Block{litellm.TextBlock{Text: "You are a test."}}},
			{Role: litellm.RoleUser, Blocks: []litellm.Block{litellm.TextBlock{Text: "run build"}}},
			{Role: litellm.Role("tool"), Blocks: []litellm.Block{litellm.TextBlock{Text: huge}}},
		},
	}

	cfg := compactRunConfig(fakeLLM.URL)
	cfg.ContextWindowTokens = 1000
	cfg.CompactionThresholdPercent = 50 // high-water 500 < ~1250-token tool result
	cfg.CompactionLowWaterPercent = 10

	sn := &runCompleter{
		svc:        svc,
		historyMgr: loop.NewRequestHistoryManager(req),
		id:         "sub-compact",
		parentID:   "parent-1",
		title:      "task",
		workspace:  cfg.Workspace,
		startedAt:  time.Now(),
		cfg:        cfg,
	}

	sn.OnTurnComplete(context.Background(), "sub-compact")

	// The run's request history must now carry the compacted marker and no
	// longer contain the full tool output (ReplaceHistory wrote it back).
	foundCompacted, foundHuge := false, false
	for _, lm := range req.Messages {
		for _, b := range lm.Blocks {
			if tb, ok := b.(litellm.TextBlock); ok {
				if strings.Contains(tb.Text, "[TOOL RESULT COMPACTED") {
					foundCompacted = true
				}
				if strings.Contains(tb.Text, huge[:64]) {
					foundHuge = true
				}
			}
			if tr, ok := b.(litellm.ToolResultBlock); ok {
				for _, c := range tr.Content {
					if tb, ok := c.(litellm.TextBlock); ok {
						if strings.Contains(tb.Text, "[TOOL RESULT COMPACTED") {
							foundCompacted = true
						}
						if strings.Contains(tb.Text, huge[:64]) {
							foundHuge = true
						}
					}
				}
			}
		}
	}
	if !foundCompacted {
		t.Error("sub-agent request history has no compacted marker, want compaction to rewrite it")
	}
	if foundHuge {
		t.Error("sub-agent request history still contains the full tool output")
	}

	// The follow-up snapshot reflects the compacted history.
	sess := loadSubAgentSessionSnapshot(t, persister, "sub-compact")
	if sess == nil {
		t.Fatal("sub-agent snapshot missing")
	}
	if !hasCompactedMessage(sess.Messages) {
		t.Error("sub-agent snapshot has no compacted message, want the follow-up snapshot to reflect compaction")
	}
	if hasMessageContent(sess.Messages, huge[:64]) {
		t.Error("sub-agent snapshot still contains the full tool output")
	}
}

// TestSubAgentTurnCompletion_NoCompactionWhenDisabled verifies the
// compaction_enabled setting is honored for sub-agents: with compaction
// disabled, sub-agent turn completion leaves the request history and the
// snapshot untouched.
func TestSubAgentTurnCompletion_NoCompactionWhenDisabled(t *testing.T) {
	rec := debug.NewRecorder(20)
	persister := batchTestPersister(t, rec)
	svc := NewRunService(RunServiceDeps{
		DebugRecorder: rec,
		Persister:     persister,
	})

	huge := strings.Repeat("Build output with detail\n", 200) // ~1250 tokens
	req := &litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: litellm.RoleSystem, Blocks: []litellm.Block{litellm.TextBlock{Text: "You are a test."}}},
			{Role: litellm.RoleUser, Blocks: []litellm.Block{litellm.TextBlock{Text: "run build"}}},
			{Role: litellm.Role("tool"), Blocks: []litellm.Block{litellm.TextBlock{Text: huge}}},
		},
	}

	cfg := compactRunConfig(unreachableURL(t))
	cfg.CompactionEnabled = false
	cfg.ContextWindowTokens = 1000
	cfg.CompactionThresholdPercent = 50

	sn := &runCompleter{
		svc:        svc,
		historyMgr: loop.NewRequestHistoryManager(req),
		id:         "sub-nocompact",
		parentID:   "parent-1",
		title:      "task",
		workspace:  cfg.Workspace,
		startedAt:  time.Now(),
		cfg:        cfg,
	}

	sn.OnTurnComplete(context.Background(), "sub-nocompact")

	// The run's request history is untouched — no compaction markers.
	for _, lm := range req.Messages {
		for _, b := range lm.Blocks {
			if tb, ok := b.(litellm.TextBlock); ok && strings.Contains(tb.Text, "[TOOL RESULT COMPACTED") {
				t.Error("sub-agent request history was compacted despite compaction being disabled")
			}
		}
	}

	// The persisted snapshot carries the full history, not a compacted one.
	sess := loadSubAgentSessionSnapshot(t, persister, "sub-nocompact")
	if sess == nil {
		t.Fatal("sub-agent snapshot missing")
	}
	if hasCompactedMessage(sess.Messages) {
		t.Error("sub-agent snapshot contains compacted messages despite compaction being disabled")
	}
	if !hasMessageContent(sess.Messages, huge[:64]) {
		t.Error("sub-agent snapshot lost the full tool output, want unchanged history")
	}
}

// TestSubAgentRun_AutoCompaction is the end-to-end check for sub-agent
// auto-compaction (issue #1096): a sub-agent whose history exceeds the
// configured high-water mark compacts it to below the low-water mark mid-run
// — verified in the next LLM request and in the mid-run + terminal snapshots
// — with the compaction settings inherited from the parent run config.
func TestSubAgentRun_AutoCompaction(t *testing.T) {
	workspace := t.TempDir()
	bigLinePrefix := writeHugeFile(t, workspace)

	var taskID string
	var turn2Body string
	var midRun *uisession.UISession
	persister, rec := newSubAgentPersistWiring(t)
	llm := batchCompactLLMServer(t, func(body string) {
		turn2Body = body
		data, err := persister.LoadSession(taskID)
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

	svc := NewRunService(RunServiceDeps{
		DebugRecorder: rec,
		Persister:     persister,
	})

	cfg := batchRunConfig(llm.URL, workspace)
	cfg.CompactionEnabled = true
	cfg.CompactionThresholdPercent = 50
	cfg.CompactionLowWaterPercent = 10
	cfg.ContextWindowTokens = 1000
	cfg.CompactionMessageSizeThreshold = 1000 // only the huge tool result qualifies
	cfg.CompactionToolCallRetentionTurns = 5
	cfg.CompactionSalienceEnabled = true
	svc.subagents.StoreParentCfg("parent-1", cfg)

	var err error
	taskID, err = svc.SpawnSubAgent(context.Background(), "parent-1", "read the huge file", 5, "")
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}
	waitForSubAgentDone(t, svc, taskID)

	// Turn 2's LLM request must be built from the compacted history: the huge
	// tool output is gone, replaced by a compacted marker.
	if turn2Body == "" {
		t.Fatal("turn-2 request body was never captured")
	}
	if strings.Contains(turn2Body, bigLinePrefix) {
		t.Error("turn-2 request still contains the huge tool output; history was not compacted")
	}
	if !strings.Contains(turn2Body, "[TOOL RESULT COMPACTED") {
		t.Errorf("turn-2 request missing compacted tool result (body %d bytes)", len(turn2Body))
	}

	// The on-disk snapshot at turn-2 time must already reflect the compacted
	// history (the per-turn snapshot runs before compaction, the re-snapshot
	// after it — both before the next turn's request).
	if midRun == nil {
		t.Fatal("mid-run snapshot was never captured")
	}
	if !hasCompactedMessage(midRun.Messages) {
		t.Errorf("mid-run snapshot has no compacted message; got %d messages", len(midRun.Messages))
	}

	// The terminal snapshot also reflects the compacted history, and the child
	// session trail shape is unchanged (snapshot keyed by task ID).
	data, err := persister.LoadSession(taskID)
	if err != nil || data == nil {
		t.Fatalf("LoadSession(%s): %v, %v", taskID, data, err)
	}
	var final uisession.UISession
	if err := json.Unmarshal(data, &final); err != nil {
		t.Fatalf("unmarshal terminal snapshot: %v", err)
	}
	if final.ID != taskID {
		t.Errorf("terminal snapshot ID = %q, want %q", final.ID, taskID)
	}
	if !hasCompactedMessage(final.Messages) {
		t.Errorf("terminal snapshot has no compacted message; got %d messages", len(final.Messages))
	}

	// The sub-agent result reflects the compacted history: it is derived from
	// the request history, which compaction rewrote.
	rec2 := svc.subagents.getRecord(taskID)
	if rec2 == nil {
		t.Fatalf("sub-agent record for %s not found", taskID)
	}
	if rec2.Status != subAgentCompleted {
		t.Errorf("record Status = %q, want %q", rec2.Status, subAgentCompleted)
	}
	if !strings.Contains(rec2.Result, "done") {
		t.Errorf("sub-agent result = %q, want final assistant answer", rec2.Result)
	}
}
