# ADR 0005 — System prompt: embedded, byte-stable, token-budgeted

- Status: accepted
- Related: spec §34 request-head stability, §35 tail-message live state

## Context

The system prompt is the byte-stable head of the prompt-cache prefix (spec §34;
spec §217 treats prompt-caching as the lynchpin of Eitri's economics). It is the
single durable home for all model-facing operating instruction: anything not in
it must ride as a tail message (live state, §35) or a tool result (skill packs,
§25). Eitri's identity and working principles (see CONTEXT.md) live here.

## Decision

- **Embed the prompt** in the single binary via `//go:embed` of a checked-in
  markdown file (`internal/engine/prompt.md`), not a runtime-loaded data file.
  One reviewable, version-controlled source; byte-deterministic per build; no
  runtime surprises.
- **Keep it byte-identical for the session.** No time/dir/session state, no
  branch on batch-vs-TUI mode. Both run the same static head; live state and
  batch specifics are engine/UI concerns, never prompt content.
- **Cap the prompt at 1000 tokens**, enforced by a test that reads the embedded
  prompt and fails above the ceiling. Since every head token is paid every
  turn, "minimally-complete" gets a verifiable number rather than a taste call.
- **Structure: short smith-persona preamble + literal principles.** The persona
  (dwarven smith of the gods) anchors the voice; operative instructions stay
  literal so a flash-class model acts on them precisely. The persona is
  presentation, not a domain glossary term (CONTEXT.md).
- **Teach lightly:** one line each for skills and reasoning_effort; encode
  verification-honesty and deliberate-pause-before-irreversible-actions; leave
  tool mechanics, parallel-call guidance, and diff presentation to tool defs.

## Considered options

- **Runtime-loaded prompt file** — rejected: violates single-binary, adds IO,
  can float out of byte-sync with code and break the cache invariant.
- **More complete prompt** — rejected: marginal behavior gain not worth paying a
  larger fixed cache-prefix token cost every turn (Q5 grill round).
- **Skill/persona elevation into the system message** — rejected per §25: skill
  content is tool-result authority, never operator-level system authority.

## Consequences

- Evolving the prompt requires a build and a test gate, not an edit-and-reload.
  That is the point: stability is the invariant, so change is deliberately
  gated by the token-budget test and code review.
- Compaction rewrites the head with a summary anchored on the Objective (ADR-0003);
  the base prompt below it stays the stable core the summary re-attaches to.
