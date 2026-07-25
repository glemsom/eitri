# ADR-0018: Personas — named system prompts with skill injection

**Status**: Accepted

## Context

Eitri currently has a single system prompt, shared across all sessions and
subagents. The user can override it in settings, but that override is global:
it affects every conversation, and subagents always inherit the same prompt.

This flat design forces a trade-off: either keep a generic prompt for all
tasks, or specialise and lose generality. Users who want different
behaviour for different tasks (code review, research, debugging, tutoring)
have no way to switch without manually editing the system prompt every time.

Additionally, subagents always run with the same persona as the parent.
If a user wants a subagent to adopt a different tone, role, or skill set
(e.g., spawn a "debugger" subagent while the main session stays on
"reviewer"), there is no mechanism to express that.

## Decision

Introduce **personas** — named bundles of:

1. A **system prompt** (the persona's core instructions).
2. A list of **injected skills** — skill names to pre-load into the
   system prompt whenever this persona is active.

### Concepts

#### Persona definition

A persona is stored as a single YAML file in `<workspace>/.eitri/personas/<name>.yaml`:

```yaml
name: reviewer
system_prompt: |
  You are a strict code reviewer. Focus on correctness, performance,
  and maintainability. Be thorough but polite.
injected_skills:
  - code-review
  - diagnosing-bugs
```

The `generic` persona is always present. If no `.eitri/personas/generic.yaml`
file exists, it is created at startup with the current built-in default
prompt and no injected skills.

#### Persona catalog

The config object adds a `personas` field alongside the existing
`system_prompt`:

```json
{
  "system_prompt": "",           // kept for backward compat → used as override
  "active_persona": "generic",   // which persona is active for the current session
  "persona_catalog": {           // references; actual content is on disk
    "generic":  ".eitri/personas/generic.yaml",
    "reviewer": ".eitri/personas/reviewer.yaml",
    "debugger": ".eitri/personas/debugger.yaml"
  }
}
```

#### System prompt assembly

When a persona is active, the system prompt is assembled in this order:

1. **Persona system prompt** (from the persona file)
2. **User override** (from `cfg.SystemPrompt` — a temporary inline override
   that takes precedence over the persona's prompt for one session only)
3. **Repository instructions** (from `CONTEXT.md` / repository instructions file)
4. **Skills catalog** (available skills, same as today)
5. **Injected skills** (pre-loaded via the activation mechanism, same as
   `skill()` calls would do, but injected automatically)
6. **Activated skills** (from `skill()` calls during the conversation)

If a persona has injected skills, those skills are loaded via the existing
`sessionSkillContext.Activations` mechanism. Deduplication ensures a skill's
instructions appear at most once, whether injected or manually activated.

#### Persona lifecycle

| Action | Behaviour |
|--------|-----------|
| Create | User opens "Manage Personas" UI, fills in name + prompt + injected skills. Saved to `.eitri/personas/<name>.yaml`. Config catalog updated. |
| Edit | Same as create but overwrites existing file. |
| Delete | File removed from `.eitri/personas/`. Config catalog updated. Orphaned sessions continue with their last persona. |
| Select | Config's `active_persona` updated. Takes effect on next run. |
| Switch mid-session | Replaces the system prompt in the running session (same session ID, new prompt from next turn). Old messages stay. |

#### Subagent persona

When a parent spawns a subagent via `delegate()`, it may specify a persona:

```json
{
  "task": "Debug the login module...",
  "persona": "debugger"
}
```

The subagent resolves the persona by reading `.eitri/personas/debugger.yaml`
from the shared workspace. If the persona file is missing or corrupt, the
subagent falls back to `generic` with a logged warning.

The parent does **not** serialize the persona content — it passes only
the name. This keeps the parent's token cost low and ensures the subagent
always reads the latest version.

#### User-defined limit

Up to 10 custom personas (not counting `generic`). Enforced at save time
in both the UI and the backend API.

### UI changes

| UI element | Location | Behaviour |
|------------|----------|-----------|
| Persona selector | Top bar, next to workspace selector | Dropdown listing `generic` + all custom personas. Current selection highlighted. |
| Manage Personas | Settings page, new section | List of personas with add/edit/delete. Each entry: name (text), system prompt (textarea), injected skills (multi-select from discovered skills). |
| Skill multi-select | Inside persona editor | Fetches available skills from backend, presents checkboxes/combobox. Saves as list of skill names. |

### Files on disk

```
.workspace/
  .eitri/
    personas/
      generic.yaml
      reviewer.yaml
      debugger.yaml
    config.json          # existing, adds persona_catalog + active_persona
```

### Impact on existing users

- The `system_prompt` config field remains for backward compatibility.
  If set, it acts as a **session override** on top of the active persona.
  On first run after upgrade, `generic` persona is created from the
  current default prompt.
- Users who never touch personas see no change in behaviour.
- The `DefaultSystemPrompt` constant in `history/session.go` becomes
  the template for auto-creating `generic` on first run. Other code
  that references it should continue to work as a fallback.

### Migration

On startup, the server checks for `.eitri/personas/`. If missing, it:

1. Creates the directory.
2. Writes `generic.yaml` with the built-in default prompt.
3. Adds `generic` to the persona catalog if not already present.
4. Sets `active_persona` to `"generic"` if unset.

If an existing `system_prompt` override is present in config.json,
it is preserved as the session override and takes precedence over
the persona's prompt (but does not modify the persona file).

### Open questions resolved

| # | Question | Resolution |
|---|----------|------------|
| Q14 | How does subagent get its persona? | Reads `.eitri/personas/<name>.yaml` from disk. Parent passes name only. |
| Q16 | Is `generic` on disk? | Yes. Auto-created if missing. |
| Q17 | Does persona replace dynamic parts? | No. Dynamic parts (tools, workspace context, skills) are appended. |
| Q18 | Skill deduplication? | Injected skills go through the same `Activations` mechanism; dedup ensures unique content. |
| Q20 | Config shape? | `system_prompt` kept as override. `persona_catalog` + `active_persona` added. |
| Q23 | Switch mid-session? | Same session, new system prompt from next turn. Old messages retained. |
| Q24 | Injected skills mechanism? | Via `Activations` — same path as manual `skill()` activation, just injected automatically. |

## Consequences

### Positive

- Users can define specialised agents for different tasks without editing
  the system prompt every time.
- Subagents can be spawned with different personas, enabling multi-role
  workflows in a single session.
- Shared workspace means all personas can access the same files; personas
  are a behaviour switch, not an isolation boundary.
- File-per-persona on disk enables version control, sharing, and backup.
- Backward compatible — existing users see no change.
- Injected skills reuse the existing activation machinery; minimal new code.

### Negative

- Persona system prompts consume context window tokens — especially with
  injected skills. Power users who inject many skills per persona may hit
  the context limit faster.
- Shared workspace across personas can lead to confusion: a "reviewer"
  persona and a "tutor" persona operate on the same files. This is
  intentional but may surprise new users.
- `.eitri/personas/` files are unencrypted on disk. System prompt content
  may contain sensitive instructions (though unlikely to contain secrets).

### Risks

- **Skill name drift**: If a skill is renamed or removed, persona
  definitions that reference it by name will silently skip injection
  (skill lookup returns nil). Mitigation: UI shows only existing skills
  in the multi-select; stale references are logged at persona load time.
- **Config file bloat**: The persona catalog object in config.json grows
  linearly with personas. Mitigation: 10-persona limit keeps it small.
- **Race conditions**: Two browser tabs saving personas simultaneously
  could clobber each other on disk. Mitigation: existing config save
  already uses atomic write (temp file + rename). Persona files should
  follow the same pattern.

## Implementation sketch

### Phase 1 — Data model and persistence
1. Add `PersonaDefinition` struct (name, system prompt, injected skills).
2. Add `persona_catalog` (map[string]string) and `active_persona` to `config.Config`.
3. Implement `persona.Load(name)` and `persona.Save(definition)` in a new
   `internal/persona` package.
4. Implement `persona.EnsureGeneric()` for first-run setup.
5. Add `persona.List()` to enumerate `.eitri/personas/*.yaml`.

### Phase 2 — System prompt assembly
1. Modify `buildSystemPrompt` in `internal/runner/system_prompt.go` to
   accept a persona name, load the persona, and inject skills via
   `sessionSkillContext.Activations`.
2. Add deduplication to skill activation: if a skill is already in
   `Activations`, skip re-adding.
3. Pass `active_persona` through `RunConfig`.

### Phase 3 — Subagent persona support
1. Extend `delegate()` tool to accept an optional `persona` field.
2. In `SpawnSubAgent`, resolve persona from disk and use its prompt +
   injected skills instead of the parent's.
3. Ensure subagents that cannot load their persona fall back to `generic`.

### Phase 4 — UI
1. Add persona selector dropdown to the chat top bar.
2. Add "Manage Personas" section to the settings page.
3. Implement add/edit/delete flows with HTMX.
4. Add skill multi-select (populated from `GET /api/skills` or similar).

### Phase 5 — Cleanup
1. Deprecate the standalone `system_prompt` textarea in settings
   (keep for backward compat but visually indicate it's a session override).
2. Remove `DefaultSystemPrompt` from `history/session.go` once all paths
   resolve through the persona layer.
