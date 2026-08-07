package runner

import (
	"context"
	"sync"
	"testing"
)

func TestNewSubagentStore(t *testing.T) {
	ss := newSubagentStore()
	if ss == nil {
		t.Fatal("newSubagentStore() returned nil")
	}
}

func TestSubagentStore_StoreAndGetRecord(t *testing.T) {
	ss := newSubagentStore()

	// getRecord returns nil for unknown
	if ss.getRecord("unknown") != nil {
		t.Fatal("getRecord should return nil for unknown task ID")
	}

	rec := &subAgentRecord{
		TaskID:    "task_1",
		SessionID: "sess-1",
		Status:    subAgentRunning,
		Done:      make(chan struct{}),
	}
	ss.storeRecord("task_1", rec)

	got := ss.getRecord("task_1")
	if got == nil {
		t.Fatal("getRecord returned nil after store")
	}
	if got.TaskID != "task_1" {
		t.Fatalf("task ID = %q, want %q", got.TaskID, "task_1")
	}
	if got.SessionID != "sess-1" {
		t.Fatalf("session ID = %q, want %q", got.SessionID, "sess-1")
	}
	if got.Status != subAgentRunning {
		t.Fatalf("status = %q, want %q", got.Status, subAgentRunning)
	}
}

func TestSubagentStore_CompletedResult(t *testing.T) {
	ss := newSubagentStore()

	// getCompletedResult returns absent for unknown task
	if _, ok := ss.getCompletedResult("unknown"); ok {
		t.Fatal("getCompletedResult should return absent for unknown task")
	}

	res := SubAgentResult{Status: "completed", Result: "fact", TurnCount: 3}
	ss.storeCompletedResult("sess-1", "task_1", res)

	got, ok := ss.getCompletedResult("task_1")
	if !ok {
		t.Fatal("getCompletedResult should return present after store")
	}
	if got != res {
		t.Fatalf("completed result = %+v, want %+v", got, res)
	}
}

func TestSubagentStore_CompletedResult_SurvivesReap(t *testing.T) {
	ss := newSubagentStore()

	rec := &subAgentRecord{TaskID: "task_1", SessionID: "sess-1", Status: subAgentCompleted,
		Result: "done", TurnCount: 2, Done: make(chan struct{})}
	rec.finish()
	ss.storeRecord("task_1", rec)
	ss.storeCompletedResult("sess-1", "task_1", subAgentRecordToResult(rec))

	// Reap removes the live record but not the durable completed result.
	ss.reapAfterTTL("task_1")
	if ss.getRecord("task_1") != nil {
		t.Fatal("record should be removed after reap")
	}
	got, ok := ss.getCompletedResult("task_1")
	if !ok {
		t.Fatal("completed result should survive the reap")
	}
	if got.Status != "completed" || got.Result != "done" || got.TurnCount != 2 {
		t.Fatalf("completed result = %+v, want completed/done/2", got)
	}
}

func TestSubagentStore_DeleteParentCfgCleansCompleted(t *testing.T) {
	ss := newSubagentStore()

	ss.storeCompletedResult("sess-1", "task_1", SubAgentResult{Status: "completed"})
	ss.storeCompletedResult("sess-2", "task_2", SubAgentResult{Status: "completed"})

	ss.DeleteParentCfg("sess-1")
	if _, ok := ss.getCompletedResult("task_1"); ok {
		t.Fatal("completed result for sess-1 should be cleaned up")
	}
	if _, ok := ss.getCompletedResult("task_2"); !ok {
		t.Fatal("completed result for sess-2 should be retained")
	}
}

func TestSubagentStore_CancelForSession(t *testing.T) {
	ss := newSubagentStore()

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())

	rec1 := &subAgentRecord{
		TaskID:    "task_1",
		SessionID: "sess-1",
		Status:    subAgentRunning,
		Done:      make(chan struct{}),
		Cancel:    cancel1,
	}
	rec2 := &subAgentRecord{
		TaskID:    "task_2",
		SessionID: "sess-1",
		Status:    subAgentRunning,
		Done:      make(chan struct{}),
		Cancel:    cancel2,
	}
	// Different session
	ctx3, cancel3 := context.WithCancel(context.Background())
	rec3 := &subAgentRecord{
		TaskID:    "task_3",
		SessionID: "sess-2",
		Status:    subAgentRunning,
		Done:      make(chan struct{}),
		Cancel:    cancel3,
	}

	ss.storeRecord("task_1", rec1)
	ss.storeRecord("task_2", rec2)
	ss.storeRecord("task_3", rec3)

	// Cancel session-1's sub-agents
	ss.CancelForSession("sess-1")

	// rec1 and rec2 should be cancelled
	select {
	case <-ctx1.Done():
	default:
		t.Fatal("rec1 should be cancelled")
	}
	select {
	case <-ctx2.Done():
	default:
		t.Fatal("rec2 should be cancelled")
	}
	// rec3 should NOT be cancelled
	select {
	case <-ctx3.Done():
		t.Fatal("rec3 should NOT be cancelled")
	default:
	}
}

func TestSubagentStore_ReapAfterTTL(t *testing.T) {
	ss := newSubagentStore()

	rec := &subAgentRecord{TaskID: "task_1", SessionID: "sess-1", Done: make(chan struct{})}
	ss.storeRecord("task_1", rec)

	if ss.getRecord("task_1") == nil {
		t.Fatal("record should exist before reap")
	}

	ss.reapAfterTTL("task_1")

	if ss.getRecord("task_1") != nil {
		t.Fatal("record should be removed after reap")
	}
}

func TestSubagentStore_ParentCfg(t *testing.T) {
	ss := newSubagentStore()

	// GetParentCfg returns false for unknown session
	_, ok := ss.GetParentCfg("unknown")
	if ok {
		t.Fatal("GetParentCfg should return false for unknown session")
	}

	cfg := RunConfig{
		ProviderID: "test-provider",
		ModelName:  "test-model",
	}
	ss.StoreParentCfg("sess-1", cfg)

	got, ok := ss.GetParentCfg("sess-1")
	if !ok {
		t.Fatal("GetParentCfg should return true for stored config")
	}
	if got.ProviderID != "test-provider" {
		t.Errorf("ProviderID = %q, want %q", got.ProviderID, "test-provider")
	}
	if got.ModelName != "test-model" {
		t.Errorf("ModelName = %q, want %q", got.ModelName, "test-model")
	}

	// Delete removes it
	ss.DeleteParentCfg("sess-1")
	_, ok = ss.GetParentCfg("sess-1")
	if ok {
		t.Fatal("GetParentCfg should return false after delete")
	}

	// Delete unknown session should not panic
	ss.DeleteParentCfg("unknown")
}

func TestSubagentStore_CollectResult(t *testing.T) {
	rec := &subAgentRecord{
		TaskID:    "task_1",
		Status:    subAgentCompleted,
		Result:    "done",
		TurnCount: 5,
		Done:      make(chan struct{}),
	}
	result := CollectResult(rec)
	if result.Status != "completed" {
		t.Fatalf("status = %q, want %q", result.Status, "completed")
	}
	if result.Result != "done" {
		t.Fatalf("result = %q, want %q", result.Result, "done")
	}
	if result.TurnCount != 5 {
		t.Fatalf("TurnCount = %d, want 5", result.TurnCount)
	}
}

func TestSubagentStore_CollectResult_Cancelled(t *testing.T) {
	rec := &subAgentRecord{
		TaskID:    "task_1",
		Status:    subAgentRunning,
		Result:    "",
		TurnCount: 0,
		Done:      make(chan struct{}),
	}
	result := CollectResult(rec)
	if result.Status != "cancelled" {
		t.Fatalf("status for running record = %q, want %q", result.Status, "cancelled")
	}
}

func TestSubagentStore_ConcurrentAccess(t *testing.T) {
	ss := newSubagentStore()
	var wg sync.WaitGroup

	for range 10 {
		wg.Go(func() {
			id := runJobID(runJobRoleSubagent)
			rec := &subAgentRecord{
				TaskID:    id,
				SessionID: "sess-1",
				Status:    subAgentRunning,
				Done:      make(chan struct{}),
				Cancel:    func() {},
			}
			ss.storeRecord(id, rec)
			ss.getRecord(id)
			ss.CancelForSession("sess-1")
			ss.reapAfterTTL(id)
		})
	}
	wg.Wait()

	// Store and read a parent config concurrently
	wg.Go(func() {
		for range 10 {
			ss.StoreParentCfg("sess-concurrent", RunConfig{ModelName: "test"})
			ss.GetParentCfg("sess-concurrent")
			ss.DeleteParentCfg("sess-concurrent")
		}
	})

	wg.Go(func() {
		for range 10 {
			ss.StoreParentCfg("sess-concurrent", RunConfig{ModelName: "test2"})
			ss.GetParentCfg("sess-concurrent")
		}
	})

	wg.Wait()
}
