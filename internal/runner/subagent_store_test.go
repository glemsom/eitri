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

func TestSubagentStore_NextID(t *testing.T) {
	ss := newSubagentStore()

	id1 := ss.nextID()
	id2 := ss.nextID()
	id3 := ss.nextID()

	if id1 == id2 || id2 == id3 {
		t.Fatal("expected unique task IDs")
	}
	if id1 != "task_1" {
		t.Fatalf("first ID = %q, want %q", id1, "task_1")
	}
	if id2 != "task_2" {
		t.Fatalf("second ID = %q, want %q", id2, "task_2")
	}
	if id3 != "task_3" {
		t.Fatalf("third ID = %q, want %q", id3, "task_3")
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

func TestSubagentStore_GetRecords(t *testing.T) {
	ss := newSubagentStore()

	// getRecords returns error for unknown task ID
	_, err := ss.getRecords([]string{"unknown"})
	if err == nil {
		t.Fatal("expected error for unknown task ID")
	}

	// getRecords returns all known records
	rec1 := &subAgentRecord{TaskID: "task_1", SessionID: "sess-1", Done: make(chan struct{})}
	rec2 := &subAgentRecord{TaskID: "task_2", SessionID: "sess-1", Done: make(chan struct{})}
	ss.storeRecord("task_1", rec1)
	ss.storeRecord("task_2", rec2)

	records, err := ss.getRecords([]string{"task_1", "task_2"})
	if err != nil {
		t.Fatalf("getRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	// Error if one is unknown
	_, err = ss.getRecords([]string{"task_1", "unknown"})
	if err == nil {
		t.Fatal("expected error when one task ID is unknown")
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

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := ss.nextID()
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
		}()
	}
	wg.Wait()

	// Store and read a parent config concurrently
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			ss.StoreParentCfg("sess-concurrent", RunConfig{ModelName: "test"})
			ss.GetParentCfg("sess-concurrent")
			ss.DeleteParentCfg("sess-concurrent")
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			ss.StoreParentCfg("sess-concurrent", RunConfig{ModelName: "test2"})
			ss.GetParentCfg("sess-concurrent")
		}
	}()

	wg.Wait()
}
