# ultraviolet: `scrollOptimize` corrupts the alternate screen on scroll + content change

Draft upstream report. Written after we **removed** our local fork of
`github.com/charmbracelet/ultraviolet` (see "History" at the bottom). This file is
the record of the change we had been carrying, so it can be filed upstream later.

## TL;DR

`TerminalRenderer.scrollOptimize` (the alternate-screen hard-scroll optimization,
active when both `tScrollOptim` and `tFullscreen` are set) can leave a stale row
on screen when a live region scrolls while a line's content changes in the same
frame. The row that scrolls into view is moved by the terminal but not repainted,
so an earlier frame's text stays visible *and* the new text is painted elsewhere —
the same content appears in two screen positions.

Eitri hits this on every streaming chain-of-thought frame: the reasoning block
reflows/scrolls on each token delta while its token-count header keeps changing.

## Upstream status (already reported)

This is **not** an unreported bug — it is filed and has a fix in review:

- **Issue #137** — "Hard-scroll optimization leaves stale rows when the scrolled
  region contains lines unchanged since the previous frame" (open). The
  description and root-cause analysis match ours exactly.
- **PR #143** — `fix(renderer): repaint unchanged rows inside hard-scrolled
  regions` (open, **approved** by a reviewer, **not merged** as of
  2026-07-15). One commit, +179/-0.

PR #143 is the correct fix and is strictly better than the `SetScrollOptim` bypass
we carried: it touches the scrolled region on `newbuf` after `scrollBuffer`, so the
repaint loop re-diffs every row whose physical cells moved, keeping the
optimization enabled. It adds a regression test asserting `curbuf` matches the
rendered frame after every render.

**Implication for Eitri:** no fork is needed once #143 lands in a release — upgrade
ultraviolet, drop the workaround, and re-enable the optimization. Track #143.

## Environment

- `github.com/charmbracelet/ultraviolet v0.0.0-20260903151058-ae99b731b8c5` (the
  version we pinned).
- Confirmed still broken in `v0.0.0-20260910203606-6c9e17dc7a16`:
  `terminal_renderer_hardscroll.go` is byte-identical and `SetScrollOptim` is
  unchanged.
- Rendered through bubbletea v2's `cursedRenderer`, which calls
  `scr.SetScrollOptim(runtime.GOOS != "windows")` unconditionally
  (`charm.land/bubbletea/v2@v2.0.9/cursed_renderer.go:654`). There is no public
  knob for a downstream app to opt out.

## Root cause

`scrollOptimize` is an Emacs-style "reuse scrolled lines" transform. It builds a
content-hash map (`updateHashmap`, `terminal_renderer_hashmap.go`) to decide which
lines merely moved versus which changed, then emits scroll-region operations
(DECSTBM + SU/SD/DL/IL) instead of repainting every row.

The failure mode is that a line whose content changed can be classified as a
moved line, so the region scroll carries the *old* cells into their new position
and the diff loop never repaints them. The terminal ends up showing an earlier
frame's line where the new content should be. Note the tell-tale TODO in
`updateHashmap`:

```go
// TODO: Investigate why this is needed. If we remove this
// line, scroll optimization does not work correctly. This
// should happen else where.
s.oldhash[i] = hash(&s.hasher, s.curbuf.Line(i))
```

Only lines with `newbuf.Touched[i] != nil` are rehashed; a changed-but-untouched
line keeps its stale hash and is treated as moved.

## Reproduction

We reproduced it through Eitri's real model: drive a long streamed reasoning turn
(120 deltas) and paint every frame through the exact renderer diff bubbletea uses,
once with the optimization requested and once without. Against upstream the two
diverge and the terminal ghost appears; with the optimization disabled per-line
diffing is correct.

The regression test we used (`internal/tui/alt_screen_scroll_regression_test.go`,
removed together with the fork — recover it from commit `7ab769c`):

```go
// (see git show 7ab769c:internal/tui/alt_screen_scroll_regression_test.go)
```

To restore: `git show 7ab769c -- internal/tui/alt_screen_scroll_regression_test.go`.

NOTE (TODO before filing): the strongest upstream repro would compare the
*terminal state* (feed the emitted bytes through a VT emulator / `tmux
capture-pane`) rather than raw byte equality, since an optimization may legitimately
emit different bytes for an equivalent screen. The current test treats byte
divergence as a corruption proxy. Distilling a minimal standalone case — a
synthetic `NewStyledString(...).Draw` sequence with no Eitri dependencies — is
still open.

## The change we carried

One function, in `terminal_renderer.go`. Upstream:

```go
func (s *TerminalRenderer) SetScrollOptim(v bool) {
	if v {
		s.flags.Set(tScrollOptim)
	} else {
		s.flags.Reset(tScrollOptim)
	}
}
```

Our fork:

```go
func (s *TerminalRenderer) SetScrollOptim(v bool) {
	_ = v
	s.flags.Reset(tScrollOptim)
}
```

i.e. ignore the enable request and always use the per-line diff path. This is a
**workaround, not a fix** — it disables the optimization wholesale rather than
correcting the classification. It cost a few bytes per frame and removed the
ghost.

## Suggested upstream fix

Already proposed in PR #143 (see above): `touchLine(newbuf, top, bot-top+1, true)`
in `scrolln` after `scrollBuffer`, so the repaint loop re-diffs the whole scrolled
region instead of trusting `newbuf.Touched`. The `updateHashmap` touched-line
rehash heuristic is a related smell but #143 fixes the symptom at its source.

## History

- `7ab769c` — vendor `third_party/ultraviolet`, add `replace` in go.mod, add the
  regression test.
- (later) — fork removed; back to the upstream module. This doc preserves the
  change for the upstream report.
- Found that the bug is upstream issue #137 with fix PR #143 in review; the proper
  path is to adopt #143 on release rather than re-fork.

## References

- `terminal_renderer_hardscroll.go` — `scrollOptimize`, `scrolln`, `scrollBuffer`.
- `terminal_renderer_hashmap.go` — `updateHashmap`, `growHunks`, `costEffective`.
- `terminal_renderer.go` — `tScrollOptim` gate at line ~1456; `SetScrollOptim`.
