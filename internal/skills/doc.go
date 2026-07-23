// Package skills implements Agent Skills discovery, YAML frontmatter parsing,
// precedence resolution, and resource manifest generation.
//
// ## Purpose
//
// This package owns the full lifecycle of Agent Skills:
//   - Discovering skill directories from fixed root locations
//   - Parsing SKILL.md files (YAML frontmatter + Markdown body)
//   - Resolving precedence when multiple roots provide same-named skills
//   - Building a Registry that tracks effective, shadowed, invalid, and disabled skills
//   - Generating resource file listings (scripts, references, assets)
//   - Parsing slash-command input for per-session skill activation
//
// ## Key types
//
//   - Skill     — a parsed Agent Skill with Name, Description, Body, Scope, Status, etc.
//   - Root      — a fixed discovery root directory with an associated Scope
//   - Registry  — resolved skill state: effective, shadowed, invalid, disabled, diagnostics
//   - Service   — thread-safe facade that manages discovery, registry refresh, skill lookup,
//                 and disabled-list persistence
//   - Scope     — origin scope of a skill root (project-eitri, project-agents, user-eitri,
//                 user-agents), used for precedence ordering
//   - Status    — loading status of a skill (effective, shadowed, invalid, disabled)
//   - Diagnostic / Diagnostics — validation and discovery diagnostics
//   - SkillSummary       — lightweight representation for system-prompt catalogs
//   - ActivatedSkill     — a skill activated for a session (name + instructions + resources)
//   - SlashParseResult   — parsed result of a slash-command input string
//
// ## Key interfaces
//
// This package defines no interfaces. The Service struct is consumed directly by
// the runner and HTTP layers.
//
// ## Dependencies
//
//   - gopkg.in/yaml.v3 — YAML frontmatter parsing
//
// No internal/eitri packages are imported.
//
// ## Extension points
//
//   - Add a new discovery root: add an entry to defaultRoots() with the appropriate Scope.
//   - Add a new resource directory: append to the resourceDirs slice in resources.go.
//   - Add a new status or severity: add a const to the Status or Severity type.
package skills
