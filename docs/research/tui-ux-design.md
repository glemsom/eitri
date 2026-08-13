# TUI UX Design — Research & Brainstorm for Eitri

**Related:** spec §9 (TUI), §6 (thinking), §7 (compaction gauge); eitri.md §2.2/§2.3; Ticket #18.
**Console of record:** Ghostty (Kitty-compatible), Charm stack (Bubble Tea + Lip Gloss + Bubbles + Glamour).
**Status:** brainstorm / decision-input — not yet a locked decision. Layouts are proposals to be mocked further and pressure-tested.

---

## TL;DR — Design stance

Modern AI-agent TUIs (Claude Code, Copilot CLI, Devin-esque, pi/opencode family) have converged on a handful of patterns. The good news for Eitri: the spec already locks display infrastructure (primary-buffer differential rendering, Charm stack, Glamour markdown, collapsible thinking) and a rich data surface (tool stream, reasoning, compaction gauge, skills, model/settings). The TUI's job is to **surface that signal without becoming another wall of scrolling text**.

This doc reviews what the incumbents do well and badly, distills design principles, then proposes **three concrete layouts** mapped directly to Eitri's real features — each with an ASCII mock, its strengths, weaknesses, and the jobs it optimizes.

---

## 1. How the incumbents feel (and where they fail)

### Claude Code — the baseline everyone copies
- **What it is:** a linear conversation in the primary buffer. Streaming Markdown in the main pane; tool calls appear as inline colored entries; permission prompts inline; `/commands` in the same composer; a status strip at the bottom (model, cost, mode toggle).
- **What's good:** it respects the terminal. Native scroll/selection/search work because it never leaves the primary buffer. Everything is one flow — you read the story top to bottom.
- **What grates:** it's a **single channel**. Tool output, thinking, and the answer all fight for the same vertical space. Long tool dumps bury the reasoning. Nothing is collapsed by default. The status bar is underused (cost/mode only). You can't see "what did it touch" without scrolling to the grep lines scattered mid-conversation.

### Copilot CLI
- **What it is:** goal-oriented. Streams a plan/approach then does work with compact progress lines; `--agent` mode runs to completion; a `!` prefix jumps to raw shell.
- **What's good:** the plan-first rhythm keeps you oriented. Output is terse — tool progress is one line, not a dump.
- **What grates:** minimal. No thinking stream, no diff affordance, no persistent context panel. Fine for "do it," thin for "walk me through a long session."

### pi / opencode family
- **What it is:** small surface, model-agnostic. Compose + chat pane; thinking may appear inline.
- **What grates:** early-stage polish varies; often no dedicated status/telemetry panel.

### The shared failure mode
Every tool is a **single scrolling transcript**. That is the draw (terminal-native) and the ceiling (everything flattened into time). The differentiated opportunity for Eitri is **paned context**: keep the primary-buffer conversation as the anchor, but give reusable secondary views for the high-signal data the spec already produces:

1. **Reasoning stream** — collapsible per-turn (spec §6). Currently it either bloats the transcript or hides entirely.
2. **Cache hit-ratio gauge + `[compacted]` marker** (spec §7) — telemetry that belongs in a *status* surface, not the transcript.
3. **Agent skills** — detected vs. active (spec §9). Belongs in a *context* surface.
4. **Model / effort / turns / cost** (eitri.md §2.7) — settings + live indicators.
5. **Tool call history** — a dense "what did it touch" list (edited files, ran commands).

None of these warrant a permanent second column that chews screen width — but a **toggleable** flyout/sidebar that the transcript flows beside on wider windows is the sweet spot for a modern terminal.

---

## 2. Design principles (working set)

1. **Primary-buffer conversation is the floor.** Native scroll/search/selection always work. Secondary views are overlays/sidebars, never the default forever.
2. **One pane for time, one pane for state.** The transcript is *what happened over time*; a status/context region is *what is true right now*. Don't conflate them.
3. **Borrow the VS Code agent-window lexicons already being standardized** (file count deltas, added/deleted, per-file diffs, subagent progress) — Copilot/VS Code shipped this vocabulary; developers already read it.
4. **Thinking is a collapsible stream**, auto-collapsed after the turn (spec §6). Show a compact "🧠 reasoning 1.2k tokens" line that expands.
5. **Status is persistent, not inline.** Cost, cache-hit ratio, model, effort, turns-used, session temp path — in a bottom status strip or right rail, *not* as transcript entries.
6. **Clutter dies by default.** Tool dumps collapse to a single line (`+N more`); expand on demand. Never let a noisy `ls` scroll the thinking.
7. **Direct manipulation where Ghostty makes it free:** hover hyperlinks, clickable file paths, `open_in_browser` already gives a browser escape hatch. Make paths clickable/highlighted.

---

## 3. Layouts

Three layouts, each with a different center of gravity. All use ASCII mock, normal user width.

Shared element names (used in all mocks):
- **composer** = input line w/ `/` completion, model+status hint.
- **rail / status** = right-side or bottom persistent telemetry.
- **thinking** = collapsible per-turn reasoning block.

---

### Layout A — "The Ledger" (transcript + right status rail)

**Idea:** single primary-buffer conversation with a **toggleable right rail** (`ctrl+b`) carrying all the "true right now" state. Default state: rail *open* on wide screens (> ~140 cols), auto-hidden narrower. Rail never steals typing focus.

```text
┌────────────────────────────────────────────────────────────┬─────────────────────┐
│  ~/src/eitri  ·  main ·  opencode-go/deepseek-v4-flash     │ ▸ STATS              │
│                                                             │ ─────────────────────│
│  you ✎  Add a cache-hit gauge to the status strip          │ cache hit  97% ▬▬▬▬▬ │
│                                                             │ cost        $0.023    │
│  🤔 reasoning  ·  medium  · 1.4k tok  (click to expand)     │ turns      17/250    │
│                                                             │ tokens     41.2k in   │
│  assistant ───────────────────────────────────────────────  │              12.1k out│
│  Put the gauge next to the model in the bottom strip. I'll  │ ─────────────────────│
│  add a streamed `usage.prompt_cache_hit_tokens` readout     │ ▸ CONTEXT            │
│  wired to a small component.                                │ skills   2 active     │
│                                                             │   · agentskills/*     │
│  ⊕ bash  →  go test ./internal/gauge/                       │   · security-review   │
│      ✓ PASS  ok  0.31s   [3 lines]                          │ session  eitri-9f2c    │
│                                                             │ ─────────────────────│
│  ⊕ edit  →  internal/gauge/component.go  [+12, −3]          │ ▸ MODEL              │
│  ⊕ edit  →  internal/gauge/README.md     [+4, −0]           │ deepseek-v4-flash     │
│                                                             │ effort   medium ▾     │
│  [compacted]  ·  cache re-warm expected                      │ thinking on          │
│                                                             └─────────────────────┘
│  assistant ───────────────────────────────────────────────                       │
│  Done. Gauge is live; hit-ratio pops in the bottom strip.                        │
│  Diff: git diff gauge/component.go                                              │
│                                                                                  │
╞══════════════════════════════════════════════════════════════════════════════════╡
│ 💬 compose…  (/)commands      🧠on·med   hit97%  ↻23  ctrl+b rail  ctrl+s settings │
└──────────────────────────────────────────────────────────────────────────────────┘
```

**What it optimizes:** *orientation*. Everything "true now" (cost, cache, turns, skills, model) is glanceable without fighting the transcript. The transcript stays clean — tool calls are one line each, thinking collapses.

**Strengths**
- Rail matches spec's existing telemetry (cache gauge #7, skills #9, settings #2.7) 1:1.
- Secondary views never steal width when not needed.
- Strong on long sessions where "where are we / how much is this costing" matters most.

**Weaknesses**
- Rail eats width; must auto-collapse or you lose the benefit of the primary buffer (native selection across full width).
- Two mental models (time + state) — trivial for devs but a tiny ramp.

**Best for:** cost- and session-aware daily drive — the default.

---

### Layout B — "The Diff Driver" (work-centric / built around what changed)

**Idea:** mirror the Copilot/VS Code **Agents-window** lexicon. The center is still the conversation, but a **`ctrl+d` review panel** takes over to show a dense, code-review-style summary of everything the agent touched: files with add/delete counts, live inline diff, per-file status. Tuned for the "review the agent's work" job in user story #3 and the "copy a fix" flow in story #46.

```text
┌───────────────────────────────────────────────────────────────┐
│  ~/src/eitri · main · opencode-go/deepseek-v4-flash           │
│  ─────────────────────────────────────────────────────────────│
│  you ✎  refactor the auth middleware to use env-config        │
│                                                                 │
│  🤔 reasoning · low · 620 tok                                   │
│  assistant ──────────────────────────────────────────────────── │
│  On it. I'll extract the token from a config package and       │
│  route it through the existing validator.                      │
│  ⊕ edit auth/middleware.go  [+28, −19]                         │
│  ⊕ bash go test ./auth  ✓ PASS                                 │
│                                                                 │
│  ~ ctrl+d  Review changed files (4)  ~                         │
│  ┌────────────────────────────────────────────────────────────┐│
│  │ auth/middleware.go              +28 −19  ● modified        ││
│  │ auth/config.go                  +40 −0   ● added           ││
│  │ auth/middleware_test.go         +22 −2   ● modified        ││
│  │ internal/env/env.go             +6  −1   ● modified        ││
│  ├────────────────────────────────────────────────────────────┤│
│  │ @ middle         @@ import (                                ││
│  │   "os"                                                       ││
│  │  -"../secrets"   +"../env"                                   ││
│  │  -token := os.Getenv("TOKEN_SECRET")                         ││
│  │  +token := env.Token("secret")                              ││
│  │    ...                                                       ││
│  │  ^ open_in_browser → full diff in browser                    ││
│  └────────────────────────────────────────────────────────────┘│
╞═════════════════════════════════════════════════════════════════╡
│ 💬 compose…   ctrl+d review · ctrl+b rail · ctrl+s settings      │
└─────────────────────────────────────────────────────────────────┘
```

**What it optimizes:** *the moment right after the agent finishes.* This is where trust is earned — reviewing deltas fast, then copying the fix / opening the diff.

**Strengths**
- Bakes in the file-count/delta vocabulary devs already read from VS Code (per the July-2026 Copilot rework).
- `open_in_browser` slot (spec tool) is the escape hatch: terminal shows the summary, browser shows the full diff & lets you edit.
- Excellent fit for story #46 (non-destructive handoff into the editor).

**Weaknesses**
- Review panel needs an actual diff engine aboard (real work); terminal diff rendering is restricted width.
- Overkill for short "explain this" questions — must be on-demand, not persistent.

**Best for:** code-generation & refactor work, approval-gated sessions, and the "review before you commit" flow.

---

### Layout C — "The Mission Brief" (plan-first, peek-at-thought)

**Idea:** a **two-pane read**: left = plan/todo list, right = live transcript + expanding reasoning. Optimized for the multi-step or autonomous jobs where "what is the plan, and is it still going according to plan" is the dominant question (the parallel-agent / orchestrator vibe from the open-source tools, but single-agent). The plan is the anchor; the transcript is the evidence.

```text
┌───────────────────────────────────────────┬─────────────────────────────────────┐
│  ▸ PLAN (3/5 done)                        │  ~/src/eitri · main · opencode-…    │
│  [] 1. map config sources             ✓   │  ────────────────────────────────────│
│  [x] 2. extract env token loader      ✓   │  you ✎ migrate to env-config        │
│  [x] 3. rewire middleware             ✓   │                                      │
│  [ ] 4. port tests to new package         │  🤔 reasoning · high · 4.8k tok      │
│  [ ] 5. docs + example config             │   | think first then refactor —       │
│  ─────────────────────────────────────    │   | the cache key must stay stable…  │
│  skills: agentskills/go · review          │   ────────────────────────────────────│
│  cache 97% · cost $0.041 · 14t/250        │  assistant ──────────────────────────│
│  [compacted] at turn 11                   │  Now rewiring middleware to read    │
│                                           │  token from env package.            │
│  ctrl+1 plan  ·  ctrl+p peek-tool         │  ⊕ edit internal/env/env.go  [+6,−1]│
│                                           │  ⊕ bash go test ./...  ✓ PASS       │
└───────────────────────────────────────────┴─────────────────────────────────────┘
```

**What it optimizes:** *long autonomous runs where you glance rather than steer.* The plan keeps you grounded; the thinking expander gives the "is it thinking clearly?" peek without burying the transcript.

**Strengths**
- Natural home for `max_turns` progress and the compaction marker (#7) — they sit beside the plan, where "are we still on track / did we re-warm" is read naturally.
- Thinking gets room to breathe on the right without drowning the transcript.
- Mirrors the plan-first rhythm of Copilot CLI & Bernstein, so it's a known shape.

**Weaknesses**
- Plan requires the agent to *emit* a structured plan (a special-turn / structured-output build worth doing but not free).
- Two columns permanently cut help left pane; must collapse on narrow windows.
- Compaction can invalidate the "done" checkbox ordering — needs a small "re-planned at turn N" note.

**Best for:** unattended multi-step and semi-autonomous agent runs.

---

## 4. Recommendation

**Ship Layout A as the default, with B as the first advanced view.**

Rationale:
- A maps directly onto **already-specced** surfaces (cache gauge #7, skills #9, thinking #6, settings #2.7) — lowest lift, highest immediate signal. No new structured-output/planning dependency (B and C both need real diff/plan machinery that A does not).
- B is the high-value *second* investment because review-trust is the #1 engagement driver for an agent that edits files (stories #3/#11/#46) and it leans on the already-present `open_in_browser`.
- C is the most ambitious (needs agent-emitted plans) — treat as a future "plan mode" toggle, not v1.

**Toggle matrix (proposed defaults):**
| Key | View | Default state |
|-----|------|---------------|
| — (primary buffer) | transcript | always on |
| `ctrl+b` | Layout A rail (stats/context/skills/model) | open if ≥ ~140 cols, else closed |
| `ctrl+d` | Layout B review/diff panel | closed, on-demand |
| `ctrl+1` | Layout C plan pane | future, "plan mode" |
| `ctrl+s` | Settings panel | alt-screen modal (§9) |

---

## 5. Cross-cutting specifics mapped to spec

- **Primary buffer + home-row actions stay Charm's differential renderer** (spec §9): secondary panels are still Bubble Tea views; alt-screen only for modal Settings (§9 already splits this).
- **Thinking:** collapsible per-turn stream (spec §6), auto-collapsed; a `🧠 1.4k` token-count hint line; expands in place in the transcript (A) or in a right peek (C).
- **Cache gauge + `[compacted]`:** live `usage.prompt_cache_hit/miss` → ratio gauge (spec §7). Rendered in the bottom status strip (always visible) **and** the rail; `[compacted]` is a transcript status entry (never a blocker).
- **Skills:** rail "skills: N active" (spec §9); slash-command `/skillname` with `/` completion in composer; detected-but-inactive listed dimmed, active highlighted; hidden-if-disabled (spec's hide-not-block).
- **Model / effort / turns / cost:** bottom status strip + rail, editable via `ctrl+s` Settings (eitri.md §2.7); `run` leftovers like `turns 17/250` honor the max-turns pause prompt.
- **Ghostty niceties:** clickable paths, hover URL underlines, `open_in_browser` as the deep-dive escape hatch, and light/dark theme-following via Ghostty color palette so the TUI tones blend with the shell.
- **Empty-state:** on `eitri` with no prompt, show the current provider/model, a one-line "what can I do", `/` completion hint, and discovered skills — before the first turn.

---

## 6. Open questions to resolve before building

1. ~~**Diff engine for Layout B**~~ — **resolved by issue #90**: ship an in-TUI color diff (pure-Go, git-style hunks via `internal/diff`, no Node) and keep `open_in_browser` as the hand-off to the host browser/editor for diffs too rich for the terminal.
2. **Plan emission for Layout C** — is a structured plan worth a special-turn generation (spec §13 pattern) in v1, or is C strictly post-v1?
3. **Default on narrow terminals** — auto-hide rails below ~120–140 cols to preserve primary-buffer selection; confirm the exact collapse threshold.
4. **Cost telemetry source** — is $ read from `usage` tokens × model rate (static table) or are we showing only token counts, avoiding a rate lookup? (Preserve "parameterless TUI" feel.)
5. **`xhigh`/effort surfacing** — show raw effort tiers (spec: `xhigh`→`high` remap) or display normalized `low/med/high/max` only?

---

*This document reflects a research/brainstorm pass. It assumes the spec-described infra (Charm stack, primary-buffer rendering, Glamour markdown, collapsible thinking) and does not alter spec §9; final layout selection is a decision ticket/ADR against TUI work.*
