# Eitri — Domain Glossary

Canonical terms for planned Eitri (single-binary AI coding agent for Linux).

- **Workspace** — the user's working directory in which Eitri reads, writes, and executes; mounted read-write at its host path inside the sandbox.
- **Path namespace** — the view of the filesystem a component operates in. Host paths are canonical; the only remapped region is the session temp (`/tmp` ↔ host `/tmp/eitri-<GUID>`). See ADR-0002.
- **Session temp** — per-run ephemeral `/tmp` shared by `bash` and host-side tools; removed at run end.
- **Sandbox / bwrap cage** — the bubblewrap isolation boundary for shell commands; root read-only, workspace + session temp writable, separate PID namespace, fresh pid-scoped `/proc`, private devtmpfs `/dev` with writable `/dev/shm`.
- **Host-side tool** — a tool running outside the bwrap cage but resolving the same path namespace as `bash` (e.g. `open_in_browser`, `write`, `edit`).
- **Slash-skill activation** — a human-invoked skill trigger from the TUI via `/skillname [<args>]`, distinct from the model's automated `skill` tool call. The activation run produces a structured payload (`<skill_content>` plus optional `<skill_resources>`). With args, the payload is **skill-injected** into the model's context for the follow-up args turn so the model acts on the args with the skill instructions loaded; a bare `/skillname` injects only the note and queues no turn.
- **Drag selection** (T6, issues #124/#261) — `internal/tui` click-drag over the history viewport marks a cell range in reverse video and copies the marked plain text. Selection coordinates are **rune-indexed and width-aware**: a mouse cell arrives in screen display-width space, `colToRuneIndex` converts it to a rune index into the plain line, and highlight (`highlightRange`) and copy (`copySelection`) both operate in that same rune space — never in display-width or byte space — so wide/multibyte characters (CJK, emoji, unicode arrows, smart quotes) are selected, highlighted, copied, and pasted exactly, with no boundary split or under-coverage (#261).
