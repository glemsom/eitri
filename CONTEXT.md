# Eitri — Domain Glossary

Canonical terms for planned Eitri (single-binary AI coding agent for Linux).

- **Workspace** — the user's working directory in which Eitri reads, writes, and executes; mounted read-write at its host path inside the sandbox.
- **Path namespace** — the view of the filesystem a component operates in. Host paths are canonical; the only remapped region is the session temp (`/tmp` ↔ host `/tmp/eitri-<GUID>`). See ADR-0002.
- **Session temp** — per-run ephemeral `/tmp` shared by `bash` and host-side tools; removed at run end.
- **Sandbox / bwrap cage** — the bubblewrap isolation boundary for shell commands; root read-only, workspace + session temp writable, separate PID namespace.
- **Host-side tool** — a tool running outside the bwrap cage but resolving the same path namespace as `bash` (e.g. `open_in_browser`, `write`, `edit`).
- **Sampling Policy** — the temperature- or nucleus (top-p)-based sampling a **special turn** may request on a constrained generation. A special turn always selects a single mode, so a provider request never carries both `temperature` and `top_p`; ordinary agent/tool turns stay on provider defaults. See spec §13 / issue #61.
