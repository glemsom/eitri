# Split `render_component` into per-component tools (render_mermaid_diagram, render_quick_replies, render_diff_card)

## Status

Accepted

## Context

ADR-0011 defined `render_component` as a single tool with `name` and `data` fields. In practice, the LLM sees a weak schema (`data: object`) and must guess which fields each component expects — `data.code` for Mermaid, `data.options` for QuickReplies, `data.old`/`data.new` for DiffCard. Wrong guesses waste turns on validation errors.

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

## Consequences

- Tool count goes from 8 to 10 — still manageable, each is single-purpose
- Adding a new component requires a new tool registration in `runner/service.go` and a switch case in `handleRender`
- The `render_component` Go file is deleted; replaced by three files
- The agent loop in `loop.go` gets a generic component-emission hook keyed by tool name, not a special case per tool
