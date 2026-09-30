// Package skillspack embeds the builtin Agent Skill packs that ship inside the
// binary. Each pack is one directory whose SKILL.md uses the same frontmatter
// the discovery loader expects. The binary owns this content: later stages
// materialize it to disk before discovery so the normal skill roots can span
// it, and edits to a materialized copy are reverted on the next launch.
//
// A pack may restate a contract the repo docs own (the subagents pack repeats
// the batch envelope, exit codes, and the piped-stdin cap from
// docs/batch-mode.md). That duplication is deliberate: a pack is materialized
// into the data directory, where the repo docs do not resolve for the agent
// reading it. Keep such restatements to the facts the pack's own branches need.
package skillspack

import "embed"

// FS is the embedded builtin skill packs, one directory per skill.
//
//go:embed subagents web-access
var FS embed.FS
