// Package agent implements the ADK agent and built-in tools.
package agent

import (
	"context"
	"fmt"
	"log"
	"os"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/glemsom/eitri/internal/executor"
)

// NewAgent creates an ADK LLMAgent with the given model and tools.
func NewAgent(llm model.LLM, sessionMgr *executor.SessionManager) (agent.Agent, error) {
	// terminal_execute args
	type termArgs struct {
		// jsonschema tag value is the field description; required is inferred from json tag (no omitempty).
		Command string `json:"command" jsonschema:"Shell command to run"`
	}
	// terminal_execute result
	type termResult struct {
		Stdout    string `json:"stdout"`
		ExitCode  int    `json:"exit_code"`
		TimedOut  bool   `json:"timed_out"`
		Truncated bool   `json:"truncated"`
	}

	termTool, err := functiontool.New[termArgs, termResult](
		functiontool.Config{
			Name:        "terminal_execute",
			Description: "Execute a shell command in the session's tmux shell and return the output. Use for running commands, tests, builds, or any shell operations.",
		},
		func(ctx agent.Context, args termArgs) (termResult, error) {
			sessionID := ctx.SessionID()
			exe, err := sessionMgr.GetOrCreate(sessionID)
			if err != nil {
				return termResult{}, fmt.Errorf("failed to get session executor: %w", err)
			}
			result, err := exe.ExecuteCommand(context.Background(), args.Command)
			if err != nil {
				return termResult{}, fmt.Errorf("command execution failed: %w", err)
			}
			return termResult{
				Stdout:    result.Stdout,
				ExitCode:  result.ExitCode,
				TimedOut:  result.TimedOut,
				Truncated: result.Truncated,
			}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create terminal_execute tool: %w", err)
	}

	// Read system prompt
	systemPrompt := os.Getenv("EITRI_DEFAULT_SYSTEM_PROMPT")
	if systemPrompt == "" {
		systemPrompt = `You are Eitri, a helpful AI coding assistant named after the Norse blacksmith who forged Mjölnir. You operate in a workspace on a Linux machine.

Guidelines:
- Use Markdown for all responses (headings, lists, tables, links).
- Use fenced code blocks with language tags (e.g. ` + "```go" + `) for all code.
- Use ` + "```mermaid" + ` fenced blocks for diagrams (architecture, sequence, flow, ER, class).
- Wrap reasoning/thinking steps in <think>...</think> tags.
- When you need to run a shell command, use the terminal_execute tool.
- Prefer showing command output and explaining results.`
	}

	a, err := llmagent.New(llmagent.Config{
		Name:        "eitri",
		Description: "Eitri AI coding assistant",
		Model:       llm,
		Instruction: systemPrompt,
		Tools:       []tool.Tool{termTool},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	return a, nil
}

// LogAgentEvents logs ADK session events for debugging.
func LogAgentEvents(ctx context.Context, ag agent.Agent, sessionID string) {
	log.Printf("Agent %q created for session %s", "eitri", sessionID)
}
