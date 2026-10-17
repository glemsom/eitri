# Expose skills to the model through a first-class `skill` tool

Eitri's agent loaded skills by running `bash cat` on a `SKILL.md` path inferred from a name/description index, steered by a `## Skills` system-prompt section. The wider Agent Skills ecosystem is written against an invocable tool ("call the Skill tool with `tdd`"), so the model had to translate that request into a manual file read — a fragile step that caused skills to fail to load. We expose a model-facing `skill` tool that activates a skill by name and returns the same payload the human `/skillname` path injects: the skill's instructions, its bundled-resource listing, and the absolute **skill directory**. The activation mechanics move out of the system prompt into the tool description; the system prompt keeps only the two reach-for-skill pointers.

Activity is derived from the conversation, not stored: a skill is active while its `<skill_content>` block is present among the session's messages (any role). The engine computes the active set at dispatch and supplies it to the tool through the context; the tool applies the policy. This resets on a new session (fresh history), lets a compacted-away body be re-activated, and treats the human path's pinned body as active. A repeat activation returns a short, non-error note repeating the skill directory instead of re-injecting the body. Only model-invocable skills are activatable; a human-only or unknown name is rejected.

## Consequences

- A new strict-schema tool named `skill` (lowercase, matching OpenCode and Eitri's conventions) joins `bash` and `open_in_browser`; the human slash path is otherwise unchanged and still always re-applies.
- The activation block's marker and the active-set context handoff live in `internal/skillindex`, beside the index marker, so the renderer (`internal/tools`), the history partitioner, and the engine's activity scan cannot drift.
- `internal/engine` still does not import `internal/tools`: the active set rides the existing `ToolExecutor` context.
- Batch mode and subagents share the registry, so the tool is available there without further wiring.

## Considered options

- Keep the `bash cat` path and the system-prompt mechanics (rejected: it is the fragile translation step this change removes).
- Store the active set in the tool or registry (rejected: it would survive compaction and `/new` incorrectly; the conversation is the source of truth).
- Capitalize the tool as `Skill` to match Claude Code (rejected: Eitri's tools are lowercase and OpenCode uses `skill`).
- Carry the directory only in a separate `<skill_resources>` attribute (rejected: the content tag is the block's anchor, and the model needs the directory even when a skill bundles no resources).
