# Eitri — Domain Glossary

Canonical terms for planned Eitri (single-binary AI coding agent for Linux).

- **Workspace** — the user's working directory in which Eitri reads, writes, and executes; mounted read-write at its host path inside the sandbox.
- **Path namespace** — the view of the filesystem a component operates in. Host paths are canonical; the only remapped region is the session temp (`/tmp` ↔ host `/tmp/eitri-<GUID>`). See ADR-0002.
- **Session temp** — per-run ephemeral `/tmp` shared by `bash` and host-side tools; removed at run end.
- **Sandbox / bwrap cage** — the bubblewrap isolation boundary for shell commands; root read-only, workspace + session temp writable, separate PID namespace.
- **Host-side tool** — a tool running outside the bwrap cage but resolving the same path namespace as `bash` (e.g. `open_in_browser`, `write`, `edit`).
- **Sampling Policy** — the temperature- or nucleus (top-p)-based sampling a **special turn** may request on a constrained generation. A special turn always selects a single mode, so a provider request never carries both `temperature` and `top_p`; ordinary agent/tool turns stay on provider defaults. See spec §13 / issue #61.
- **Eitri's working principles** — the canonical, model-facing operating values that define how Eitri works: **concise** (full technical substance, no filler or hedging), **simplest correct solution** (no speculative abstractions; a deliberately-used structure is not overengineering), and **preserve existing style** (surgical, style-consistent edits over rewrites). They live in the byte-stable system prompt (see spec §34) but are defined here so prompt, docs, and tests share one meaning. The smith persona is presentation wording, not a domain term.
