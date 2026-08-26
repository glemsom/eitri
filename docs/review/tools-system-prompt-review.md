# Review — Tool Surface & System Prompt Adequacy for an LLM Agent

Scope: are the tools Eitri offers and its system prompt (`internal/engine/prompt.md`)
instructive enough for an LLM to use Eitri *efficiently*?

Status: **active.** This review was originally written against a pre-#575 prompt and
has been updated to reflect current HEAD. The top findings from the original pass
(impossible `reasoning_effort` instruction, missing edit workflow, missing tool-choice
guidance, missing truncation contract, over-prescriptive ripgrep boilerplate) were all
resolved by `#575` "engine: fix system prompt claims, add edit/tool workflow" and its
follow-ups, and are now locked in by the `TestSystemPrompt*` suite in `prompt_test.go`.
What remains below is the set of findings that survived that round, verified against
source at HEAD.

Method: read `prompt.md`, `prompt.go`, `registry.go`, all tool definitions
(`tool_bash.go`, `tool_browser.go`, `tool_webfetch.go`), `sandbox.go`, `network.go`,
`translate.go`, `skills.go`, plus the compress and generation-control paths the prompt
and tools reference.

Verdict: **functional and close to efficient.** The 3-tool surface is small but
everything it touches is precisely described, the system prompt now teaches the
locate→read→edit→verify workflow, tool-selection policy, the output-cap contract, and
recovery for the common failures. Two gaps remain that cost an agent real turns.

---

## 1. Tool surface

| Tool | Description | Model-facing schema | Sandbox |
|------|-------------|--------------------|---------|
| `bash` | run `/bin/bash -c` in bwrap; combined stdout+stderr | `command` (string, required) | bwrap cage |
| `web_fetch` | HTTP GET, returns Markdown | `url` (http/https, required) | host-side network path |
| `open_in_browser` | open URL or `file://` in host browser | `path` (required) | host-side |

The per-tool Description strings are the strongest part of this surface. They are
accurate, honest about limits, and unusually good at training the model on the runtime
(this finding is unchanged from the original pass and remains true):

- `bash` description explains the bwrap cage (writable workspace, session-temp `/tmp`,
  read-only root, fresh procfs/dev), combined output stream, and teaches the compressor
  contract: ANSI stripped, consecutive dup lines collapsed, truncation at line budget
  with explicit `+N more`, never silent, deterministic ("same command same form"),
  "re-running the command is the recovery path if you need the tail."
- `web_fetch` description (and its runtime check) correctly states it runs on its own
  network-unrestricted path, *not* through bash.
- `open_in_browser` description surfaces the `file:///tmp/...` → host-path translation.

Schemas are strict: `additionalProperties:false`, explicit `required`, no ambiguous
optional params.

### Remaining gap: no dedicated read/search/edit tools

Everything is still `bash` + `sed`/`nl`/`rg`/heredoc. This is a legitimate design
(Pi-style), and the prompt now fully carries the load (§2). No action recommended;
revisit only if the prompt ever regresses. The model discovers the tree by running
`find`/`rg`/`ls` itself; the prompt's new skill-discovery hint is the only place
"survey before acting" is stated.

---

## 2. System prompt

`internal/engine/prompt.md`, ~830 estimated tokens by the `chars/4` heuristic used in
`estimateString` (`compact.go`), under the `MaxSystemPromptTokens = 1000` ceiling
(`prompt.go`). Personality-light and deliberately brief — the good trade. The
locate→read→edit→verify procedure, tool-choice policy, cap contract, and recovery
hints it now carries are the mechanical backbone an efficient agent needs.

### 2.1 The two surviving gaps

**Gap A — skill discovery requires guessing.** The prompt tells the model to
`cat ~/.agents/skills/<name>/SKILL.md` (or project `.agents/skills/...`) when a task
matches a skill's scope, and now says to list the roots first (`ls ~/.agents/skills/`,
`.agents/skills/`) to find what's installed. This is a real improvement over the
original (which named no way to enumerate skills at all). It is closed as of this
revision.

**Gap B — `web_fetch` error recovery.** `network.go` errors on any non-2xx and on the
30s timeout; `htmlToMarkdown` can error on a hard-to-parse page. The prompt previously
told the model to prefer `web_fetch` over `curl` but never said what to do when
`web_fetch` itself fails. Now resolved: the Tools section directs a fall back to
`curl` in `bash`, and the `bash` line tells the model to report a sandbox/bwrap failure
(missing `bwrap`, permissions) instead of blindly re-running. Both closed as of this
revision.

### 2.2 Resolved by `#575` (kept from the original report as context)

- Impossible `reasoning_effort` instruction — **deleted**; the model has no mechanism
  to set it (it is read from user config), and a test now asserts it must not appear.
- File operations became an explicit locate(`rg -n`) → read(`nl -ba | sed -n`) →
  edit(`sed -i`/quoted heredoc) → verify(re-read) decision procedure, with the quoted
  heredoc's *why* (no expansion/globbing) stated inline.
- Tool-selection guidance added for `bash` vs `web_fetch` vs `open_in_browser`,
  including the "prefer web_fetch for Markdown; use curl for raw bytes/headers"
  tradeoff and the render-to-`/tmp` → `file:///tmp/...` open pattern for
  `open_in_browser`.
- Ripgrep advice reframed from mandatory flag boilerplate to intent, with `-l`/
  `--files-with-matches` named as the survey idiom.
- Truncation/cap contract surfaced in the prompt (not just the `bash` tool
  description): `+N more` and `+N bytes truncated` mean partial output — narrow and
  re-run. This closes the original §3 concern that the agent might reason over a
  truncated tree.

### 2.3 Minor, remaining

- **`TestSystemPromptIsStatic` bug fixed in-line.** The test used
  `strings.ContainsAny(p, "$(")` to ban command substitution, which matches any `(`
  in normal prose — a false positive waiting to fire. Corrected to
  `strings.Contains(p, "$(")` (literal), which is the real property: the prompt must
  not interpolate session state. No net prompt behavior changed.

---

## 3. Interaction of prompt + compressor + byte-cap

The compression contract (`maxLines = 200`, `DefaultByteCap` at the tool-result
boundary, `compress.go`) is now stated in the prompt's Working principles ("Tool
output is line- and byte-capped … narrow the query and re-run"), closing the original
risk of reasoning over a truncated core. This is the highest-value reliability fix from
the original pass and is now locked by a test (`TestSystemPromptStatesOutputCapContract`).

---

## 4. Bottom line

The tools are clean and their descriptions carry an unusual amount of runtime
training. After `#575` the system prompt carries the whole locate→read→edit→verify
workflow, the cap contract, tool-choice policy, and recovery for web_fetch and sandbox
failures. What was a "functional but thin" prompt is now, at current HEAD, adequate
for efficient use. The two surviving gaps from the original pass — skill enumeration
and web_fetch/sandbox error recovery — are closed as of this revision. No further
action recommended beyond guarding these against prompt regressions with tests, which
the existing `TestSystemPrompt*` suite already does.

Verified against the code: every cited behavior (tools, schemas, compressor, byte cap,
config-threaded effort removal, `network.go` error paths, `skills.go` discovery) was
read in source at HEAD.