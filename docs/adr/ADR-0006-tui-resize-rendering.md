# ADR 0006 — TUI resize handling: primary-buffer viewport

- Status: accepted
- Date: 2026-08-14
- Related: docs/research/tui-resize-ux.md; ticket TUI responsive-resize (to file)

## Context

Eitri's interactive TUI (spec §9) renders into the primary (normal) terminal
buffer so native scrollback, selection, and search survive a session. That
choice is asserted today only in a `runTUI` doc comment and research docs — it
is not recorded as a first-class decision. Resize is also handled poorly in
practice:

- `tea.WindowSizeMsg` stores only `Width`; the terminal `Height` is discarded.
- The transcript is a single linear `strings.Builder` string (`renderPane`),
  so it is not height-aware: on a window shrink the composer and status strip
  trail off-screen and the layout just scrolls with nothing anchored.
- Every `WindowSizeMsg` re-runs `RenderMarkdown` over the *entire* message
  history — O(history) work per resize tick, so a drag-resize stutters.

This ADR records the render-mode decision and the resize/marker architecture
that fixes it, in the primary buffer without adopting an alternate-screen full
screen mode.

## Decision

1. **Primary buffer is the ratified render mode.** The TUI renders into the
   primary buffer — never the alternate screen — preserving native terminal
   scrollback, selection, and search. This turns the existing `runTUI` comment
   into a recorded decision.

2. **The composer, status strip, and slash-completion sit in a fixed bottom
   band.** They are pinned and never scroll away. The agent history (messages,
   thinking blocks, tool entries, skills panel) is the only scrollable region.

3. **The history region is a Height-aware `bubbletea/viewport`.** `Height` is
   captured from `WindowSizeMsg` alongside `Width`; the history clamps to the
   terminal so the composer band never trails off-screen.

4. **Render cost is bounded per resize.** Markdown is rendered once per message
   and cached (keyed by width); the viewport content is rebuilt only when a
   message actually changed or its width-bucket changed, and rapid resize
   events are coalesced. Resizing never re-renders the full history.

5. **The right context rail (issue #88) honours the same visible height** as
   the history region so the two panes form one coherent row.

6. **No re-implemented navigation in this change.** Native terminal scroll
   remains the navigation path; `GotoBottom`/follow behaviour keeps the newest
   output in view while live, but paging/scroll keys/mouse tracking are out of
   scope. This is the current decision, not a permanent fence: a future
   alternate-screen "focus mode" is free to supersede this ADR.

## Consequences

- The full transcript is no longer guaranteed to be *visible* at once; native
  scroll still reads it, but only over the latest painted frame.
- `renderPane`/`View` move from one linear string to explicit ordered regions
  (scroll region + fixed band + overlay regions) — a structural refactor, done
  first so behaviour is preserved. The region seam is implemented (issue #105):
  `renderHistory` (scroll region), `renderBand` (fixed band), and the
  review/settings/prompt overlays render independently and are composed in
  order by `renderPane`/`View`, with byte-identical output to the former single
  string.
- Settings, the continuation prompt, the review panel, and slash-completion get
  dedicated overlay regions: review its own height-clipped region, settings and
  the prompt a fixed region, slash-completion pinned above the composer.
- Decisions 2 & 3 are implemented (issue #106 / T02): the composer, status
  strip, and slash-completion sit in a fixed bottom band; the history region is
  Height-aware — `Height` is captured from `WindowSizeMsg` and the history
  clamps to the terminal, so only the history is scrollable and a drag-resize
  keeps the band pinned and the composer on screen. Mouse/wheel routing is
  deliberately off (decision 6: native scroll is the navigation path, and the
  T04 follow seam drives live-stream auto-follow). T02's clamp is a pure
  height slice.
- Decision 4 is implemented (issue #107 / T03): the scroll region's content is
  cached per width-bucket (`widthBucketCols`), and rebuilt only when a message
  actually changed (`histVer`) or the transcript crossed a width-bucket, so a
  drag-resize that stays within a bucket reuses the prior markdown and rapid
  resize bursts coalesce to a single re-render instead of re-running the whole
  `RenderMarkdown` pass every tick. The persisted `bubbletea/viewport` scroll
  component's position + follow behaviour lands with T04.
- Decision 5 is implemented (issue #109 / T05): the right context rail honours
- Decision 6's overlay-region work is implemented (issue #110 / T06): the
  review panel renders in its own height-clipped region — `renderPane` clips the
  overlay to at most `reviewRegionMax` rows (and never more than the terminal
  leaves after the fixed band), so a tall expanded diff clips instead of pushing
  the composer off-screen; `clipReviewRegion` keeps the header + file list and
  drops the diff tail, and the history viewport (now taking `reserved` rows =
  band + review) keeps the band bottom-pinned. Settings and the continuation
  prompt remain fixed full-surface regions that return first from `View()` and
  capture keys (`updateSettings`/`updatePrompt`), so they stay focused;
  slash-completion stays pinned above the composer in the fixed band. Keyboard
  routing to each overlay is unchanged.
  the same visible height as the history region — `styledRail` height-clamps the
  rendered rail to the rows the fixed bottom band does not occupy
  (`railClampHeight`), so the two panes form one coherent row instead of the
  rail overflowing while the history clips. The rail's auto-show/hide also
  responds to terminal height as well as width (`railShowHeight` gates
  `railVisible` alongside `railShowWidth`); `ctrl+b` still overrides on any size.
- Resize feels correct rather than merely non-broken: the composer stays put
  and a drag-resize does not re-render the whole history.
- A future ADR may obsolete this one if Eitri adopts an alternate-screen focus
  mode; that would edit spec §9's primary-buffer line.
