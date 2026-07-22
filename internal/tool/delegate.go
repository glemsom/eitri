package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/litellm"
)

type delegateArgs struct {
	Task     string `json:"task" jsonschema:"The task to delegate to a sub-agent (required)"`
	MaxTurns int    `json:"max_turns" jsonschema:"Maximum number of turns for the sub-agent (default: 50)"`
}

// DelegateTool implements ToolHandler for the delegate tool.
// It spawns a sub-agent to complete a task and returns a task ID immediately.
type DelegateTool struct {
	subMgr SubAgentManager
	schema litellm.Schema
}

// NewDelegate creates a DelegateTool that uses the given SubAgentManager.
func NewDelegate(subMgr SubAgentManager) *DelegateTool {
	return &DelegateTool{
		subMgr: subMgr,
		schema: SchemaOf[delegateArgs](),
	}
}

func (t *DelegateTool) Name() string { return "delegate" }

func (t *DelegateTool) Description() string {
	return "Delegate a task to a sub-agent. Returns a task_id immediately as JSON {\"task_id\": \"...\"}. " +
		"The sub-agent runs independently with its own tool access and its own context window. " +
		"Use delegate for data-intensive work (fetching multiple URLs, reading large files, " +
		"wide searches) to avoid filling the parent\u2019s context window with raw data. " +
		"Multiple delegates can be fired in one turn and collected together. " +
		"Call collect() with the task_id to retrieve results."
}

func (t *DelegateTool) JSONSchema() litellm.Schema { return t.schema }

func (t *DelegateTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed delegateArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("delegate: invalid args: %w", err), false
	}
	if parsed.Task == "" {
		return textBlocks("Error: 'task' parameter is required"), nil, true
	}
	if parsed.MaxTurns <= 0 {
		parsed.MaxTurns = 50
	}

	sessionID, _ := ctx.Value(SessionIDKey).(string)

	taskID, err := t.subMgr.SpawnSubAgent(ctx, sessionID, parsed.Task, parsed.MaxTurns)
	if err != nil {
		return nil, fmt.Errorf("delegate: %w", err), false
	}

	result := fmt.Sprintf(`{"task_id": %q}`, taskID)
	return textBlocks(result), nil, false
}
