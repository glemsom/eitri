package tui

import (
	"context"
	"strings"
	"testing"
)

// TestRenderRegions_HistoryVsBandSeparation asserts the regioned render seam
// (ADR-0006 decision 6, issue T01): the history is produced by the scroll
// region (renderHistory) and the status strip + composer by the fixed band
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

// TestRenderPane_ComposesRegionsInOrder asserts renderPane composes the scroll
// and band regions in order with the band last — the region seam that T02+
// builds on (ADR-0006 decision 6). With a resize landed, the scroll region is
// Height-aware (ADR-0006 decision 3): renderPane is the height-clamped history
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
	// history equals the rendered scroll region modulo trailing-whitespace
	// normalization (band height left it all room). The viewport strips a
	// trailing newline, so compare trimmed.
	if strings.TrimRight(head, "\n") != strings.TrimRight(hist.String(), "\n") {
		t.Errorf("renderPane head != height-clamped history region\n--- want --------\n%s\n--- got ---------\n%s", hist.String(), head)
	}
}
