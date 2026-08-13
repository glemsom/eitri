package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// This file covers ADR-0006 decision 4 (issue T03): bounded re-render on
// resize. The scroll region's content is cached per width-bucket, the full
// history/markdown rebuild happens only when a message actually changed or the
// width-bucket changed, and rapid bursts of resize events coalesce to a single
// rebuild instead of re-rendering the whole history every tick.

// TestResizeCache_ReusesHistoryWithinABucket asserts the scroll region's
// rendered content is cached per width-bucket and reused across resizes that
// stay within the same bucket (ADR-0006 decision 4, issue T03 AC1): a resize
// that changes the terminal width but not the transcript's width-bucket must
// not rebuild the history content from scratch, and the rendered output must
// be byte-identical to the prior render.
func TestResizeCache_ReusesHistoryWithinABucket(t *testing.T) {
	m := buildResizeCacheModel(t)
	// First resize lands in bucket 120/widthBucketCols; the first View build
	// populates the cache.
	m = applyResize(t, m, 120, 24)
	base := m.historyContent() // first build
	if got := cacheBucket(m); got != widthBucket(120) {
		t.Fatalf("first render bucket = %d, want %d", got, widthBucket(120))
	}
	if got := rebuilds(m); got != 1 {
		t.Fatalf("initial view should rebuild once, got %d rebuilds", got)
	}

	// A second resize within the same bucket (120 and 126 share bucket 7)
	// must reuse the cache: no rebuild, identical rendered history region. The
	// band/composer may still re-wrap to the new width, so assert the bounded
	// scroll region (historyContent) rather than the whole View.
	m = applyResize(t, m, 126, 24)
	if got := widthBucket(126); got != widthBucket(120) {
		t.Fatalf("test widths must share a bucket, got %d vs %d", got, widthBucket(120))
	}
	after := m.historyContent()
	if got := rebuilds(m); got != 1 {
		t.Errorf("same-bucket resize rebuilt history (got %d rebuilds, want 1); want cached reuse", got)
	}
	if after != base {
		t.Errorf("same-bucket resize re-rendered the history region; want cached reuse\n--- before ---\n%s\n--- after ---\n%s", base, after)
	}
}

// TestResizeCache_RebuildsOnBucketBoundary asserts the cache is invalidated and
// the history re-renders when the transcript width crosses a bucket boundary —
// a real width change for wrapping purposes (ADR-0006 decision 4, issue T03
// AC2).
func TestResizeCache_RebuildsOnBucketBoundary(t *testing.T) {
	m := buildResizeCacheModel(t)
	m = applyResize(t, m, 120, 24)
	_ = m.View() // first build
	baseBuilds := rebuilds(m)

	// Width far enough that the transcript crosses at least one bucket.
	m = applyResize(t, m, 200, 24)
	if widthBucket(200) == widthBucket(120) {
		t.Fatalf("test widths must cross a bucket boundary")
	}
	_ = m.View()
	if got := rebuilds(m); got != baseBuilds+1 {
		t.Errorf("bucket-crossing resize should rebuild exactly once (got %d rebuilds, want %d)", got, baseBuilds+1)
	}
}

// TestResizeCache_RebuildsOnMessageChange asserts a new committed message
// invalidates the cache and is reflected in the next render at an unchanged
// width-bucket (ADR-0006 decision 4, issue T03 AC2: rebuild also fires on a
// real message change).
func TestResizeCache_RebuildsOnMessageChange(t *testing.T) {
	m := buildResizeCacheModel(t)
	m = applyResize(t, m, 120, 24)
	_ = m.View() // first build
	baseBuilds := rebuilds(m)

	// A new turn changes history content at the same width-bucket.
	m = typeText(t, m, "second")
	m = submitAndWait(t, m)
	_ = m.View()
	if got := rebuilds(m); got <= baseBuilds {
		t.Errorf("message change should rebuild history (got %d rebuilds, want > %d)", got, baseBuilds)
	}
	// Glamour word-wraps across ANSI runs; match on the bare words (as the rest
	// of the suite does) instead of the contiguous styled phrase.
	v := m.View()
	if !strings.Contains(v, "second") || !strings.Contains(v, "answer") {
		t.Errorf("new message missing after rebuild, view:\n%q", v)
	}
}

// TestResizeCache_CoalescesResizeBurst asserts a rapid burst of resize events
// that stays within the current width-bucket coalesces to zero re-renders
// (ADR-0006 decision 4, issue T03 AC3): dragging a window fires many
// WindowSizeMsg ticks; those sharing a bucket must not each rebuild the
// history. A single View after the burst serves the latest cached content.
func TestResizeCache_CoalescesResizeBurst(t *testing.T) {
	m := buildResizeCacheModel(t)
	m = applyResize(t, m, 100, 24)
	_ = m.View() // first build
	start := rebuilds(m)

	// A burst of resizes all within the current bucket: no rebuilds.
	for _, w := range []int{101, 105, 103, 106, 102} {
		m = applyResize(t, m, w, 24)
	}
	_ = m.View()
	if got := rebuilds(m); got != start {
		t.Errorf("same-bucket resize burst rebuilt history %d times (start %d); want coalesced no-rebuild", got-start, start)
	}
}

// buildResizeCacheModel builds a model with several committed turns so a
// history rebuild is non-trivial and a cache is clearly observable.
func buildResizeCacheModel(t *testing.T) Model {
	t.Helper()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "answer " + prompt, Reasoning: "reasoning " + prompt}, nil
		},
	})
	for i := 0; i < 3; i++ {
		m = typeText(t, m, "q")
		m = submitAndWait(t, m)
	}
	return m
}

// applyResize drives one WindowSizeMsg into the model, asserting the returned
// tea.Model is still a tui.Model.
func applyResize(t *testing.T, m Model, w, h int) Model {
	t.Helper()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return asModel(t, nm)
}

// rebuilds reports the scroll-region render-cache rebuild count (test seam).
func rebuilds(m Model) int {
	return m.histCache.rebuilds
}

// cacheBucket reports the width-bucket the scroll-region cache was last built
// for (test seam). -1 when no build has landed.
func cacheBucket(m Model) int {
	return m.histCache.bkt
}
