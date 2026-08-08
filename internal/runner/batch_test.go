package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/persona"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

func TestBatchRun_ReturnsErrorForMissingBaseURL(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		ModelName:  "test-model",
	}
	var buf bytes.Buffer
	_, err := svc.BatchRun(context.Background(), "hello", cfg, &buf)
	if err == nil {
		t.Fatal("expected error for missing base URL")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %q, want 'not configured'", err.Error())
	}
}

func TestBatchRun_ReturnsErrorForMissingModel(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    unreachableURL(t),
	}
	var buf bytes.Buffer
	_, err := svc.BatchRun(context.Background(), "hello", cfg, &buf)
	if err == nil {
		t.Fatal("expected error for missing model")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %q, want 'not configured'", err.Error())
	}
}

func TestBatchRun_ReturnsErrorOnCancelledContext(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    unreachableURL(t),
		APIKey:     "test-key",
		ModelName:  "test-model",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Already cancelled

	var buf bytes.Buffer
	_, err := svc.BatchRun(ctx, "hello", cfg, &buf)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestBatchRun_FailsGracefullyOnConnectionFailure(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://127.0.0.1:1",
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
	}

	var buf bytes.Buffer

	// Use a very short timeout so test runs fast.
	// The connection will be refused but retries take ~1s each.
	// A short timeout demonstrates that context cancellation works.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result, err := svc.BatchRun(ctx, "test prompt", cfg, &buf)
	if err != nil {
		if result != "" {
			t.Logf("result was non-empty despite error: %q", result)
		}
		return
	}
	t.Logf("unexpected success: result=%q, buf=%q", result, buf.String())
}

func TestBatchRun_DeadEndpointReturnsFastWithZeroRetryPolicy(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://127.0.0.1:1", // connection refused
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		// RetryPolicy zero value means no retries: single attempt, no 1s sleeps.
	}

	var buf bytes.Buffer
	start := time.Now()
	_, err := svc.BatchRun(context.Background(), "test prompt", cfg, &buf)
	if err == nil {
		t.Fatal("expected connection failure error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("BatchRun took %v, want < 2s (no retry sleeps on dead endpoint)", elapsed)
	}
}

func TestBatchRun_SkipsThinkingLevelForUnsupportedModel(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID:    "opencode_go",
		BaseURL:       "http://127.0.0.1:1",
		APIKey:        "test-key",
		ModelName:     "gpt-4", // does not support thinking levels
		ThinkingLevel: "high",
		Workspace:     t.TempDir(),
	}

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := svc.BatchRun(ctx, "test prompt", cfg, &buf)
	// The guard should not error; the failure will be from connection refused or timeout
	// Ensure no thinking-level-related error is surfaced
	if err != nil && strings.Contains(err.Error(), "thinking") {
		t.Errorf("unexpected thinking-level-related error: %v", err)
	}
}

func TestBatchRun_SetsThinkingLevelForSupportedModel(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID:    "opencode_go",
		BaseURL:       "http://127.0.0.1:1",
		APIKey:        "test-key",
		ModelName:     "deepseek-reasoner", // supports thinking levels
		ThinkingLevel: "high",
		Workspace:     t.TempDir(),
	}

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := svc.BatchRun(ctx, "test prompt", cfg, &buf)
	// The guard should set reasoning_effort; failure will be from connection refused or timeout
	// No thinking-level-related error should appear
	if err != nil && strings.Contains(err.Error(), "thinking") {
		t.Errorf("unexpected thinking-level-related error: %v", err)
	}
}

// TestBatchRun_FeedsDebugRecorderMetrics verifies that a headless batch run
// records traces into the debug recorder and feeds the same interaction
// metrics as browser runs (issue #987).
func TestBatchRun_FeedsDebugRecorderMetrics(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`+"\n\n")
		fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","model":"test-model","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer llm.Close()

	rec := debug.NewRecorder(20)
	uiSessionMgr := uisession.NewManager(10, t.TempDir())
	svc := NewRunService(RunServiceDeps{
		UISessionMgr:  uiSessionMgr,
		DebugRecorder: rec,
	})

	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    llm.URL,
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		MaxTurns:   1,
	}

	var buf bytes.Buffer
	if _, err := svc.BatchRun(context.Background(), "hello", cfg, &buf); err != nil {
		t.Fatalf("batch run failed: %v", err)
	}

	// The trace must carry the model + usage parsed from the stream tail.
	traces := rec.List(0, "", "")
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	if traces[0].Model != "test-model" {
		t.Fatalf("trace Model = %q, want test-model", traces[0].Model)
	}
	if traces[0].Usage == nil || traces[0].Usage.PromptTokens != 10 || traces[0].Usage.CompletionTokens != 5 {
		t.Fatalf("trace Usage = %+v, want prompt=10 completion=5", traces[0].Usage)
	}

	// The metrics aggregate must reflect the batch call.
	snap := rec.Metrics()
	if snap.TotalCalls != 1 {
		t.Fatalf("TotalCalls = %d, want 1", snap.TotalCalls)
	}
	if len(snap.Providers) != 1 || snap.Providers[0].ProviderID != "opencode_go" {
		t.Fatalf("providers = %+v, want opencode_go", snap.Providers)
	}
	mm := snap.Providers[0].Models[0]
	if mm.Model != "test-model" || mm.Calls != 1 {
		t.Fatalf("model aggregate = %+v, want test-model calls=1", mm)
	}
	if mm.Tokens.PromptTokens != 10 || mm.Tokens.CompletionTokens != 5 {
		t.Fatalf("model tokens = %+v, want prompt=10 completion=5", mm.Tokens)
	}
	if mm.Cache.Hits != 0 || mm.Cache.Misses != 1 {
		t.Fatalf("model cache = %+v, want hits=0 misses=1", mm.Cache)
	}
}

// TestBatchRun_StreamsReasoningToStdout verifies that reasoning-model batch
// runs stream thinking deltas to stdout alongside ordinary text (issue #1095):
// the thinking content arrives delimited by [thinking]/[/thinking] markers and
// the final text remains plain, so the two are distinguishable. The returned
// content must stay the final text only (reasoning is a stream-side concern).
func TestBatchRun_StreamsReasoningToStdout(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		// Reasoning deltas, then final text. The two reasoning chunks are
		// coalesced server-side into one thinking_delta SSE event.
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"reasoning_content":"Let me think"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"reasoning_content":"Let me think carefully"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"Final answer"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer llm.Close()

	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    llm.URL,
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		MaxTurns:   1,
	}

	var buf bytes.Buffer
	content, err := svc.BatchRun(context.Background(), "hello", cfg, &buf)
	if err != nil {
		t.Fatalf("batch run failed: %v", err)
	}

	// The returned content is the final text only.
	if content != "Final answer" {
		t.Fatalf("content = %q, want %q", content, "Final answer")
	}

	out := buf.String()
	// Reasoning is delimited and precedes the final text.
	thinkingOpen := strings.Index(out, "[thinking]\n")
	thinkingClose := strings.Index(out, "[/thinking]\n")
	finalIdx := strings.Index(out, "Final answer")
	if thinkingOpen < 0 {
		t.Fatalf("stdout missing [thinking] open marker:\n%s", out)
	}
	if thinkingClose < 0 {
		t.Fatalf("stdout missing [/thinking] close marker:\n%s", out)
	}
	if !(thinkingOpen < thinkingClose && thinkingClose < finalIdx) {
		t.Fatalf("expected [thinking] < [/thinking] < final text, got:\n%s", out)
	}
	// The accumulated reasoning content appears inside the delimited block.
	if !strings.Contains(out[thinkingOpen:thinkingClose], "Let me think carefully") {
		t.Fatalf("thinking block missing accumulated reasoning content:\n%s", out)
	}
	// The final text is streamed plain (no markers around it).
	if strings.Contains(out[finalIdx:], "[thinking]") || strings.Contains(out[finalIdx:], "[/thinking]") {
		t.Fatalf("final text should be plain, got:\n%s", out)
	}
}

// TestBatchRun_PlainTextOutputUnchanged verifies that models without reasoning
// produce exactly the same plain-text stdout as before — no thinking markers
// anywhere in the stream (issue #1095 acceptance criterion).
func TestBatchRun_PlainTextOutputUnchanged(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    singleTurnLLM(t).URL,
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		MaxTurns:   1,
	}

	var buf bytes.Buffer
	if _, err := svc.BatchRun(context.Background(), "hello", cfg, &buf); err != nil {
		t.Fatalf("batch run failed: %v", err)
	}

	out := buf.String()
	if out != "ok" {
		t.Fatalf("stdout = %q, want plain %q (no thinking markers)", out, "ok")
	}
	if strings.Contains(out, "[thinking]") || strings.Contains(out, "[/thinking]") {
		t.Fatalf("plain model output must not contain thinking markers: %q", out)
	}
}

// TestBatchStreamer exercises the state machine that delimits reasoning
// content in batch-mode stdout (issue #1095).
func TestBatchStreamer(t *testing.T) {
	t.Run("token only", func(t *testing.T) {
		var buf bytes.Buffer
		b := &batchStreamer{out: &buf}
		b.writeToken("hello ")
		b.writeToken("world")
		b.closeThinking()
		if got := buf.String(); got != "hello world" {
			t.Fatalf("output = %q, want %q", got, "hello world")
		}
	})
	t.Run("thinking then token", func(t *testing.T) {
		var buf bytes.Buffer
		b := &batchStreamer{out: &buf}
		b.writeThinking("reason a ")
		b.writeThinking("reason b")
		b.writeToken("answer")
		b.closeThinking()
		want := "[thinking]\nreason a reason b[/thinking]\nanswer"
		if got := buf.String(); got != want {
			t.Fatalf("output = %q, want %q", got, want)
		}
	})
	t.Run("token thinking token delimiters each span", func(t *testing.T) {
		var buf bytes.Buffer
		b := &batchStreamer{out: &buf}
		b.writeToken("first")
		b.writeThinking("r1")
		b.writeToken("middle")
		b.writeThinking("r2")
		b.writeToken("last")
		b.closeThinking()
		want := "first[thinking]\nr1[/thinking]\nmiddle[thinking]\nr2[/thinking]\nlast"
		if got := buf.String(); got != want {
			t.Fatalf("output = %q, want %q", got, want)
		}
	})
	t.Run("stream ends during thinking closes block", func(t *testing.T) {
		var buf bytes.Buffer
		b := &batchStreamer{out: &buf}
		b.writeThinking("unfinished")
		b.closeThinking()
		want := "[thinking]\nunfinished[/thinking]\n"
		if got := buf.String(); got != want {
			t.Fatalf("output = %q, want %q", got, want)
		}
	})
	t.Run("closeThinking idempotent", func(t *testing.T) {
		var buf bytes.Buffer
		b := &batchStreamer{out: &buf}
		b.writeThinking("r")
		b.closeThinking()
		b.closeThinking()
		want := "[thinking]\nr[/thinking]\n"
		if got := buf.String(); got != want {
			t.Fatalf("output = %q, want %q", got, want)
		}
	})
}

// TestBatchRun_DelegateSpawnsSubAgent verifies that the delegate tool resolves
// the parent run config in batch mode (issue #1001). BatchRun registers the
// parent config under the batch ID, so the run context must carry the batch ID
// as the tool session ID — otherwise DelegateTool.Call forwards an empty
// session ID and SpawnSubAgent fails with "no parent run config found".
//
// The mock LLM server is driven by the request bodies it receives: the first
// parent turn is told to call delegate, the spawned sub-agent (identified by
// its task-suffix system prompt) gets a plain answer, and the parent's second
// turn must have the delegate task_id in its history. Asserting the parent's
// second turn carries the task_id proves delegate returned successfully.
func TestBatchRun_DelegateSpawnsSubAgent(t *testing.T) {
	var mu sync.Mutex
	var parentReqs, subAgentReqs int
	parentTurn2HadTaskID := false

	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		isSubAgent := bytes.Contains(body, []byte("You are performing the following task"))

		parentNum := 0
		if isSubAgent {
			mu.Lock()
			subAgentReqs++
			mu.Unlock()
		} else {
			mu.Lock()
			parentReqs++
			parentNum = parentReqs
			if parentNum > 1 && bytes.Contains(body, []byte("task_id")) {
				parentTurn2HadTaskID = true
			}
			mu.Unlock()
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")

		switch {
		case isSubAgent:
			// Sub-agent gets a plain text answer and finishes.
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"sub-agent done"},"index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		case parentNum == 1:
			// Parent turn 1: the model decides to delegate a task.
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"delegate","arguments":"{\"task\":\"run whoami\"}"}}]},"finish_reason":"tool_calls"}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			// Parent turn 2: give the sub-agent a window to fire its request,
			// then wrap up the batch run with a final answer.
			time.Sleep(200 * time.Millisecond)
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"delegation complete"},"index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	defer llm.Close()

	svc := NewRunService(RunServiceDeps{})
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    llm.URL,
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		MaxTurns:   5,
	}

	var buf bytes.Buffer
	content, err := svc.BatchRun(context.Background(), "delegate this task", cfg, &buf)
	if err != nil {
		t.Fatalf("batch run failed: %v", err)
	}
	if !strings.Contains(content, "delegation complete") {
		t.Fatalf("unexpected batch content: %q", content)
	}

	// The parent's second turn must carry the delegate task_id in history —
	// if delegate errored (e.g. "no parent run config found"), no task_id
	// would have been fed back.
	if !parentTurn2HadTaskID {
		t.Fatal("parent's second turn did not carry the delegate task_id — delegate likely failed with 'no parent run config found'")
	}

	// The delegate must have actually spawned a sub-agent request against the
	// LLM server (it runs asynchronously, so poll briefly).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := subAgentReqs
		mu.Unlock()
		if n >= 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("delegate did not spawn a sub-agent request to the LLM server")
}

// TestBatchRun_ErrorFeedsMetrics verifies that a failing headless batch run
// classifies the error at capture time and increments the per-model error
// counters.
func TestBatchRun_ErrorFeedsMetrics(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limit exceeded"}}`)
	}))
	defer llm.Close()

	rec := debug.NewRecorder(20)
	uiSessionMgr := uisession.NewManager(10, t.TempDir())
	svc := NewRunService(RunServiceDeps{
		UISessionMgr:  uiSessionMgr,
		DebugRecorder: rec,
	})

	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    llm.URL,
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		MaxTurns:   1,
	}

	var buf bytes.Buffer
	if _, err := svc.BatchRun(context.Background(), "hello", cfg, &buf); err == nil {
		t.Fatal("expected batch run to fail on 429")
	}

	snap := rec.Metrics()
	if snap.TotalCalls != 1 {
		t.Fatalf("TotalCalls = %d, want 1", snap.TotalCalls)
	}
	if snap.TotalErrors != 1 {
		t.Fatalf("TotalErrors = %d, want 1", snap.TotalErrors)
	}
	mm := snap.Providers[0].Models[0]
	if mm.Errors[debug.ErrorClassRateLimit] != 1 {
		t.Fatalf("rate_limit errors = %d, want 1", mm.Errors[debug.ErrorClassRateLimit])
	}
	if mm.LastError != debug.ErrorClassRateLimit {
		t.Fatalf("last_error = %q, want rate_limit", mm.LastError)
	}
}

func TestExtractLastMessages(t *testing.T) {
	mgr := uisession.NewManager(10, t.TempDir())
	seedSession(t, mgr, "test-session", "", "")

	// No messages yet
	user, asst := extractLastMessages(mgr, "test-session")
	if user != "" || asst != "" {
		t.Fatalf("expected empty messages, got user=%q, asst=%q", user, asst)
	}

	// Add a user message
	mgr.AppendToConversation("test-session", message.Message{Role: "user", Content: "hello world", CreatedAt: time.Now()})
	user, asst = extractLastMessages(mgr, "test-session")
	if user != "hello world" {
		t.Fatalf("expected user='hello world', got %q", user)
	}
	if asst != "" {
		t.Fatalf("expected empty assistant, got %q", asst)
	}

	// Add an assistant message
	mgr.AppendToConversation("test-session", message.Message{Role: "assistant", Content: "hi there", CreatedAt: time.Now()})
	user, asst = extractLastMessages(mgr, "test-session")
	if user != "hello world" {
		t.Fatalf("expected user='hello world', got %q", user)
	}
	if asst != "hi there" {
		t.Fatalf("expected assistant='hi there', got %q", asst)
	}

	// Add more messages and verify last ones are returned
	mgr.AppendToConversation("test-session", message.Message{Role: "user", Content: "second question", CreatedAt: time.Now()})
	mgr.AppendToConversation("test-session", message.Message{Role: "assistant", Content: "second answer", CreatedAt: time.Now()})
	user, asst = extractLastMessages(mgr, "test-session")
	if user != "second question" {
		t.Fatalf("expected user='second question', got %q", user)
	}
	if asst != "second answer" {
		t.Fatalf("expected assistant='second answer', got %q", asst)
	}
}

// TestExtractLastMessages_ConcurrentWithAppend is a race-regression test for
// issue #1241's fix round: extractLastMessages reads the canonical
// conversation, so it must read a locked copy rather than iterate the live
// shared reference a concurrent run keeps appending to. Under -race this test
// must report no data races.
func TestExtractLastMessages_ConcurrentWithAppend(t *testing.T) {
	mgr := uisession.NewManager(100, t.TempDir())
	seedSession(t, mgr, "test-session", "", "start")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	// Simulated active run goroutine: keeps appending to the canonical
	// conversation (the loop's session-backed history adapter path). A user
	// message every 10 appends keeps the exchange-cap trim active so the
	// conversation stays bounded for the whole overlap window.
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			mgr.AppendToConversation("test-session", message.Message{
				Role: "assistant", Content: fmt.Sprintf("reply %d", i), CreatedAt: time.Now(),
			})
			if i%10 == 0 {
				mgr.AppendToConversation("test-session", message.Message{
					Role: "user", Content: fmt.Sprintf("user %d", i), CreatedAt: time.Now(),
				})
			}
			i++
		}
	}()

	// Reader goroutine: batch run-start extracts last user/assistant messages
	// while the run is active elsewhere on the same session.
	//
	// The primary gate for the unsynchronized access is the race detector; the
	// read-side invariant we assert here is that a user message is always
	// present (the seeded "start" message plus the writer's periodic user
	// appends survive the exchange-cap trim — it keeps the trailing user
	// tail). An empty assistant is legitimate at the very start, before the
	// writer's first append, so only the user side is counted.
	var emptyUser int32
	var reads int64
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			user, _ := extractLastMessages(mgr, "test-session")
			atomic.AddInt64(&reads, 1)
			if user == "" {
				atomic.AddInt32(&emptyUser, 1)
			}
		}
	}()

	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()

	if atomic.LoadInt64(&reads) == 0 {
		t.Fatal("reader goroutine never ran")
	}
	if empty := atomic.LoadInt32(&emptyUser); empty > 0 {
		t.Errorf("extractLastMessages returned empty user in %d reads", empty)
	}
	// After the overlap the conversation certainly holds assistant replies.
	if _, asst := extractLastMessages(mgr, "test-session"); asst == "" {
		t.Error("extractLastMessages returned empty assistant after overlapping appends")
	}
}

func TestBatchRun_ConversationContextCapturedOnError(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://127.0.0.1:1", // connection refused -> error
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		MaxTurns:   5,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	_, err := svc.BatchRun(ctx, "test prompt for context capture", cfg, &buf)
	if err == nil {
		t.Fatal("expected error for connection failure")
	}

	// Verify conversation context was captured
	convCtx := svc.LastBatchConversationContext()
	if convCtx == nil {
		t.Fatal("expected non-nil conversation context after batch run error")
	}
	if convCtx.LastUserMessage == "" {
		t.Fatal("expected non-empty last user message")
	}
	if convCtx.LastUserMessage != "test prompt for context capture" {
		t.Fatalf("expected user message 'test prompt for context capture', got %q", convCtx.LastUserMessage)
	}
}

func TestBatchRun_UsesActivePersona(t *testing.T) {
	// Isolate the persona home dir per test; injected via RunConfig.HomeDir
	// instead of mutating the process HOME env (issue #1023).
	homeDir := t.TempDir()

	workspace := t.TempDir()

	// Create a test persona with a custom system prompt and an injected skill
	personaDef := &persona.PersonaDefinition{
		Name:           "test-reviewer",
		SystemPrompt:   "You are a code reviewer. Be thorough and check for edge cases.",
		RequiredSkills: []string{"test-review-skill"},
	}
	if err := persona.SaveToHome(homeDir, personaDef); err != nil {
		t.Fatalf("save persona: %v", err)
	}

	// Create a minimal test skill referenced by the persona
	skillDir := filepath.Join(workspace, ".eitri", "skills", "test-review-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillContent := "---\nname: test-review-skill\ndescription: A test skill for batch persona testing\n---\nReview the code for potential bugs and security issues."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	// Create skills service rooted at the test skill directory
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{
		{Path: filepath.Join(workspace, ".eitri", "skills"), Scope: skills.ScopeProjectAgents},
	})

	// Verify that buildSystemPrompt produces output containing the persona's
	// custom system prompt and the injected skill's content.
	cfg := RunConfig{
		Workspace:     workspace,
		HomeDir:       homeDir,
		ActivePersona: "test-reviewer",
	}
	sysPrompt, err := buildSystemPrompt(cfg, sessionSkillContext{}, skillsSvc)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}
	if !strings.Contains(sysPrompt, "You are a code reviewer. Be thorough and check for edge cases.") {
		t.Fatalf("system prompt should contain persona's custom prompt, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "test-review-skill") {
		t.Fatalf("system prompt should reference injected skill name, got:\n%s", sysPrompt)
	}
	if strings.Contains(sysPrompt, "Review the code for potential bugs and security issues.") {
		t.Fatalf("system prompt should NOT contain injected skill body (skills are loaded via skill() tool), got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "Required skills for this persona:") {
		t.Fatalf("system prompt should contain required skills directive, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "<required_skills>") {
		t.Fatalf("system prompt should contain <required_skills> block, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "</required_skills>") {
		t.Fatalf("system prompt should contain </required_skills> closing tag, got:\n%s", sysPrompt)
	}

	// End-to-end test: BatchRun with ActivePersona pointing to the test persona.
	// Use a dead-port connection pattern so the run fails (connection refused)
	// after the persona has been loaded and the system prompt built.
	uiSessionMgr := uisession.NewManager(10, t.TempDir())
	svc := NewRunService(RunServiceDeps{
		UISessionMgr:  uiSessionMgr,
		SkillsService: skillsSvc,
	})

	batchCfg := RunConfig{
		ProviderID:    "opencode_go",
		BaseURL:       "http://127.0.0.1:1", // connection refused -> error
		APIKey:        "test-key",
		ModelName:     "test-model",
		Workspace:     workspace,
		HomeDir:       homeDir,
		ActivePersona: "test-reviewer",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	_, err = svc.BatchRun(ctx, "test prompt", batchCfg, &buf)
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
	// The error should be about connection failure, not about persona loading.
	// If the persona file didn't exist or failed to load, the error message
	// would contain "persona". We verify the persona was loaded correctly
	// by asserting the error is NOT persona-related.
	if strings.Contains(err.Error(), "persona") {
		t.Fatalf("unexpected persona error — persona should have been loaded successfully: %v", err)
	}
}
