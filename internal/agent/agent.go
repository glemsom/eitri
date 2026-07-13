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
	"github.com/glemsom/eitri/internal/skills"
)

// NewAgent creates an ADK LLMAgent with the given model and tools.
func NewAgent(llm model.LLM, sessionMgr *executor.SessionManager) (agent.Agent, error) {
	workspace := sessionMgr.Workspace()
	return newAgentWithSkills(llm, sessionMgr, workspace, skills.NewService())
}

// NewAgentWithSkills creates an ADK LLMAgent with skills support.
func NewAgentWithSkills(llm model.LLM, sessionMgr *executor.SessionManager, skillsSvc *skills.Service) (agent.Agent, error) {
	workspace := sessionMgr.Workspace()
	return newAgentWithSkills(llm, sessionMgr, workspace, skillsSvc)
}

func newAgentWithSkills(llm model.LLM, sessionMgr *executor.SessionManager, workspace string, skillsSvc *skills.Service) (agent.Agent, error) {
	tools := make([]tool.Tool, 0)

	// terminal_execute
	type termArgs struct {
		Command string `json:"command" jsonschema:"Shell command to run"`
	}
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
	tools = append(tools, termTool)

	// file_viewer
	type fileViewerArgs struct {
		Path   string `json:"path" jsonschema:"File path relative to workspace root or an absolute path within the workspace"`
		Offset int    `json:"offset,omitempty" jsonschema:"1-indexed line offset to start reading from (default: 1)"`
		Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of lines to return (default: no limit)"`
	}
	type fileViewerResult struct {
		Path      string `json:"path"`
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
	}

	// Get skill directories for file_viewer access
	skillDirs := skillsSvc.SkillDirectories()
	fileViewerTool, err := functiontool.New[fileViewerArgs, fileViewerResult](
		functiontool.Config{
			Name:        "file_viewer",
			Description: "Read file contents from workspace or active skill directories. Supports line offset and limit. Only UTF-8 text files. Rejects binary files and directories.",
		},
		func(ctx agent.Context, args fileViewerArgs) (fileViewerResult, error) {
			absPath, err := validatePathWithAllowed(args.Path, workspace, skillDirs)
			if err != nil {
				return fileViewerResult{}, fmt.Errorf("path validation failed: %w", err)
			}

			vr, err := ReadFile(absPath, args.Offset, args.Limit)
			if err != nil {
				return fileViewerResult{}, err
			}
			return fileViewerResult{
				Path:      args.Path,
				Content:   vr.Content,
				Truncated: vr.Truncated,
			}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create file_viewer tool: %w", err)
	}
	tools = append(tools, fileViewerTool)

	// file_editor
	type fileEditorArgs struct {
		Path    string `json:"path" jsonschema:"File path relative to workspace root"`
		Content string `json:"content" jsonschema:"New file content (UTF-8 text)"`
		Mode    string `json:"mode" jsonschema:"'create' for new files, 'overwrite' for existing files"`
	}
	type fileEditorResult struct {
		Path         string   `json:"path"`
		Mode         string   `json:"mode"`
		BytesWritten int      `json:"bytes_written"`
		OldContent   string   `json:"old_content,omitempty"`
		NewContent   string   `json:"new_content,omitempty"`
		DirsCreated  []string `json:"dirs_created,omitempty"`
	}

	fileEditorTool, err := functiontool.New[fileEditorArgs, fileEditorResult](
		functiontool.Config{
			Name:        "file_editor",
			Description: "Create or overwrite files in workspace. Mode 'create' creates a new file (rejects if exists), 'overwrite' replaces existing file content. Captures old content for diff display.",
		},
		func(ctx agent.Context, args fileEditorArgs) (fileEditorResult, error) {
			absPath, err := validateWorkspacePath(args.Path, workspace)
			if err != nil {
				return fileEditorResult{}, fmt.Errorf("path validation failed: %w", err)
			}

			er, err := WriteFile(absPath, args.Content, args.Mode)
			if err != nil {
				return fileEditorResult{}, err
			}
			return fileEditorResult{
				Path:         args.Path,
				Mode:         er.Mode,
				BytesWritten: er.BytesWritten,
				OldContent:   er.OldContent,
				NewContent:   er.NewContent,
				DirsCreated:  er.DirsCreated,
			}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create file_editor tool: %w", err)
	}
	tools = append(tools, fileEditorTool)

	// activate_skill
	type activateSkillArgs struct {
		Name string `json:"name" jsonschema:"Name of the skill to activate"`
	}
	type activateSkillResult struct {
		Content string `json:"content"`
	}

	activateSkillTool, err := functiontool.New[activateSkillArgs, activateSkillResult](
		functiontool.Config{
			Name:        "activate_skill",
			Description: "Activate a skill by name. Skills provide reusable instructions, references, and scripts for specialized tasks. Call this when a task matches an available skill description. Returns structured skill content including instructions and resource manifest.",
		},
		func(ctx agent.Context, args activateSkillArgs) (activateSkillResult, error) {
			if skillsSvc == nil {
				return activateSkillResult{}, fmt.Errorf("skills service not available")
			}
			skill := skillsSvc.Lookup(args.Name)
			if skill == nil {
				return activateSkillResult{}, fmt.Errorf("skill %q not found in effective skills", args.Name)
			}

			resources := skills.ListResources(skill.Path)
			content := skills.SkillContent(skill.Body, resources, skill.Path)
			return activateSkillResult{Content: content}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create activate_skill tool: %w", err)
	}
	tools = append(tools, activateSkillTool)

	// Build system prompt with skills catalog
	systemPrompt := os.Getenv("EITRI_DEFAULT_SYSTEM_PROMPT")
	if systemPrompt == "" {
		systemPrompt = `You are Eitri, a helpful AI coding assistant named after the Norse blacksmith who forged Mjölnir. You operate in a workspace on a Linux machine.

Guidelines:
- Use Markdown for all responses (headings, lists, tables, links).
- Use fenced code blocks with language tags (e.g. ` + "```go" + `) for all code.
- Use ` + "```mermaid" + ` fenced blocks for diagrams (architecture, sequence, flow, ER, class).
- Wrap reasoning/thinking steps in <think>...</think> tags.
- When you need to run a shell command, use the terminal_execute tool.
- To read files, use the file_viewer tool.
- To create or edit files, use the file_editor tool.
- When a task matches an available skill description, call activate_skill with that skill name before proceeding.
- Prefer showing command output and explaining results.`
	}

	// Append skills catalog to system prompt if skills are available
	catalog := skillsSvc.SkillsCatalogXML()
	if catalog != "" {
		systemPrompt += "\n\nAvailable skills:\n" + catalog + "\n\nWhen a task matches a skill description, call activate_skill with the skill name before proceeding. This loads the skill's instructions, references, and scripts into context."
	}

	a, err := llmagent.New(llmagent.Config{
		Name:        "eitri",
		Description: "Eitri AI coding assistant",
		Model:       llm,
		Instruction: systemPrompt,
		Tools:       tools,
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
