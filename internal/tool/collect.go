package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/litellm"
)

type collectArgs struct {
	TaskIDs []string `json:"task_ids" jsonschema:"List of task IDs to collect results from (required)"`
}

// CollectTool implements ToolHandler for the collect tool.
// It blocks until all specified sub-agent tasks complete and returns structured results.
type CollectTool struct {
	subMgr SubAgentManager
	schema litellm.Schema
}

// NewCollect creates a CollectTool that uses the given SubAgentManager.
func NewCollect(subMgr SubAgentManager) *CollectTool {
	return &CollectTool{
		subMgr: subMgr,
		schema: SchemaOf[collectArgs](),
	}
}

func (t *CollectTool) Name() string { return "collect" }

func (t *CollectTool) Description() string {
	return "Collect results from previously delegated sub-agent tasks. " +
		"Provide the task_ids returned by delegate(). " +
		"This tool blocks until all tasks complete or the parent run is cancelled. " +
		"Returns a JSON object keyed by task ID, each with status (completed|error|cancelled), result (string), and turn_count (int)."
}

func (t *CollectTool) JSONSchema() litellm.Schema { return t.schema }

func (t *CollectTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed collectArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("collect: invalid args: %w", err), false
	}
	if len(parsed.TaskIDs) == 0 {
		return textBlocks("Error: 'task_ids' must be a non-empty array of task ID strings"), nil, true
	}

	results, err := t.subMgr.CollectSubAgents(ctx, parsed.TaskIDs)
	if err != nil {
		return nil, fmt.Errorf("collect: %w", err), false
	}

	resultJSON, err := json.Marshal(results)
	if err != nil {
		return nil, fmt.Errorf("collect: marshal results: %w", err), false
	}
	return textBlocks(string(resultJSON)), nil, false
}
