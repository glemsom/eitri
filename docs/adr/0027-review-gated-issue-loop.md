# 0027 — Review-gated issue loop (build → test → review)

**Status**: Accepted (amended — the `code-build` stage runs only change-relevant tests; the full suite stays the `code-test` gate's job)
**Date**: 2026-08-07

## Context

The AFK issue-processing loop (`scripts/agent-loop.sh`) dispatches
`ready-for-agent` GitHub issues to `eitri -b` workers in detached worktrees.
Today the loop merges every worker-opened PR after a serialized, rebase-first
merge pass regardless of whether the implementation actually builds, passes
tests, or meets the issue's spec. A bot that writes plausible-but-wrong code
can therefore land regression after regression with no machine-checkable gate
between "wrote a PR" and "merged into `main`".

We want the loop to guarantee that nothing reaches `main` unless it (1) is
implemented as specified, (2) passes the project's test suite (or visibly
downgrades the absent-suite case), and (3) is adjudicated against the spec by
an independent reviewer — before its PR is ever merged.

The pipeline must stay **deterministic and auditable**: orchestration is pure
bash (no LLM orchestrator persona), each evaluation is a fresh-context batch
run, review state is handed via files in the worktree (not PR comments), a
bounded fix loop prevents endless ping-pong, and merge is conditional on bash
tracking the latest verdicts from both gates.

## Decision

Gate every agent-loop issue through a **build → test → review** pipeline before
its PR may be merged, driven by deterministic bash in `scripts/agent-loop.sh`,
with three example personas (`code-build`, `code-test`, `code-review`) and a
bounded fix loop. The following contracts are binding for every
review-gated-loop ticket (T2–T6 of issue #1186).

### Pipeline order

Each issue goes **build → test → review**. Nothing is ever sent to review that
has not passed test; a test-failing PR is bounced back to build (when rounds
remain) and is never merged. The sequence is a strict partial order: build may
produce a PR, test may accept or reject it, review may only ever see a
test-passing PR.

### Driving role

Orchestration is pure bash in `scripts/agent-loop.sh`; personas carry
expertise but there is **no orchestrator persona**. Each stage — build, test,
review — is a fresh batch run (a fresh `eitri --persona <NAME> -b "…"` call in
the issue's worktree), so every evaluation gets **objective, fresh-context
judgement**: no reuse of a long conversation that could carry the original
author's biases or stale assumptions into a new gate decision.

### Three personas

Ships as **example YAMLs under `docs/personas/`** — user-adaptable templates,
not auto-installed:

- **`code-build`** — the implementer. Creates a branch, implements the issue
  (via the `tdd` skill where possible), updates docs, builds the project and
  runs the tests relevant to its change (the TDD loop needs them), fixes
  issues, commits/pushes, and opens a PR whose description contains
  `Closes #N`. It deliberately does NOT run the project's full suite — the
  race suite and any browser E2E gate are `code-test`'s authoritative job, run
  once per round after the build. Must NOT merge or touch `main`. Fix-loop
  re-entry (addressing `.test.md`/`.review.md` findings) is the same persona
  invoked fresh.
- **`code-test`** — the verifier gate. Runs `make test` (or the project's test
  command), writes `.test.md` (currently-open findings only), passes on green
  OR on "no test suite found" provided the project builds, rejects on a
  failing build, and notes any no-test-suite downgrade in `.test.md`.
- **`code-review`** — the reviewer gate. Uses the `code-review` skill
  (Standards + Spec axes — see the skill and the Code review skill docs),
  verifies spec-fit + CHANGELOG + code quality, emits
  `VERDICT: APPROVED | CHANGES_REQUIRED | BLOCKED`, and writes `.review.md`
  (currently-open findings only).

`code-review` uses the `code-review` skill with its Standards + Spec axes run
via parallel sub-agents — this is valid because a batch run is a full parent
providing the context both axes need.

### Verdict contract

The review gate's batch run **must end its log with a mandated final
`VERDICT: APPROVED | CHANGES_REQUIRED | BLOCKED` line**, extracted from the
worker log by bash. **No verdict line, or a non-zero exit = a hard failure** for
that issue: comment and move on, never a blind retry. Auth/config/lock errors
will not self-heal, so retrying a hard-failure is wasted work and can mask a
real problem. (This is the single source of truth for the merge precondition,
below.)

### Handoff artifacts

`.review.md` and `.test.md` in the worktree hold **currently-open findings
only** — they are overwritten each gate pass, not appended to. The fix/build
agent's prompt always includes **both** current files plus the PR. Review state
is handed via these files, not PR comments: comments are a human nicety, not
the review contract.

### Fix loop

Both test and review can bounce back to build:

- Test **REJECT** → new build round.
- Review **CHANGES_REQUIRED** → new build round.
- Review **BLOCKED** → uses the fix cap up immediately (see below).

A round = one re-pipeline **build → test → review**. Cap = **3 shared rounds
total** across the whole fix loop, consumed by either reject path. Cap
exhausted → leave the PR open, comment "needs human", move on to the next
issue. Never endless ping-pong.

### Merge

Merge an issue's PR only when bash tracks the **latest** test verdict = **PASS**
and the **latest** review verdict = **APPROVED**. Merge stays bash
(`gh pr merge --squash`, serialized, rebase-first via the existing
`merge_pr`), reusing the existing serialized merge queue / rebase-conflict
resolution. There is no merge persona.

### No-test-suite fallback

The test gate passes on `make test` green **OR** "no test suite found" —
provided the project builds. A failing compile is REJECT regardless. The
downgrade (accepting no suite because the project builds) is **noted in
`.test.md`** so a later reviewer and any human sees that the test gate admitted
the PR without an actual test suite.

## Considered options (rejected)

- **Keep the current single-pass, un-gated loop** — no machine-checkable
  quality or spec gate; bots could merge wrong code indefinitely. Rejected.
- **An orchestrator persona that calls gates itself** — reintroduces an LLM as
  the arbitration point, is harder to audit/deterministically reproduce, and
  abandons the "pure bash drives the pipeline" invariant. Rejected.
- **Have one long-lived reviewer conversation review each stage** — a shared
  context carries the author's reasoning forward and biases the reviewer;
  a fresh batch run per stage is deliberately objective. Rejected.
- **Merge when tests pass, skip review** — review is the spec-fit adjudication
  that catches "tests green but wrong feature"; dropping it would let
  plausible-but-misspecified code merge. Rejected.
- **Hand review state via PR comments only** — comments are human-oriented and
  unstructured; the fix agent needs a deterministic, versioned file contract.
  Rejected in favour of `.test.md`/`.review.md` (comments remain a nicety).

## Consequences

Positive:

- Nothing reaches `main` without build success, a passing (or visibly
  downgraded) test gate, and an independent review approval.
- The whole pipeline is auditable: deterministic bash, fresh-context runs,
  and verdict lines in per-stage logs make every gate decision reproducible
  from the worktree log alone.
- A bounded 3-round fix loop caps cost and prevents infinite bot ping-pong;
  `BLOCKED` and cap exhaustion route to a human explicitly.

Negative:

- Three batch LLM runs per round (plus re-entries) is markedly more compute
  than the current single pass—the price of an objective, gated pipeline.
- `.test.md`/`.review.md` must be kept in sync with each worktree's branch; a
  stale pair could drive a fix round against outdated findings (mitigation: the
  files are overwritten per gate pass and the worker prompt always appends the
  current PR).
- The 3-round cap is a heuristic; a genuinely hard issue can exhaust it and
  require a human to reopen the loop.

## Verification

Before this pipeline is relied on, the bwrap-sandboxed batch runner must be
verified to run `go test`/`make test` **in-tree** in a detached worktree — the
`bash` tool runs under the bwrap sandbox (ADR-0017), and the test gate
depends on the sandbox allowing in-tree build/test commands to execute. This
verification gates acceptance of the whole approach.

## References

- [0017 — bwrap sandbox for bash tool](0017-bwrap-sandbox.md) — the sandbox the
  test/build steps must run inside
- [0024 — Unified parent-run preparation](0024-unified-parent-run-preparation.md)
- [0025 — Unified run engine across sub-agents and batch](0025-unified-run-engine-across-subagent-and-batch.md)
- [0018 — Personas](0018-personas.md) — persona + `required_skills` contract the
  example personas declare against
- The `code-review` skill (Standards + Spec axes via parallel sub-agents)
