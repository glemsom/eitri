# ADR 0006 — TUI rendering: alternate-screen full-terminal viewport

- Status: accepted
- Date: 2026-08-14
- Supersedes: the earlier in-progress primary-buffer compensation plan (width-bucket scroll-cache, manual clip/anchor/band-height derivation)
- Related: issue #118 (Full-terminal viewport TUI alt-screen pivot), issue #119 (T1 — Alt-screen full-terminal viewport)

## Context

The interactive TUI (eitri.md §2.3) originally rendered into the primary (normal)
terminal buffer so native scrollback/selection/search would survive a session.
That forced a *compensation layer* to keep the layout correct in a buffer that is
not repainted wholesale: a per-width-bucket markdown scroll-cache, manual history
clip/anchor slicing, and hand-derived band/review/rail heights — all to avoid
re-rendering or leaving stale residue on resize.

In practice the primary-buffer approach made resize fragile (duplicated/scattered
text), and the compensation machinery grew complex solely to paper over the
buffer's incremental-render semantics.

## Decision

1. **The TUI renders through the alternate screen.** `runProgram` launches the
   Bubble Tea program with `tea.WithAltScreen()`, so Eitri takes over a clean
   full terminal surface and every render frame is a full repaint of the alt
   buffer. Entering the alt screen clears stale surface state, so resizing no
   longer duplicates or scatters text; the transcript re-flows to new widths
   stably.

2. **The transcript lives in a native `bubbletea/viewport`.** The persisted
   history viewport component owns the scroll position, clipping, and follow
   behaviour (re-anchoring to newest output via `GotoBottom`), instead of manual
   slice/anchor math.

3. **The primary-buffer compensation layer is deleted.** The width-bucket
   scroll-cache, the manual clip/anchor slice helpers, and their field state
   are removed along with the tests that asserted only those internals. Each
   render rebuilds the history and lets the native viewport clip it; the fixed
   bottom band (status strip, slash completion, composer) stays pinned below the
   viewport.

4. **Batch/headless mode is unchanged.** The alt-screen change is confined to
   the interactive `runProgram` path.

## Consequences

- Native terminal scrollback/selection/search no longer apply while the alt
  buffer is active; the transcript is read through the viewport.
- Resize is a clean repaint: entering the alt screen and every `WindowSizeMsg`
  repaint the full surface, so no stale primary-buffer residue remains.
- The TUI boot path (`internal/app/tui.go`) is the seam that flips render mode;
  tests exercise the model's `View`/`renderPane` directly rather than driving a
  real terminal.
