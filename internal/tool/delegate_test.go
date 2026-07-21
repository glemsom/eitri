package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/voocel/litellm"
)

// fakeSubAgentManager implements SubAgentManager for testing.
type fakeSubAgentManager struct {
	tasks   map[string]SubAgentResult
	nextID  int
	spawnFn func(ctx context.Context, sessionID, task string, maxTurns int) (taskID string, err error)
}

func (f *fakeSubAgentManager) SpawnSubAgent(ctx context.Context, _ string, task string, maxTurns int) (string, error) {
	if f.spawnFn != nil {
		return f.spawnFn(ctx, "", task, maxTurns)
	}
	f.nextID++
	taskID := fmt.Sprintf("task_test_%d", f.nextID)
	if f.tasks == nil {
		f.tasks = make(map[string]SubAgentResult)
	}
	f.tasks[taskID] = SubAgentResult{Status: "completed", Result: "result_" + task, TurnCount: maxTurns}
	return taskID, nil
}

func (f *fakeSubAgentManager) CollectSubAgents(_ context.Context, taskIDs []string) (map[string]SubAgentResult, error) {
	results := make(map[string]SubAgentResult, len(taskIDs))
	for _, tid := range taskIDs {
		if r, ok := f.tasks[tid]; ok {
			results[tid] = r
		} else {
			results[tid] = SubAgentResult{Status: "cancelled"}
		}
	}
	return results, nil
}

func TestDelegateTool_Name(t *testing.T) {
	d := NewDelegate(&fakeSubAgentManager{})
	if got := d.Name(); got != "delegate" {
		t.Errorf("Name() = %q, want %q", got, "delegate")
	}
}

func TestDelegateTool_Call_EmptyTask(t *testing.T) {
	d := NewDelegate(&fakeSubAgentManager{})
	args := json.RawMessage(`{"task": ""}`)
	blocks, err, isError := d.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	if !isError {
		t.Fatal("expected isError=true for empty task")
	}
	if len(blocks) == 0 {
		t.Fatal("expected non-empty blocks")
	}
}

func TestDelegateTool_Call_ValidTask(t *testing.T) {
	mgr := &fakeSubAgentManager{}
	d := NewDelegate(mgr)
	args := json.RawMessage(`{"task": "research X", "max_turns": 10}`)
	blocks, err, isError := d.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	if isError {
		t.Fatal("isError should be false for valid task")
	}
	if len(blocks) == 0 {
		t.Fatal("expected non-empty blocks")
	}
	// Result should contain task_id
	result := blocksToTextForTest(blocks)
	if !json.Valid([]byte(result)) {
		t.Fatalf("result is not valid JSON: %q", result)
	}
	var res struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.TaskID == "" {
		t.Fatal("task_id should not be empty")
	}
}

func TestDelegateTool_Call_DefaultMaxTurns(t *testing.T) {
	mgr := &fakeSubAgentManager{}
	d := NewDelegate(mgr)
	args := json.RawMessage(`{"task": "do something"}`)
	blocks, err, isError := d.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	if isError {
		t.Fatal("isError should be false")
	}
	_ = blocks
}

func TestDelegateTool_Schema(t *testing.T) {
	d := NewDelegate(&fakeSubAgentManager{})
	schema := d.JSONSchema()
	if schema == nil {
		t.Fatal("JSONSchema() returned nil")
	}
}

func TestCollectTool_Name(t *testing.T) {
	c := NewCollect(&fakeSubAgentManager{})
	if got := c.Name(); got != "collect" {
		t.Errorf("Name() = %q, want %q", got, "collect")
	}
}

func TestCollectTool_Call_EmptyTaskIDs(t *testing.T) {
	c := NewCollect(&fakeSubAgentManager{})
	args := json.RawMessage(`{"task_ids": []}`)
	blocks, err, isError := c.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	if !isError {
		t.Fatal("expected isError=true for empty task_ids")
	}
	if len(blocks) == 0 {
		t.Fatal("expected non-empty blocks")
	}
}

func TestCollectTool_Call_Valid(t *testing.T) {
	mgr := &fakeSubAgentManager{}
	// Spawn a task first
	d := NewDelegate(mgr)
	dArgs := json.RawMessage(`{"task": "research X", "max_turns": 5}`)
	dBlocks, _, _ := d.Call(context.Background(), dArgs)
	dResult := blocksToTextForTest(dBlocks)
	var dRes struct {
		TaskID string `json:"task_id"`
	}
	json.Unmarshal([]byte(dResult), &dRes)

	// Collect it
	c := NewCollect(mgr)
	cArgs := json.RawMessage(`{"task_ids": ["` + dRes.TaskID + `"]}`)
	blocks, err, isError := c.Call(context.Background(), cArgs)
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	if isError {
		t.Fatal("isError should be false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected non-empty blocks")
	}
	result := blocksToTextForTest(blocks)
	var results map[string]SubAgentResult
	if err := json.Unmarshal([]byte(result), &results); err != nil {
		t.Fatalf("unmarshal result: %v, json: %q", err, result)
	}
	r, ok := results[dRes.TaskID]
	if !ok {
		t.Fatalf("task_id %s not in results: %v", dRes.TaskID, results)
	}
	if r.Status != "completed" {
		t.Errorf("status = %q, want %q", r.Status, "completed")
	}
}

func TestCollectTool_Call_MultipleTasks(t *testing.T) {
	mgr := &fakeSubAgentManager{}
	// Spawn two tasks
	d := NewDelegate(mgr)
	task1Args := json.RawMessage(`{"task": "task A", "max_turns": 3}`)
	task2Args := json.RawMessage(`{"task": "task B", "max_turns": 5}`)
	b1, _, _ := d.Call(context.Background(), task1Args)
	b2, _, _ := d.Call(context.Background(), task2Args)

	var r1, r2 struct {
		TaskID string `json:"task_id"`
	}
	json.Unmarshal([]byte(blocksToTextForTest(b1)), &r1)
	json.Unmarshal([]byte(blocksToTextForTest(b2)), &r2)

	c := NewCollect(mgr)
	cArgs := json.RawMessage(`{"task_ids": ["` + r1.TaskID + `", "` + r2.TaskID + `"]}`)
	blocks, err, isError := c.Call(context.Background(), cArgs)
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	if isError {
		t.Fatal("isError should be false")
	}
	result := blocksToTextForTest(blocks)
	var results map[string]SubAgentResult
	if err := json.Unmarshal([]byte(result), &results); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(results), results)
	}
}

func TestCollectTool_Schema(t *testing.T) {
	c := NewCollect(&fakeSubAgentManager{})
	schema := c.JSONSchema()
	if schema == nil {
		t.Fatal("JSONSchema() returned nil")
	}
}

// blocksToTextForTest extracts text from litellm blocks for test assertions.
func blocksToTextForTest(blocks []litellm.Block) string {
	var b strings.Builder
	for _, block := range blocks {
		switch v := block.(type) {
		case litellm.TextBlock:
			b.WriteString(v.Text)
		case litellm.ToolResultBlock:
			b.WriteString(blocksToTextForTest(v.Content))
		default:
			b.WriteString("unknown_block")
		}
	}
	return b.String()
}
