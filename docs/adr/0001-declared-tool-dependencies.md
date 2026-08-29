# Declared tool dependencies and a single authoritative agent prompt

Eitri "supports" the tools inside its `bash` tool loosely, telling the model to reach for whatever shell program fits. We replace that open-ended contract with a fixed, startup-checked dependency set that the single, never-adapted system prompt matches exactly: a tool Eitri relies on is either a declared dependency (checked at boot; fatal if missing) or it is not promised at all.

## Why

A coding agent's guidance is only as good as the tools behind it. Letting the prompt reference tools that may be absent forces either an adaptive prompt (guidance reshaped per install) or a model that wastes turns on `command not found`. Neither matches Eitri's goal of one precise system prompt.

## Decision

- **Hard substrate (fatal if missing):** `bwrap`, `bash`.
- **Declared dependencies (fatal if missing):** `rg`, `curl`, `lynx`, `patch`, `python3`. One startup check in `app.Run` (reusing the existing injectable `LookPath` seam and single exit path) reports every missing tool with a per-distro install hint; exit code stays 1.
- **Soft dependencies (documented, never gate startup):** `git` (one non-fatal boot notice when absent), `xdg-open` (backend of `open_in_browser`; fails at tool runtime only).
- **Base tools (assumed present, never checked):** `grep`, `sed`, `awk`, `cat`, `nl`, `diff`.
- **One system prompt:** a single fixed prompt in `internal/engine/prompt.md` that matches the declared set and is never adapted to what is installed. Tool Descriptions keep their mechanical contract (arguments, output shape, compress/truncation semantics) on the normal name+description function-calling mechanism; the prompt owns strategy only.
- The browser path is only `open_in_browser`; nothing instructs the model to reach for a browser inside `bash`.

## Considered options

- **warn-and-continue for declared deps** — rejected: makes the single truthful prompt impossible.
- **`git apply` in the edit path / `python3` or not** — settled as `patch` for edits and `python3` as a hard requirement (the right tool for complex text transforms where `sed`/`awk` struggle); both folded into the set above.
