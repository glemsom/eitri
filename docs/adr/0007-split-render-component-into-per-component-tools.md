# 0007 — Split `render_component` into per-component tools

**Status**: Accepted

## Context

`render_component` was a single tool with `name` and `data` fields. The LLM saw a weak schema (`data: object`) and had to guess per-component fields (`data.code` for Mermaid, `data.options` for QuickReplies, `data.old`/`data.new` for DiffCard), wasting turns on validation errors. The tool also never emitted SSE `component` events — it returned stub text the agent loop never broadcast.

## Decision

Replace `render_component` with per-component tools, each with a precise typed schema via `SchemaOf[T]()` — compile-time validation, 1:1 mapping to a Templ component, an SSE `component` event emitted after a successful call, and structured text returned to the LLM:

| Tool | Params | Emitted component |
|------|--------|-------------------|
| `render_mermaid_diagram` | `code: string` | MermaidDiagram |
| `render_quick_replies` | `options: []string` | QuickReplies |

`render_diff_card` was removed later: the LLM rarely shows a diff independently of editing, and the `edit` tool already auto-emits a diff as a side effect. That emission is now `FileEditCard` — the diff viewer wrapped with file path, mode (overwrite/create), and byte count, giving the user more context at a glance.

## Considered Options

- **Keep single `render_component` with improved docs**: schema stays weak — descriptions alone don't prevent field-name guesswork.
- **Discriminated union in one tool**: JSON Schema `oneOf` per component shape — clean but unsupported by `SchemaOf[T]()` reflection; would require manual schema construction.
- **Per-component tools (chosen)**: one struct per tool, LLM picks by intent, zero runtime validation; new components simply add a new tool.

## Consequences

- Adding a component requires a tool registration and a case in the component-name map; the agent loop emits components via a generic hook keyed by tool name, not per-tool special cases.
- The `render_component` Go file was deleted, replaced by `render_mermaid_diagram.go` and `render_quick_replies.go`.
