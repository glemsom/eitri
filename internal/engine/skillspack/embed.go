// Package skillspack embeds the builtin Agent Skill packs that ship inside the
// binary. Each pack is one directory whose SKILL.md uses the same frontmatter
// the discovery loader expects. The binary owns this content: later stages
// materialize it to disk before discovery so the normal skill roots can span
// it, and edits to a materialized copy are reverted on the next launch.
package skillspack

import "embed"

// FS is the embedded builtin skill packs, one directory per skill.
//
//go:embed subagents web-access
var FS embed.FS
