package runner

import (
	"context"
	"strconv"
	"testing"

	"github.com/glemsom/eitri/internal/testutil"
)

// TestNoGoroutineLeak_CompactSession exercises the compact path that spins up a
// throwaway LLM client (and its HTTP client goroutines) against a reachable
// fake server, and asserts the shutdown audit holds: no service-owned goroutine
// may remain running after teardown (issue #1127). The guard registers first so
// it runs last among cleanups, after the fake server has closed.
func TestNoGoroutineLeak_CompactSession(t *testing.T) {
	testutil.NewGoroutineLeakGuard(t)
	fakeLLM := fakeCompactLLMServer(t, "summarised output")
	svc, uiMgr := newRunServiceForTest(t)

	sess, err := uiMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	svc.historySessionMgr.Create(sess.ID)
	svc.historySessionMgr.AppendUser(sess.ID, "Tell me a summary of context")
	for i := 0; i < 6; i++ {
		svc.historySessionMgr.AppendAssistant(sess.ID, "Some long diagnostic output line number "+strconv.Itoa(i), nil)
	}

	for i := 0; i < 3; i++ {
		count, freed, pruned, err := svc.CompactSession(context.Background(), sess.ID, compactRunConfig(fakeLLM.URL))
		if err != nil {
			t.Fatalf("CompactSession iter %d: %v", i, err)
		}
		t.Logf("compact iter %d: count=%d freed=%d pruned=%d", i, count, freed, pruned)
	}
}

// TestNoGoroutineLeak_RunStartCancel verifies a run started and cancelled
// against an unreachable URL leaves no service-owned goroutine running.
func TestNoGoroutineLeak_RunStartCancel(t *testing.T) {
	testutil.NewGoroutineLeakGuard(t)
	svc, _ := newRunServiceForTest(t)

	for i := 0; i < 5; i++ {
		if _, err := svc.StartRun(context.Background(), sessionIDFor(i), "hello", runConfigFor(i)); err != nil {
			// StartRun may fail immediately against unreachable URL; that is fine.
			t.Logf("StartRun %d error (expected): %v", i, err)
		}
		svc.Cancel(sessionIDFor(i))
	}
	svc.CancelAll()
}

func sessionIDFor(i int) string {
	return "leak-session-" + strconv.Itoa(i)
}

func runConfigFor(i int) RunConfig {
	return RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://127.0.0.1:1",
		APIKey:     "test-key",
		ModelName:  "test-model",
	}
}
