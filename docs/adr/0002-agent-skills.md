# ADR 0002: Agent Skills support

## Status

Accepted

## Context

Eitri needs to support Agent Skills as defined by the public specification at <https://agentskills.io/specification>. A skill is a directory containing a required `SKILL.md` file with YAML frontmatter and Markdown instructions, plus optional `scripts/`, `references/`, and `assets/` directories.

Eitri is a local, direct-host coding agent. Skills can affect model behavior and may reference local scripts/resources. Project-level skills can be supplied by a repository, while user-level skills are shared across projects.

## Decision

Eitri supports Agent Skills as a first-class feature with actual agent use, not only UI listing.

### Discovery roots

Eitri scans fixed roots in this precedence order:

1. `<workspace>/.eitri/skills/`
2. `<workspace>/.agents/skills/`
3. `~/.eitri/skills/`
4. `~/.agents/skills/`

The `.agents/skills/` roots provide cross-client interoperability. The `.eitri/skills/` roots provide an Eitri-native escape hatch for behavior that should not affect other clients.

Missing directories are ignored. Unreadable directories and malformed skills produce diagnostics but do not block startup or chat.

### Collision handling

If multiple skills share the same `name`, the highest-precedence skill is effective. Lower-precedence records are marked `shadowed`. Only effective loaded skills appear in the model prompt catalog and are valid `activate_skill` targets.

### Trust model

Eitri does not require a workspace trust gate for project-level skills initially. Project skills are loaded automatically. The Skills UI must show detected skills, including scope and path, so users can notice unexpected repository-provided skills and act manually.

### Disable/enable controls

Skills can be disabled via the `Service.SetDisabled()` API, which moves them from the effective registry to a disabled list. Disabled skills are:

- Excluded from `Lookup()` results
- Excluded from `Effective()`, `Summary()`, and `SkillsCatalogXML()`
- Excluded from `SkillDirectories()` (file_viewer path validation)
- Added to the `Registry.Disabled()` list

The disabled set is persisted in config as `disabled_skills` and round-trips through save/load.

Custom paths remain deferred.

### Parsing and validation

Eitri uses lenient validation with hard minimums:

- Skip if `SKILL.md` is missing, YAML frontmatter is missing/unparseable, `name` is missing/empty, or `description` is missing/empty.
- Warn but load when strict spec constraints are violated but enough metadata exists to use the skill.
- Parse optional `license`, `compatibility`, `metadata`, and `allowed-tools`.
- Treat `allowed-tools` as advisory only initially.

This favors interoperability with skills authored for other clients while surfacing diagnostics to users.

### Activation mechanism

Eitri uses a dedicated `activate_skill(name)` built-in tool rather than relying on raw file reads.

Reasons:

- Validate names against the effective registry.
- Deduplicate activation per session.
- Track active skills as session state.
- Return body-only instructions in a structured wrapper.
- Include skill directory and resource manifest without eagerly loading resources.
- Enforce body/resource limits.

`activate_skill` returns structured content with:

- skill name
- absolute skill directory
- advisory `allowed-tools`, if present
- Markdown body from `SKILL.md` without frontmatter
- resource manifest for `scripts/`, `references/`, and `assets/`
- path-resolution instructions

Activation fails when the skill body exceeds 200KB. Resource manifests are capped at 200 files and depth 4.

### Prompt disclosure

At run start, Eitri injects a compact catalog of effective loaded skills into the system prompt. The catalog includes `name` and `description` only. The prompt instructs the model to call `activate_skill` before proceeding when a task matches a skill description.

If no skills are available, Eitri omits the catalog and skill instructions.

### Session persistence

Each chat session stores active skill names. Active skills are deduplicated and re-applied on every run in that session by resolving names against the current effective registry. Server restart loses active skills along with in-memory sessions.

If an active skill disappears, Eitri skips it for that run and surfaces a warning.

### Slash activation

Users can activate skills explicitly in chat input:

- `/skill-name` activates without starting an LLM run.
- `/skill-name prompt text...` activates, then sends the remaining prompt.
- Multiple leading slash skills activate in order.
- Unknown skill-shaped slash commands return `422 Unknown skill or command`.

### Resource access

`file_viewer` may read workspace files and files under effective/active skill directories. `file_editor` remains workspace-only. Skill scripts are never executed automatically; the agent may run scripts through `terminal_execute` under Eitri's existing direct-host execution model.

## Consequences

### Positive

- Skills are interoperable with other Agent Skills-compatible clients.
- Eitri-specific skills can coexist with cross-client skills.
- Model context stays small through progressive disclosure.
- Users can audit detected skills in the UI.
- Dedicated activation gives Eitri a clean seam for future permission/trust features.

### Negative

- Project repositories can influence agent behavior without a trust prompt initially.
- `allowed-tools` is not enforced, so users must not treat it as a security boundary.
- Runner prompt construction now depends on skills registry state.
- File read validation must account for skill directories in addition to workspace paths.

### Future work

- Workspace trust gate for project-level skills.
- Per-skill enable/disable.
- Custom skill roots.
- Deactivate active skills.
- Skill install/import UI.
- Enforcement of `allowed-tools` after a real permission model exists.
