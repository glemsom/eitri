# Refined toolset: read, glob, grep, write, edit, bash, render_component, skill

Replace the 5 current tools (`file_viewer`, `file_editor`, `terminal_execute`, `render_component`, `activate_skill`) with a refined set of 8 tools that are simpler, more intent-aligned, and harder for the LLM to misuse.

**Status**: Partially superseded

> **Superseded sections**:
> - The `bash` tool described below (tmux-backed, 180s timeout) was replaced with direct `exec.Command` execution by [ADR-0015](0015-remove-tmux-executor.md) (60s default timeout, no persistent shell state, stdout/stderr separation).
> - The `render_component` single-tool design was replaced by per-component tools (`render_mermaid_diagram`, `render_quick_replies`) in [ADR-0012](0012-split-render-component-into-per-component-tools.md).

## Context

Eitri's current 5 tools (ADR-0010, `internal/tool/`) emerged organically from the ADK era. Several pain points:

- **`file_viewer` is overloaded** — read files, list directories, show line info. The LLM often passes wrong args.
- **`file_editor` has too many modes** — create, overwrite, edit, insert. LLMs frequently misuse overwrite for tiny changes, wiping files.
- **No `glob` or `grep`** — finding files and searching code requires shell commands, which are slower and harder to parse.
- **`activate_skill` is confusing** — LLMs don't always know when to call it. Making it explicit as `skill` clarifies the contract.

The goal is a tool set where each tool has one clear job, the schema is minimal, and the system prompt + tool descriptions steer the LLM toward correct usage by default.

## Decision

### Tool definitions

| Tool | Purpose | Key params |
|------|---------|------------|
| `read` | Read a file with line-range metadata | `path`, `start_line`, `end_line` |
| `glob` | Find files matching a pattern | `pattern` |
| `grep` | Search file contents with regex | `pattern`, `file_pattern` |
| `write` | Create new file or complete overwrite | `path`, `content` |
| `edit` | Precise search-and-replace on existing file | `path`, `old_text`, `new_text` |
| `bash` | Execute shell command in session tmux | `command` |
| `render_component` | Render generative UI component | `name`, `data` |
| `skill` | Load a skill's instructions and resources | `name` |

### Read tool (replaces `file_viewer` read mode)

- `path` (string, required): File path relative to workspace.
- `start_line` (int, optional, default 1): 1-indexed start line.
- `end_line` (int, optional, default 100): 1-indexed end line.

Response metadata: when the file has more lines than `end_line`, the output is prefixed with `[file: <path>, lines <start>-<end> of <total>]` so the LLM knows to continue reading. No `include_line_info` or anchors — `edit` uses exact block matching instead.

### Glob tool (new)

- `pattern` (string, required): Glob pattern relative to workspace root. Supports `*`, `?`, `[...]`. Double-star `**` is not in scope for v1 (standard `filepath.Match`-compatible patterns only).

Returns sorted list of matching file paths, one per line.

### Grep tool (new)

- `pattern` (string, required): Regex pattern to search for.
- `file_pattern` (string, optional): Glob to filter files (e.g. `*.go`). If omitted, searches all files. The pattern is matched against the filename only (not the full path), so `*.go` matches `.go` files in any subdirectory. For path-prefixed patterns (e.g. `internal/*.go`), the full relative path is also checked.

Returns `file:line:content` results sorted by file and line. Output is capped at 128 KiB with truncation marker.

### Write tool (replaces `file_editor` create + overwrite)

- `path` (string, required): File path relative to workspace.
- `content` (string, required): File content.

**Behavioral guardrail** (in tool description, not system prompt): "Create a brand new file, or completely overwrite an existing file if a complete rewrite is cleaner and faster than performing multiple search-and-replace edit operations. Do not use this tool for making minor line adjustments to existing files."

Go enforcement: if the file exists, allow overwrite. The guardrail is advisory — the tool works either way; the description steers the LLM.

### Edit tool (replaces `file_editor` edit + insert modes)

- `path` (string, required): File path relative to workspace.
- `old_text` (string, required): Exact block of text to find and replace. Should include several surrounding lines to ensure uniqueness.
- `new_text` (string, required): Replacement text for the matched block.

**Strict exact match only, v1.** No fuzzy matching, no stretch matching. If `old_text` matches 0 or more than 1 time, the tool returns an error with context showing what was found nearby. This prevents subtle corruption bugs. Fuzzy/stretch matching can be added in a later iteration.

The LLM is instructed (via tool description): "Make precise, targeted modifications to an existing file using search-and-replace blocks. Do not use this to write new files. Always include several surrounding lines of code as context anchors in 'old_text' to avoid ambiguity."

### Bash tool (rename of `terminal_execute`)

- `command` (string, required): Shell command to run.

Same implementation as current `terminal_execute`: session-scoped tmux, 180s default timeout (up from 60s), 128 KiB output cap, concurrent-command rejection, session-death recovery.

### Render component tool (unchanged)

- `name` (string, required): Component name: `MermaidDiagram`, `QuickReplies`, `DiffCard`.
- `data` (object, required): Component data.

Same implementation as current `render_component`.

### Skill tool (replaces `activate_skill`)

- `name` (string, required): Name of the skill to activate.

Returns structured string: the skill's `SKILL.md` body plus a resource manifest (scripts, references, assets). Activation persists in the session across runs, matching current `activate_skill` behavior.

### Tool descriptions

Short, precise, action-oriented. Examples:

- `read`: "Read a file from the workspace. Returns the specified line range. If the file has more content, a metadata prefix shows total line count so you can continue reading."
- `edit`: "Make precise, targeted modifications to an existing file using search-and-replace blocks. Always include several surrounding lines of code as context anchors in 'old_text' to avoid ambiguity."
- `write`: "Create a brand new file, or completely overwrite an existing file if a complete rewrite is cleaner and faster than performing multiple search-and-replace edit operations. Do not use this tool for making minor line adjustments to existing files."

### Output format consistency

All tools return plain text content blocks. The agent loop wraps results in `ToolResultBlock` for LLM consumption. No structured JSON responses — text is simplest for LLMs to parse and reason about.

## Considered Options

### Keep `file_viewer` and `file_editor` with minor tweaks

Rejected because the overloaded-modes problem is structural. Adding more modes to fix it would make things worse. Splitting into single-responsibility tools is cleaner.

### Add anchors back to edit tool

Rejected. LLMs struggle with line-number anchors that shift during edits (Aider's research confirms this). Block-level search-and-replace with surrounding context is the de facto standard in modern coding agents.

### Add regex-mode to grep but not to edit

Intentional. `grep` is for exploration, where regex is essential. `edit` is for modification, where regex could cause subtle corruption (escaping, greediness). Strict literal block matching is safer.

### Support `**` in glob v1

Rejected for simplicity. Standard `filepath.Match` glob is trivial to implement without dependencies. `**` support can be added via `doublestar` if needed.

## Consequences

### Positive

- Each tool has one job. LLM confusion about mode selection disappears.
- `edit` with explicit block matching is simpler than current edit+insert+anchor logic.
- `glob` + `grep` reduce shell commands for file discovery, saving context and latency.
- `skill` as a named tool clarifies the contract — LLM explicitly requests skills rather than hoping they auto-activate.
- 180s default bash timeout reduces false timeouts for long-running commands.
- Tool descriptions as the primary steering mechanism keeps system prompts clean.

### Risks

- **More tools = more LLM choices**: 8 vs 5 tools increases decision space. Mitigated by clear tool descriptions that make selection obvious.
- **`edit` strict match may frustrate LLMs**: if the LLM can't produce exact match, it burns turns. Mitigated by clear description guidance to include surrounding context.
- **`write` advisory guardrail won't stop bad LLMs**: a bad model will overwrite files anyway. A future permission model (confirm dialog before write) would solve this regardless of tool shape.
- **No `**` in glob**: deep directory searches require `bash find`. Mitigated by `grep` which handles recursive content search.

### Migration

1. Write new tool implementations in `internal/tool/` alongside existing ones (old and new coexist temporarily).
2. Update `runner/loop.go` to use new tool registry.
3. Update `runner/service.go` to build new tool set.
4. Remove old tool implementations (`file_viewer.go`, `file_editor.go`, `terminal_execute.go`, `activate_skill.go` — keep `render_component.go`).
5. Update ARCHITECTURE.md and CONTEXT.md documentation.
6. Update existing tests; add new tests for glob and grep tools.
