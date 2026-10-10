package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/tui/telemetry"
)

// frameCase names a scenario whose rendered frame is pinned as golden bytes.
type frameCase struct {
	name  string
	build func(t *testing.T) Model
}

// frameCases covers the frame-composition branches: idle/rail-visible,
// rail-hidden, busy, and content past the bottom with the rail on and off.
func frameCases() []frameCase {
	rail := func() *Rail {
		return NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	}
	stub := func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{Answer: "answer " + prompt}, nil
	}
	tallHistory := func(t *testing.T) Model {
		m := NewModelCfg(Dependencies{Turn: stub})
		for i := 1; i <= 5; i++ {
			m = typeText(t, m, "q"+string(rune('a'+i-1)))
			m = submitAndWait(t, m)
		}
		return m
	}
	return []frameCase{
		{"idle_rail_visible", func(t *testing.T) Model {
			m := NewModelCfg(Dependencies{Rail: rail()})
			return resizeTo(t, m, 120, 30)
		}},
		{"idle_rail_hidden", func(t *testing.T) Model {
			m := NewModelCfg(Dependencies{})
			return resizeTo(t, m, 100, 20)
		}},
		{"busy_rail_visible", func(t *testing.T) Model {
			m := NewModelCfg(Dependencies{Turn: streamingTurn, Events: NewEventFeed(), Rail: rail()})
			m = resizeTo(t, m, 120, 30)
			m = typeText(t, m, "hi")
			m, _ = submitBusy(t, m)
			return applyDelta(t, m, "Working on it now, streaming a long answer that wraps the pane.")
		}},
		{"past_bottom_rail_visible", func(t *testing.T) Model {
			m := tallHistory(t)
			m.tx.rail = rail()
			return resizeTo(t, m, 120, 12)
		}},
		{"past_bottom_rail_hidden", func(t *testing.T) Model {
			m := tallHistory(t)
			return resizeTo(t, m, 100, 12)
		}},
	}
}

func goldenFramePath(name string) string {
	return filepath.Join("testdata", "frame_golden", name+".txt")
}

// assertGoldenFrame pins got against the golden bytes. Set EITRI_GOLDEN_WRITE=1
// to (re)record, which only ever records the pre-refactor model path.
func assertGoldenFrame(t *testing.T, name, got string) {
	t.Helper()
	path := goldenFramePath(name)
	if os.Getenv("EITRI_GOLDEN_WRITE") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	if got != string(want) {
		t.Errorf("frame %s changed\n got: %q\nwant: %q", name, got, string(want))
	}
}

// TestFrameGoldenModelView pins the whole-view bytes captured from the
// compose-then-trim path before the composition moved into Transcript.View.
func TestFrameGoldenModelView(t *testing.T) {
	for _, tc := range frameCases() {
		t.Run(tc.name, func(t *testing.T) {
			assertGoldenFrame(t, tc.name, view(tc.build(t)))
		})
	}
}

// TestViewMatchesGoldenComposition drives the new Transcript.View seam with the
// caller-rendered band and requires it to reproduce the pinned frame bytes.
func TestViewMatchesGoldenComposition(t *testing.T) {
	for _, tc := range frameCases() {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.build(t)
			var band strings.Builder
			m.renderBand(&band)
			bs := band.String()
			assertGoldenFrame(t, tc.name, m.tx.View(bs, lineCount(bs)))
		})
	}
}

func TestGeometryReportsFrameDimensions(t *testing.T) {
	tx := NewTranscript(TranscriptConfig{Rail: NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")})
	tx.SetSize(120, 30)
	g := tx.Geometry()
	if g.Width != 120 || g.Height != 30 {
		t.Errorf("Geometry pane size = %dx%d, want 120x30", g.Width, g.Height)
	}
	if g.BandWidth != 120 {
		t.Errorf("Geometry band width = %d, want 120", g.BandWidth)
	}
	if g.RailWidth != tx.railWidthOrDefault() {
		t.Errorf("Geometry rail width = %d, want the rail's column width %d", g.RailWidth, tx.railWidthOrDefault())
	}
	if !g.RailVisible {
		t.Error("Geometry must report a wired rail as visible")
	}

	hidden := NewTranscript(TranscriptConfig{})
	hidden.SetSize(100, 20)
	if hg := hidden.Geometry(); hg.RailVisible {
		t.Error("Geometry must report no rail as hidden")
	}
}

func TestRailHeightAnswersRowCountWithoutRerender(t *testing.T) {
	tx := NewTranscript(TranscriptConfig{
		Rail:      NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a"),
		Telemetry: telemetry.NewTelemetry("deepseek-v4-flash", "low", true, 250),
	})
	tx.SetSize(120, 30)
	tx.View("band\n", 1)
	if got := tx.RailHeight(1); got != 14 {
		t.Errorf("RailHeight(band 1) = %d, want the rail's 14 content rows", got)
	}
	tx.SetSize(120, 5)
	if got := tx.RailHeight(1); got != 4 {
		t.Errorf("RailHeight(band 1) = %d, want it capped to the 4-row rail region", got)
	}
}
