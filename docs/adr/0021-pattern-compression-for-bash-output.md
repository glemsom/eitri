# 0021 — Deterministic pattern compression for bash tool output

**Status:** Accepted
**Date:** 2025-07-28

## Context

Eitri's agent loop sends every bash tool result verbatim to the LLM. Tool outputs like `ls -la`, `find .`, or `grep -r` routinely produce hundreds of tokens — most of them repetitive (permissions, timestamps, repeated prefix paths). The existing Compactor solves oversize messages but at a cost: it calls the LLM itself to produce a summary, which is slow, expensive, and non-deterministic (same input → different output each turn), which busts provider prompt caches.

LeanCTX (an open-source context engineering layer) solves this with a library of command-specific pattern compressors — pure Go-style string functions that group, deduplicate, and summarize output deterministically, with a hard guarantee to never inflate tokens.

Eitri runs as the agent itself, not as a proxy in front of one. We can apply the same technique directly in the bash tool's output path: after the command runs, compress the raw output before it enters the conversation history.

## Decision

Add a new `internal/compress/` package containing deterministic pattern compressors for common CLI tools, and wire compression into the bash tool's `Call()` method.

### Compressors

Initial set of four compressors, each matching the command name prefix:

| Command | Compressor | What it strips |
|---------|-----------|----------------|
| `ls` | Group files and dirs, show size only. Drop permissions, owner, group, mtime. Append summary count. | permissions, owner, group, mtime, link count |
| `find`, `fd` | Group paths by directory. Cap at 10 per dir. Skip noisy dirs (`.git`, `node_modules`, `target/`, etc.). Append summary. | repeated directory prefixes, noise dirs |
| `grep`, `rg`, `ripgrep` | Group matches by file. Sort by match count (descending). Show first 5-10 per file. Append summary. | repeated file prefixes, low-cardinality single-match files |

Each compressor returns `None` when the output is too short or compression would inflate tokens (anti-inflation guard using chars/4 token estimate).

### Output flow

1. Bash runs command, captures stdout + stderr
2. Raw output capped at 8 KiB (up from 4 KiB, since compression packs more signal)
3. Router matches command name against known compressors
4. If matched and beneficial: compressed version replaces the original in `Blocks`
5. Raw original is preserved in a new `RawBlocks` field on `ToolResult` — stored in session snapshots for debugging, never sent to the LLM

### UI impact

The UI tool card now shows the compressed output — same text the LLM sees. The raw original is only accessible through crash dumps and session snapshots.

### Anti-inflation guarantee

Every compression pass is guarded by a token estimate (len/4). If the compressed version would use as many or more tokens than the original, the original is returned unchanged. This is verified by unit tests.

### Why not a wire proxy

LeanCTX implements this as a local proxy that intercepts HTTP requests between the agent and the LLM provider. Eitri is the agent itself — applying compression at the tool output boundary is simpler, avoids running a separate proxy process, and avoids the complexity of re-assembly and content-addressed recovery (CCR) that a stateless proxy needs.

## Consequences

### Positive

- 40-60% token reduction on `ls`/`find`/`grep`/`rg` tool outputs — the most common exploratory commands
- Zero LLM calls: all compression is pure Go string processing (sub-millisecond per call)
- Deterministic: same input → same output every turn → provider prompt cache stays valid
- Predictable: the LLM learns the compressed format and adapts its parsing strategy
- Backward compatible: commands without a matching compressor pass through unchanged

### Risks

- **Format drift:** If a future `ls` version changes output format, the parser may miss entries silently. Mitigated by the anti-inflation guard — if parsing fails, output passes through.
- **LLM confusion:** The model may initially expect `ls -la` output and get the compressed form. Mitigated by the fact that the compressed format is information-richer (grouped, counted, noiseless).
- **Snapshot debugging:** RawBlocks adds storage but the 1 GiB eviction cap on the Persister bounds the cost.

## References

- LeanCTX source: `rust/src/core/patterns/ls.rs`, `find.rs`, `grep.rs`, `mod.rs`
- LeanCTX website claim: https://leanctx.com/docs/concepts/proxy/
- Research notes: `docs/research/leanctx-request-compression.md`
