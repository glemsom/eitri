# 0002 — Agent Skills support

**Status**: Accepted

## Context

Eitri needs to support Agent Skills as defined by the public specification at <https://agentskills.io/specification>. A skill is a directory containing a required `SKILL.md` file with YAML frontmatter and Markdown instructions, plus optional `scripts/`, `references/`, and `assets/` directories.

Eitri is a local, direct-host coding agent. Skills can affect model behavior and may reference local scripts/resources. Project-level skills can be supplied by a repository; user-level skills are shared across projects.

## Decision

Eitri supports Agent Skills as a first-class feature with actual agent use, not only UI listing.

### Discovery roots

Scanned in precedence order. Missing directories are ignored; unreadable directories and malformed skills produce diagnostics but never block startup or chat:

1. `<workspace>/.eitri/skills/` — Eitri-native escape hatch
2. `<workspace>/.agents/skills/` — cross-client interoperability
3. `~/.eitri/skills/`
4. `~/.agents/skills/`

### Collision handling

On name collision, the highest-precedence skill wins; lower-precedence records are marked `shadowed`. Only effective skills appear in the model prompt catalog and are valid `activate_skill` targets.

### Trust model

No workspace trust gate initially — project skills load automatically. The Skills UI shows detected skills with scope and path so users can notice unexpected repository-provided skills.

### Disable/enable

`Service.SetDisabled()` moves a skill from the effective registry to a disabled list, excluding it from lookup, catalog, and file_viewer path validation. The disabled set persists as `disabled_skills` in config.

### Parsing and validation

Lenient validation with hard minimums: skip when `SKILL.md`, YAML frontmatter, `name`, or `description` is missing. Warn but load when strict spec constraints are violated yet enough metadata exists. `license`, `compatibility`, `metadata`, and `allowed-tools` (advisory only) are parsed.

### Activation

A dedicated `activate_skill(name)` built-in tool (not raw file reads) validates names against the effective registry, deduplicates per session, and returns body-only instructions plus skill directory, advisory `allowed-tools`, and a resource manifest for `scripts/`, `references/`, and `assets/` — without eagerly loading resources. Activation fails when the body exceeds 200 KB; manifests are capped at 200 files, depth 4.

### Prompt disclosure

At run start the system prompt carries a compact catalog (name + description only) of effective skills and directs the model to call `activate_skill` when a task matches a skill description. The catalog is omitted entirely when no skills are available.

### Session persistence

Each session stores active skill names and re-applies them on every run against the current effective registry. A skill that disappears is skipped with a warning. Active skills die with the in-memory session on restart.

### Slash activation

`/skill-name` activates without starting a run; `/skill-name prompt text...` activates then sends the prompt; multiple leading slash skills activate in order. Unknown skill-shaped commands return `422`.

### Resource access

`file_viewer` may read workspace files and files under effective/active skill directories; `file_editor` stays workspace-only. Skill scripts are never executed automatically — the agent runs them via `terminal_execute` under Eitri's existing direct-host execution model.

## Consequences

- Interoperable with other Agent Skills-compatible clients; Eitri-specific skills coexist via the `.eitri/` roots.
- Model context stays small through progressive disclosure; users can audit detected skills in the UI; the activation seam leaves room for future permission/trust features.
- Project repositories can influence agent behavior without a trust prompt; `allowed-tools` is not a security boundary.
- Runner prompt construction depends on skills registry state; file-read validation must account for skill directories.

## Non-goals

Workspace trust gate for project skills, per-skill enable/disable, custom skill roots, deactivating active skills, skill install/import UI, enforcement of `allowed-tools`.
