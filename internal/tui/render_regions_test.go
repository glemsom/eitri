package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestModel_expandedViewEditCardRendersInlineDiff asserts the Ctrl+E
// expanded-view mode renders an edit/write card as the before→after inline
// diff inside the card frame, and that toggling the mode off restores the
// collapsed [+N,−M] summary (issue #275 AC through the transcript seam).
func TestModel_expandedViewEditCardRendersInlineDiff(t *testing.T) {
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Tools: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "edit it")
	m = submitAndWait(t, m)
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "edit", Args: `{"path":"internal/auth.go"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{
		Name: "edit", Result: "Edit applied\n", Added: 1, Removed: 1,
		Before: "package auth\n\nfunc Old() {}\n", After: "package auth\n\nfunc New() {}\n", Path: "internal/auth.go",
	}})

	// Collapsed: the head keeps the [+N,−M] delta tag and the old/new bodies
	// never render.
	collapsed := view(m)
	if !strings.Contains(collapsed, "[+1, −1]") {
		t.Errorf("collapsed card must keep the delta tag, got: %q", collapsed)
	}
	if strings.Contains(collapsed, "func Old") || strings.Contains(collapsed, "func New") {
		t.Errorf("collapsed card must not leak before/after content, got: %q", collapsed)
	}

	// Expanded view (Ctrl+E): the card renders the inline diff instead of the
	// raw result dump, framed by the card's left border.
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	strip := ansiStrip(view(m))
	if !strings.Contains(strip, "-func Old() {}") || !strings.Contains(strip, "+func New() {}") {
		t.Errorf("expanded-view edit card must render the inline diff, got:\n%s", strip)
	}
	if !strings.Contains(strip, "@@ -1,3 +1,3 @@") {
		t.Errorf("expanded-view edit card must render git-style hunk headers, got:\n%s", strip)
	}
}

// TestRenderRegions_HistoryVsBandSeparation asserts the regioned render seam
// (issue T01): the history is produced by the scroll region (renderHistory) and
// the status strip + composer by the fixed band
// (renderBand), with no content leaking across the seam. This is the region
// boundary that T02+ later drives with height-aware viewport + band pinning.
func TestRenderRegions_HistoryVsBandSeparation(t *testing.T) {
	feed := NewToolFeed()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer", Reasoning: "I think first."}, nil
		},
		WorkspacePath: "/tmp/acme-project",
		Telemetry:     te,
		Tools:         feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "edit it")
	m = submitAndWait(t, m)
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "edit", Args: `{"path":"internal/main.go"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{
		Name: "edit", Result: "Edit applied\n", Added: 2, Removed: 0,
	}})

	// Drive a slash prefix into the composer so the completion list is active.
	m = typeText(t, m, "/")

	var hist, band strings.Builder
	m.tx.renderHistory(&hist, nil, nil)
	m.renderBand(&band)
	hs, bs := hist.String(), band.String()

	// Scroll region: history + workspace header + tool entry; never the
	// composer or the status strip (both live in the fixed band).
	for _, want := range []string{"workspace: /tmp/acme-project", "✂️ edit"} {
		if !strings.Contains(hs, want) {
			t.Errorf("scroll region missing %q, got:\n%s", want, hs)
		}
	}
	// Glamour word-wraps the markdown answer across ANSI runs, so match on the
	// word (as elsewhere in the suite): the answer body belongs to the scroll.
	if !strings.Contains(hs, "plain") || !strings.Contains(hs, "answer") {
		t.Errorf("scroll region missing the message body, got:\n%s", hs)
	}
	if strings.Contains(hs, m.composer.View()) {
		t.Errorf("composer leaked into the scroll region, got:\n%s", hs)
	}
	if strings.Contains(hs, "ctrl+s settings") {
		t.Errorf("status strip leaked into the scroll region, got:\n%s", hs)
	}

	// Fixed band: status strip (now hints-only, issue #228) + composer; never
	// the message body.
	if !strings.Contains(bs, "ctrl+s settings") {
		t.Errorf("band missing status strip, got:\n%s", bs)
	}
	if !strings.Contains(bs, m.composer.View()) {
		t.Errorf("band missing composer, got:\n%s", bs)
	}
	if strings.Contains(bs, "plain") {
		t.Errorf("message body leaked into the band, got:\n%s", bs)
	}
}

// resizeTo sizes the model to the given terminal dims (Height-aware viewport,
// issue T02) so height-clipping behaviour can be exercised at a specific size.
func resizeTo(t *testing.T, m Model, width, height int) Model {
	t.Helper()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return asModel(t, nm)
}

// TestRenderPane_ComposesRegionsInOrder asserts renderPane composes the scroll
// and band regions in order with the band last — the region seam that T02+
// builds on. With a resize landed, the scroll region is Height-aware:
// renderPane is the height-clamped history
// followed by the fixed band, which stays the final (bottom-pinned) region.
func TestRenderPane_ComposesRegionsInOrder(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		WorkspacePath: "/tmp/acme-project",
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	var hist, band strings.Builder
	m.tx.renderHistory(&hist, nil, nil)
	m.renderBand(&band)
	bandStr := band.String()

	got := m.renderPane()
	// The band is the last region; everything before it is the height-clamped
	// history viewport.
	if !strings.HasSuffix(got, bandStr) {
		t.Errorf("renderPane must keep the band as the final (bottom-pinned) region\n--- got ---\n%s", got)
	}
	head := strings.TrimSuffix(got, bandStr)
	// For a short transcript that fits in the viewport, the height-clamped
	// history region equals the rendered scroll region modulo the native
	// viewport's width padding: each row is padded to the pane width, and the
	// region drops a trailing newline. Trim per-line whitespace and drop blank
	// tail rows to compare content (not padding).
	wantLines := normalizeRows(hist.String())
	gotLines := normalizeRows(head)
	if len(gotLines) != len(wantLines) {
		t.Fatalf("renderPane head (%d rows) != height-clamped history region (%d rows)\n--- want --------\n%s\n--- got ---------\n%s", len(gotLines), len(wantLines), hist.String(), head)
	}
	for i := range wantLines {
		if gotLines[i] != wantLines[i] {
			t.Errorf("renderPane head row %d mismatch\n want: %q\n got: %q", i, wantLines[i], gotLines[i])
		}
	}
}

// normalizeRows splits a render string into its content rows, trimming each
// row's trailing whitespace and dropping the blank padded tail rows that a
// native viewport appends, so two renders with the same content but different
// padding compare equal.
func normalizeRows(s string) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, strings.TrimRight(l, " "))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// TestReviewRegion_ClipsTallDiff is obsolete with the modal review panel
// (issue #276): the overlay and its height-clipped region are gone, so there
// is no review region left to clip. The expanded card path pins the diff
// inside the scroll region instead — tall card diffs clip against the native
// history viewport, whose height clamp is covered by
// TestRenderRegions_HistoryVsBandSeparation plus toolcard_diff_test.go. This
// test is deleted rather than re-homed because its seam (the review region)
// no longer exists.
