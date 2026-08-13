# TUI Resize & Full-Screen UX — Research for Eitri

**Related:** spec §9 (TUI), eitri.md §2.2; `internal/tui/model.go`, `rail.go`, `internal/app/tui.go`.
**Console of record:** Ghostty (Kitty-compatible), Charm stack (Bubble Tea + Lip Gloss + Bubbles + Glamour).
**Status:** research / decision-input — not a locked decision.

---

## TL;DR

The user's question has a name: the "take over the terminal, no scroll" full-screen mode that other AI-agent TUIs use is the **alternate screen buffer** (alt-screen mode, alt buffer). Claude Code, Copilot CLI, and most TUI agents switch into it so the app owns the *entire* viewport and draws its own scroll.

Eitri deliberately chose the **opposite**: it renders in the **primary (normal) buffer**, keeping native terminal scrollback, selection, and search — this is a documented spec decision (`internal/app/tui.go` `runTUI` comment, `docs/research/tui-ux-design.md` §1/5). That is a defensible design line, but it is the *reason* resize "feels bad": in the primary buffer the transcript is a plain growing string, so on window shrink/resize there is **no viewport, no height handling, and no pinned composer** — the content just scrolls and re-wraps with nothing anchoring the layout.

This doc answers: (1) what the full-screen concept is, (2) where Eitri's current resize handling is actually weak, and (3) concrete options from *in-buffer* (keep the primary-buffer commitment) to *alt-screen* (match incumbents), with a recommendation.

---

## 1. The terminology the user asked about

- **Alternate screen buffer (alt-screen / alt buffer):** a separate terminal "page" programs switch to (escape sequence `\e[?1049h`) to take complete control of the viewport. It **hides the terminal's scrollback** while active, and on exit (`\e[?1049l`) restores the original screen. This is what "full screen / take over / no scroll" means.
- **Primary (normal) buffer:** the default terminal page with live scrollback. Apps that render here leave scrollback/selection/search intact but must redraw carefully and self-manage any "scroll".
- **Charm/Bubble Tea terminology:** `tea.WithAltScreen()` opts a program into the alt buffer (toggled at runtime via `tea.EnterAltScreen` / `tea.ExitAltScreen` commands). `tea.WithMouseCellMotion()` enables mouse tracking. Eitri currently uses *neither* — plain `tea.NewProgram(m)`.

Why TUIs like Claude Code use alt-screen: it gives a stable, full-width, self-drawn viewport where the app controls every line; the app can draw its own scrolling history/paging. The cost: you lose native terminal scroll, drag-selection across output, and `less`-style terminal search unless the app re-implements them (most agents ship an internal scroll now).

---

## 2. What Eitri does today — accurate inventory

**Render mode:** primary buffer everywhere. `runProgram` is `tea.NewProgram(m)` — no alt-screen, no mouse, no height/viewport. The design doc (§5) says "alt-screen only for modal Settings," but today even Settings and the Review panel render inline in the primary buffer.

**Resize handling today:**
| Callback | What happens |
|----------|--------------|
| `tea.WindowSizeMsg` | `m.width = msgi.Width; m.syncWidths()` (`model.go`) |
| `syncWidths()` | `composer.SetWidth(transcriptWidth())` — textarea re-wraps |
| `transcriptWidth()` | terminal width − 2 gutter; − (railWidth+1) while the rail ("#" issue #88) shows; floor of 20 cols |
| `railVisible()` | auto-shows rail ≥120 cols **and ≥24 rows**; auto-hides narrower or shorter; `ctrl+b` overrides on any size |
| Terminal **height** | **not captured or used at all** |
| Viewport / paging / internal scroll | **none** — the transcript is a `strings.Builder` dumped via `View()` |
| Composer anchoring | not pinned; it just trails the rendered transcript string |

**Consequence (the actual bug-ish behavior):**
1. **Shrinking the window** makes the transcript re-wrap (width) but leaves **no height cap** — the view is a long string that just scrolls in the primary buffer, with the composer pushed off-screen. There is no way to keep the composer visible or page through history.
2. **Width shrink** triggers markdown re-`RenderMarkdown(msg.content, width)` every frame over the *whole* message log — O(history) re-render per resize keypress. On long sessions this stutters on every resize event.
3. **No follow/scroll control** — you cannot "scroll to bottom" or view older output beyond native terminal scroll.
4. Settings/review/thinking surfaces are stacked as conditional `if` blocks in `View()` — they are not height-aware either, so on a short window a tall review diff simply overflows.

---

## 3. The design tension (worth naming explicitly)

Eitri made a **spec-locked bet on the primary buffer** (native scrollback/selection/search is a stated selling point — see `tui-ux-design.md` §1 "respects the terminal"). The options below sit on a spectrum:

```
preserve scrollback ──────────────┬────────────────────────── take over viewport
  primary buffer, no viewport     │        primary buffer + internal viewport
  (today)                         │        (keep spec, fix resize UX)      alt-screen (+ mouse)
```

The clean middle path — **stay in the primary buffer but add an internal viewport with height awareness + follow-mode** — is what the bubbletea `viewport` component gives you, and it directly fixes the resize complaint *without* breaking the spec's preserve-native-scrollback commitment. This is the recommendation.

---

## 4. Options

### Option A — In-buffer viewport: keep primary buffer, add height-aware scrolling (RECOMMENDED)

Adopt the Charm `bubbles/viewport` component for the transcript, exactly as the spec's own infra already anticipates ("Bubble Tea differential renderer" / "primary buffer"). `viewport` is a real component, not a string dump:

- **Height-aware:** it knows the terminal height (`WindowSizeMsg` gives you `Height` — currently ignored), so on shrink the visible pane clips to the window and the composer stays pinned at the bottom.
- **Internal scrolling:** `viewport.Model` gives up/down/pgup/pgdn, `GotoBottom()` follow-mode (auto-scroll to newest while a stream is live; stop when the user scrolls up), and `AtBottom()` to decide when to auto-follow.
- **Native scrollback preservation:** still primary buffer; the user keeps terminal drag-selection/search over the *latest painted frame* while the *internal* viewport handles agent-history paging.
- **Minimal re-render:** you replace the per-message `RenderMarkdown` loop with `viewport.SetContent()` on update, and only re-render when the *content* changes (not on every width tick), fixing the O(history)-per-resize stutter.
- **Costs:** transcript and composer become separate stacked regions (Glamour renders into viewport content, composer below); you must manage content-height + viewport-height coordinates; nested scrollbars/style pass-through need a little care. Modest, well-trodden Bubble Tea territory.

### Option B — Alt-screen full screen (matches Claude Code/Copilot CLI)

Toggle `tea.EnterAltScreen` on boot (`tea.WithAltScreen()`), add `tea.WithMouseCellMotion()`, and move the whole chat into a `viewport` that owns its scroll. This is the "full screen takeover" the user described seeing elsewhere.

- **Pros:** matches incumbent UX; full-width stable pane; easy to add clickable links/scrollbar/copy-on-select; mouse wheel scrolling comes free with the viewport+mouse options.
- **Cons:** **breaks the Eitri spec commitment** — native terminal scrollback/selection/search are gone, and you must reimplement scroll. Directly conflicts with `tui-ux-design.md` §1/5 and the `runTUI` doc comment. High conceptual cost relative to what the user actually asked (they asked to *fix resize*, not necessarily to go full-screen).

### Option C — Hybrid: primary buffer for chat, alt-screen only toggled `ctrl+f`

Keep the default exactly as today (Option-zero + Option A viewport for the transcript), but add an opt-in `ctrl+f` "focus mode" that enters alt-screen + mouse for a clean immersive session, and `ctrl+f` again (or `ctrl+c`) to drop back to the primary buffer. Settings/Review could then also be true alt-screen modals as the design doc originally implied.

- **Pros:** keeps the spec's default intact, gives power users the incumbent experience on demand, and lets you A/B which feels better before committing one default.
- **Cons:** two render paths to maintain; the viewport/scroll work from Option A is still required as the shared core.

### Option D — Minimal reactive patch (do the narrow thing)

Don't add a viewport. Only:
- capture `Height`, clamp `View()` to it so the composer never trails off-screen,
- gate `RenderMarkdown` re-wrap on a width *change* (skip identical resizes / debounce),
- pin composer to visible bottom via lipgloss placement.

Cheapest, fixes the worst "composer vanishes / jumpy" symptoms, but gives **no scroll control** — you still can't page agent history, and follow-mode is untouched. A stopgap, not a fix.

---

## 5. Recommendation

**Ship Option A (in-buffer `viewport` for the transcript) as the core fix; keep alt-screen in reserve (Option C) behind a `ctrl+f` if the team wants to match incumbents on demand.**

Rationale against the user's actual ask ("improve UX, focus on resize correctly"):
- Their instinct toward "full screen" is really about a *stable, self-owned scrolling viewport*, not specifically about hiding scrollback. Option A delivers the stability + scroll they want while holding Eitri's differentiating primary-buffer commitment.
- It uses infrastructure the project already endorses (Bubble Tea components), honors spec §9 and the `runTUI` doc comment, and fixes the concrete defects: **no height handling, no paging/follow, O(history) re-render on every `WindowSizeMsg`**.
- If the team decides the incumbents' alt-screen is genuinely better, Option C lets you ship that as a *toggle* without re-litigating the whole default — and you keep all the Option A viewport work for it.

**Concrete implementation checklist (for the eventual ticket / ADR):**
1. Store `m.height` from `WindowSizeMsg` (currently discarded).
2. Replace the message-render loop in `renderPane()` with a `viewport.Model`; set its height from terminal height minus composer + status + header.
3. `viewport.SetContent()` on (a) new/streaming message, (b) tool entry, (c) resize — but **debounce / guard** re-Markdown so a fast drag-resize doesn't re-render the entire history each frame (`RenderMarkdown` is the expensive bit; render to content width only when width actually changed).
4. Follow mode: `viewport.GotoBottom()` after appends unless the user has scrolled up; detect `AtBottom()`.
5. Pin composer + status strip below the viewport (they already render as siblings; make them a fixed bottom band).
6. Reduce `RenderMarkdown` cost: cache per-message rendered output keyed by (width) instead of re-rendering all messages every frame.

**Open questions for the decision ticket:**
- Scroll UX on primary buffer: does internal `UP/DOWN` (not `less`) conflict with terminal-native scroll? — need a manual pass in Ghostty; likely keep `pgup/pgdn` + mouse-wheel only.
- Is `ctrl+f` alt-screen focus (Option C) worth building behind a flag in the same change, or strictly post-v1?
- Does the rail (issue #88) height-cap too, or scroll independently? — **resolved** by ADR-0006 decision 5 (issue #109 / T05): the rail height-caps to the same visible height as the history region.

---

*This document reflects a research pass; it does not alter spec §9. It inventories current behavior (verified against `internal/tui/model.go`, `rail.go`, `internal/app/tui.go`) and proposes options. Final selection is a decision ticket/ADR against TUI resize work.*
