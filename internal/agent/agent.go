// Package agent implements the ADK agent and built-in tools.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/glemsom/eitri/internal/executor"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

// NewAgent creates an ADK LLMAgent with the given model and tools.
func NewAgent(llm model.LLM, sessionMgr *executor.SessionManager) (agent.Agent, error) {
	return NewAgentWithPrompt(llm, sessionMgr, "")
}

// NewAgentWithPrompt creates an ADK LLMAgent with an optional custom system prompt.
func NewAgentWithPrompt(llm model.LLM, sessionMgr *executor.SessionManager, customSystemPrompt string) (agent.Agent, error) {
	workspace := sessionMgr.Workspace()
	return newAgentWithSkills(llm, sessionMgr, workspace, customSystemPrompt, skills.NewService(), nil)
}

// NewAgentWithSkills creates an ADK LLMAgent with skills support.
func NewAgentWithSkills(llm model.LLM, sessionMgr *executor.SessionManager, skillsSvc *skills.Service, uiSessionMgr *session.Manager) (agent.Agent, error) {
	return NewAgentWithPromptAndSkills(llm, sessionMgr, "", skillsSvc, uiSessionMgr)
}

// NewAgentWithPromptAndSkills creates an ADK LLMAgent with skills support and an optional custom system prompt.
func NewAgentWithPromptAndSkills(llm model.LLM, sessionMgr *executor.SessionManager, customSystemPrompt string, skillsSvc *skills.Service, uiSessionMgr *session.Manager) (agent.Agent, error) {
	workspace := sessionMgr.Workspace()
	return newAgentWithSkills(llm, sessionMgr, workspace, customSystemPrompt, skillsSvc, uiSessionMgr)
}

func newAgentWithSkills(llm model.LLM, sessionMgr *executor.SessionManager, workspace string, customSystemPrompt string, skillsSvc *skills.Service, uiSessionMgr *session.Manager) (agent.Agent, error) {
	tools := make([]tool.Tool, 0)

	// terminal_execute
	type termArgs struct {
		Command string `json:"command" jsonschema:"Shell command to run"`
	}

	termTool, err := functiontool.New[termArgs, executor.CommandResult](
		functiontool.Config{
			Name:        "terminal_execute",
			Description: "Execute a shell command in the session's tmux shell and return the output. Use for running commands, tests, builds, or any shell operations.",
		},
		func(ctx agent.Context, args termArgs) (executor.CommandResult, error) {
			sessionID := ctx.SessionID()
			exe, err := sessionMgr.GetOrCreate(sessionID)
			if err != nil {
				return executor.CommandResult{}, fmt.Errorf("failed to get session executor: %w", err)
			}
			result, err := exe.ExecuteCommand(ctx, args.Command)
			if err != nil {
				return executor.CommandResult{}, fmt.Errorf("command execution failed: %w", err)
			}
			return result, nil
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

	// render_component
	type renderComponentArgs struct {
		Name string                 `json:"name" jsonschema:"Component name: MermaidDiagram, QuickReplies, or DiffCard"`
		Data map[string]interface{} `json:"data" jsonschema:"Component data"`
	}
	type renderComponentResult struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}

	renderComponentTool, err := functiontool.New[renderComponentArgs, renderComponentResult](
		functiontool.Config{
			Name:        "render_component",
			Description: "Render a rich UI component in the chat. Supported components: MermaidDiagram (diagrams), QuickReplies (suggestion chips), DiffCard (code diffs). Use this for visual output instead of plain text when possible.",
		},
		func(ctx agent.Context, args renderComponentArgs) (renderComponentResult, error) {
			switch args.Name {
			case "MermaidDiagram":
				if args.Data == nil {
					return renderComponentResult{}, fmt.Errorf("MermaidDiagram requires 'code' field in data")
				}
				if _, ok := args.Data["code"]; !ok {
					return renderComponentResult{}, fmt.Errorf("MermaidDiagram requires 'code' field in data")
				}
			case "QuickReplies":
				if args.Data == nil {
					return renderComponentResult{}, fmt.Errorf("QuickReplies requires 'options' field in data")
				}
				options, ok := args.Data["options"]
				if !ok {
					return renderComponentResult{}, fmt.Errorf("QuickReplies requires 'options' field in data")
				}
				optsArr, ok := options.([]interface{})
				if !ok || len(optsArr) == 0 {
					return renderComponentResult{}, fmt.Errorf("QuickReplies options must be a non-empty array of strings")
				}
			case "DiffCard":
				if args.Data == nil {
					return renderComponentResult{}, fmt.Errorf("DiffCard requires 'old' and 'new' fields in data")
				}
				if _, ok := args.Data["old"]; !ok {
					return renderComponentResult{}, fmt.Errorf("DiffCard requires 'old' field in data")
				}
				if _, ok := args.Data["new"]; !ok {
					return renderComponentResult{}, fmt.Errorf("DiffCard requires 'new' field in data")
				}
			default:
				return renderComponentResult{}, fmt.Errorf("unknown component %q; supported: MermaidDiagram, QuickReplies, DiffCard", args.Name)
			}
			return renderComponentResult{Name: args.Name, Status: "ok"}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create render_component tool: %w", err)
	}
	tools = append(tools, renderComponentTool)

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

			// Record activation in session for persistence across runs
			if uiSessionMgr != nil {
				uiSessionMgr.ActivateSkill(ctx.SessionID(), args.Name)
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
	systemPrompt := buildSystemPrompt(customSystemPrompt, skillsSvc)

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

func buildSystemPrompt(customSystemPrompt string, skillsSvc *skills.Service) string {
	systemPrompt := customSystemPrompt
	if systemPrompt == "" {
		systemPrompt = os.Getenv("EITRI_DEFAULT_SYSTEM_PROMPT")
	}
	if systemPrompt == "" {
		systemPrompt = `You are Eitri, a helpful AI coding assistant named after the Norse blacksmith who forged Mjölnir. You operate in a workspace on a Linux machine.

Guidelines:
- Use Markdown for all responses (headings, lists, tables, links).
- Use fenced code blocks with language tags (e.g. ` + "```go" + `) for all code.
- Use ` + "```mermaid" + ` fenced blocks for diagrams (architecture, sequence, flow, ER, class).
- Use render_component tool for rich visual output: MermaidDiagram (diagrams), QuickReplies (suggestion chips), and DiffCard (code diffs).
- Wrap reasoning/thinking steps in <think>...</think> tags.
- When you need to run a shell command, use the terminal_execute tool.
- To read files, use the file_viewer tool.
- To create or edit files, use the file_editor tool.
- When a task matches an available skill description, call activate_skill with that skill name before proceeding.
- Prefer showing command output and explaining results.`
	}

	if skillsSvc == nil {
		return systemPrompt
	}

	catalog := skillsSvc.SkillsCatalogXML()
	if catalog != "" {
		systemPrompt += "\n\nAvailable skills:\n" + catalog + "\n\nWhen a task matches a skill description, call activate_skill with the skill name before proceeding. This loads the skill's instructions, references, and scripts into context."
	}
	return systemPrompt
}

// LogAgentEvents logs ADK session events for debugging.
func LogAgentEvents(ctx context.Context, ag agent.Agent, sessionID string) {
	slog.Info("agent created", slog.String("agent", "eitri"), slog.String("session_id", sessionID))
}
