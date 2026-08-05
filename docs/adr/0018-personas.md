# 0018 — Personas: named system prompts with skill injection

**Status**: Accepted

## Context

Eitri has a single system prompt shared across all sessions and sub-agents; the settings override is global and sub-agents always inherit the parent's prompt. This forces a trade-off between a generic prompt for all tasks and specialising at the cost of generality — users cannot switch behaviour per task (code review, research, debugging, tutoring) without manually editing the system prompt, and a sub-agent cannot adopt a different tone, role, or skill set than its parent.

## Decision

Introduce **personas** — named bundles of a **system prompt** (core instructions) and a list of **required skills** (skill names pre-loaded into the system prompt whenever the persona is active).

### Persona definition

One YAML file per persona in `~/.eitri/personas/<name>.yaml` (user-level only — personas are behaviour preferences, not project capabilities; no workspace-scoped directory):

```yaml
name: reviewer
system_prompt: |
  You are a strict code reviewer. Focus on correctness, performance,
  and maintainability. Be thorough but polite.
required_skills:
  - code-review
  - diagnosing-bugs
```

The `generic` persona is always present, auto-created at startup from the built-in default prompt if missing. Up to 10 custom personas (not counting `generic`), enforced at save time in both UI and backend.

### Config

The config object adds `active_persona` and a `persona_catalog` of references (`"reviewer": ".eitri/personas/reviewer.yaml"`); the existing `system_prompt` field is kept as a temporary per-session override that takes precedence over the persona's prompt.

### System prompt assembly

When a persona is active, the system prompt is assembled in this order:

1. **Persona system prompt** (from the persona file)
2. **User override** (`cfg.SystemPrompt` — temporary inline override, one session)
3. **Repository instructions** (`CONTEXT.md` / repository instructions file)
4. **Skills catalog** (available skills)
5. **Required skills** — a `<required_skills>` startup directive instructing the agent to call `skill("name")` for each required skill on its first turn. Skills are NOT pre-injected with content; the agent loads them via the `skill()` tool, establishing commitment through the tool-call result.
6. **Activated skills** (from `skill()` calls during the conversation)

Required skills go through the same `Activations` mechanism as manual activation; deduplication ensures a skill's instructions appear at most once.

### Lifecycle

| Action | Behaviour |
|--------|-----------|
| Create / Edit | Manage Personas UI writes `.eitri/personas/<name>.yaml`; config catalog updated. |
| Delete | File removed; catalog updated; orphaned sessions continue with their last persona. |
| Select | `active_persona` updated; takes effect on next run. |
| Switch mid-session | System prompt replaced in the running session (same session ID, new prompt from next turn); old messages stay. |

### Subagent persona

`delegate()` may specify a persona (`{"task": "...", "persona": "debugger"}`). The sub-agent resolves the persona by reading `.eitri/personas/<name>.yaml` from the shared workspace; missing or corrupt files fall back to `generic` with a logged warning. The parent passes only the name — no serialized content — keeping the parent's token cost low and ensuring the sub-agent reads the latest version.

## Consequences

Positive:

- Specialised agents per task without editing the system prompt; multi-role sub-agent workflows in a single session.
- File-per-persona on disk enables version control, sharing, and backup; backward compatible — existing users see no change.
- Required skills reuse the existing activation machinery; minimal new code.

Negative:

- Persona system prompts consume context window tokens, especially with required skills.
- Shared workspace across personas can surprise new users (a "reviewer" and a "tutor" persona operate on the same files — intentional, not an isolation boundary).
- `.eitri/personas/` files are unencrypted on disk.

### Risks

- **Skill name drift** — a renamed/removed skill is silently skipped (lookup returns nil). Mitigation: the UI multi-select shows only existing skills; stale references are logged at persona load time.
- **Config file bloat** — the catalog grows linearly with personas. Mitigation: the 10-persona limit.
- **Race conditions** — two tabs saving personas simultaneously could clobber each other. Mitigation: persona files follow the existing atomic-write pattern (temp file + rename).
