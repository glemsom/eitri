# Split `render_component` into per-component tools (render_mermaid_diagram, render_quick_replies, render_diff_card)

## Status

Accepted

## Context

An earlier ADR defined `render_component` as a single tool with `name` and `data` fields. In practice, the LLM sees a weak schema (`data: object`) and must guess which fields each component expects — `data.code` for Mermaid, `data.options` for QuickReplies, `data.old`/`data.new` for DiffCard. Wrong guesses waste turns on validation errors.

Additionally, the tool never actually emitted SSE `component` events — it returned stub text `[Component rendered: %s]` but the agent loop had no code to broadcast a visual component to the browser. The fix for that bug also makes a per-tool dispatch structure cleaner (tool name maps directly to component name).

## Decision

Replace the single `render_component` tool with three tools, each with a precise typed schema:

| Tool | Params | Emitted component |
|------|--------|-------------------|
| `render_mermaid_diagram` | `code: string` | MermaidDiagram |
| `render_quick_replies` | `options: []string` | QuickReplies |
| `render_diff_card` | `old: string, new: string, lang?: string` | DiffCard |

Each tool:
- Has a typed Go struct with `SchemaOf[T]()` — LLM sees exact required fields
- Validates at compile time, not at runtime
- Maps 1:1 to a Templ component in the render handler
- Emits an SSE `component` event from the agent loop after successful call
- Returns structured text to LLM describing what rendered (e.g. "Rendered MermaidDiagram with graph TD; A-->B;")

## Considered Options

- **Keep single `render_component` with improved docs**: Schema stays weak — LLM still has no structural hints. Descriptions alone don't prevent field-name guesswork.
- **Discriminated union in one tool**: JSON Schema `oneOf` per component shape. Clean but unsupported by `SchemaOf[T]()` reflection — would require manual schema construction.
- **Per-component tools (chosen)**: Each tool is a struct with one job. LLM picks by intent. Zero runtime validation. New components simply add a new tool.

## Reversal (2026-07-17): `render_diff_card` removed, `edit` emits richer `FileEditCard`

`render_diff_card` was later removed as an LLM-facing tool. The LLM rarely,
if ever, chooses to show a diff independently of editing. The `edit` tool
already auto-emits a diff component — the LLM uses `edit` for its primary
purpose and gets the diff as a side effect.

Replaced `DiffCard` emission from the `edit` tool with `FileEditCard`, which
wraps the diff viewer with file path, mode (overwrite/create), and byte count.
This gives the user more context at a glance.

## Consequences

- Tool count goes from 8 to 9 — still manageable, each is single-purpose (8 - 1 old `render_component` + 2 active replacements = 9; `render_diff_card` was removed, see reversal above)
- Adding a new component requires a new tool registration in `runner/service.go` and a case in `loop_helpers.go` component-name map
- The `render_component` Go file is deleted; replaced by two files (`render_mermaid_diagram.go`, `render_quick_replies.go`)
- The agent loop in `loop.go` gets a generic component-emission hook keyed by tool name, not a special case per tool
