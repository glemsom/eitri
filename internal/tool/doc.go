// Package tool provides the ToolHandler interface, SchemaOf[T] helper, and
// built-in tool implementations that replace the ADK functiontool system.
//
// ## Purpose
//
// This package owns the contract and dispatch for all tools the agent can invoke:
//   - ToolHandler — the interface every built-in tool must implement
//   - Registry    — name-based tool dispatch and lifecycle (register, replace, lookup)
//   - SchemaOf[T] — compile-time JSON Schema generation from Go struct tags
//   - Built-in tools — bash, collect, delegate, edit, glob, grep, read, write,
//     web_fetch, skill, render_mermaid_diagram, render_quick_replies
//   - SubAgentManager — seam interface for sub-agent spawning (implemented by runner)
//
// ## Key types
//
//   - ToolHandler           — interface with Name, Description, JSONSchema, Call
//   - Registry              — dispatch map of tool handlers by name
//   - ToolResult            — struct with Blocks, IsError, NeedsConfirm flags
//   - SubAgentManager       — interface for spawning/collecting sub-agents
//   - SubAgentResult         — outcome of one sub-agent task
//
// ## Key interfaces
//
//   - ToolHandler       — contract that every built-in tool satisfies; see tool.go
//   - SubAgentManager   — seam that the runner package implements so tools can
//     spawn sub-agents without depending on the runner package directly
//
// ## Dependencies
//
// Internal:
//   - internal/fileutil — file system helpers (used by read, write, edit, glob, grep, ...)
//   - internal/session  — session types (used by delegate for child sessions)
//
// External:
//   - github.com/voocel/litellm — LLM block types (litellm.Block, litellm.Schema, etc.)
//
// ## Extension points
//
//   - Add a new built-in tool: create a file with a struct implementing ToolHandler,
//     then register it in the Registry (typically in init() or at startup).
//   - Add a new result renderer: add a render_*.go file and wire it into the SSE
//     component event stream alongside the existing renderers.
//   - Use SchemaOf[T] in any tool's JSONSchema() method to derive the schema
//     from a Go struct with json: and jsonschema: tags.
package tool
