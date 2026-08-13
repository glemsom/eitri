package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

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
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
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
	m.renderHistory(&hist)
	m.renderBand(&band)
	hs, bs := hist.String(), band.String()

	// Scroll region: history + workspace header + tool entry; never the
	// composer or the status strip (both live in the fixed band).
	for _, want := range []string{"eitri", "workspace: /tmp/acme-project", "⊕ edit"} {
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
	if strings.Contains(hs, "cache:80%") {
		t.Errorf("status strip leaked into the scroll region, got:\n%s", hs)
	}

	// Fixed band: status strip + composer; never the message body.
	if !strings.Contains(bs, "cache:80%") {
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
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		WorkspacePath: "/tmp/acme-project",
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	var hist, band strings.Builder
	m.renderHistory(&hist)
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

// TestReviewRegion_ClipsTallDiff asserts the review overlay gets its own
// height-clipped region (issue T06 AC1): a tall expanded diff clips instead of
// overflowing the terminal, so the fixed bottom band (composer) stays the final
// bottom-pinned region and the pane never exceeds the terminal height.
func TestReviewRegion_ClipsTallDiff(t *testing.T) {
	feed := NewToolFeed()
	// Build a long after-body so the expanded diff dwarfs the short window.
	after := make([]string, 60)
	for i := range after {
		after[i] = fmt.Sprintf("line-%02d", i)
	}
	m := NewModelCfg(Dependencies{
		Turn:  func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Tools: feed,
	})
	// Small window: band ~2 rows leaves only a handful of history/viewport rows.
	m = resizeTo(t, m, 80, 10)
	nm := feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "edit", Args: `{"path":"/w/big.go"}`}})
	m = asModel(t, nm)
	nm = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{
		Name: "edit", Result: "edited\n", Added: 60, Removed: 0,
		Before: "old\n", After: strings.Join(after, "\n") + "\n", Path: "/w/big.go",
	}})
	m = asModel(t, nm)

	// Open the review panel and expand the focused file's inline diff.
	m = reopenReview(t, m)
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	got := m.renderPane()
	paneLines := len(strings.Split(strings.TrimRight(got, "\n"), "\n"))
	if paneLines > 10 {
		t.Errorf("pane (%d lines) exceeds the %d-row terminal: review region must clip, not overflow\n%s", paneLines, 10, got)
	}
	// The band stays the final (bottom-pinned) region, so the composer survives.
	var band strings.Builder
	m.renderBand(&band)
	bandStr := band.String()
	if !strings.HasSuffix(got, bandStr) {
		t.Errorf("band (composer) not bottom-pinned; review flowed over it:\n%s", got)
	}
	// A tail line deep in the diff must be clipped out of the pane.
	if strings.Contains(got, "line-59") {
		t.Errorf("tall diff tail not clipped out of the review region:\n%s", got)
	}
}
