# git is a declared dependency

Status: accepted

`git` was a soft dependency (ADR-0001): opportunistic, never gated at boot. Session analysis of hand-authored unified diffs (agent bugfix session `1c61d909ed0c0b3ff29e0b1f55cca641`) found the model reliably gets diff *content* right but unreliably counts unified-diff hunk-header line spans once an edit spans more than one line, which `patch` then rejects outright (`malformed patch`). `git apply --recount` recomputes hunk spans from the hunk body instead of trusting the header, removing that failure mode — but the system prompt's own lean-tool-presence rule (`internal/engine/prompt_test.go`) forbids referencing a tool that isn't unconditionally guaranteed, so the prompt could not hedge "use `git apply` if present, else fall back to `patch`" without contradicting that rule.

We promote `git` from soft to declared: it now gates boot exactly like `rg`, `curl`, `lynx`, `patch`, and `python3` (`internal/app/deps.go`), so the prompt can name `git apply --recount` unconditionally as the edit-apply step. This collapses the three-tier model (declared / soft / base) down to two (declared / base) as the soft tier's only entry moves out — the browser launcher backing `open_in_browser` was already documented as a special case with zero boot involvement, not modeled through the soft-dependency check machinery, and needs no tier of its own.

## Consequences

- Every host running Eitri must have `git` installed; this raises the boot bar over ADR-0001's baseline.
- `checkSoftDependencies` and the `softDependency` type are removed as dead code — nothing else used that mechanism.
- `internal/engine/prompt.md` can now state `git apply --recount` as the single, unconditional edit-apply step.
