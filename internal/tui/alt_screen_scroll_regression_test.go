package tui

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
)

// resetWriter lets the renderer's per-frame output be isolated for assertions.
type resetWriter struct{ b *bytes.Buffer }

func (w resetWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

// altDiff reproduces bubbletea v2's cursed renderer diff pipeline that paints the
// live TUI to the alternate screen: one reused screen buffer per frame, diffed by
// a single uv.TerminalRenderer. scrollOptim selects whether hard-scroll
// optimization is requested, exactly as bubbletea requests it on non-Windows.
type altDiff struct {
	scr  *uv.TerminalRenderer
	cell uv.ScreenBuffer
	out  *bytes.Buffer
	w, h int
}

func newAltDiff(w, h int, scrollOptim bool) *altDiff {
	out := &bytes.Buffer{}
	env := []string{"TERM=xterm-256color", "COLORTERM=truecolor"}
	scr := uv.NewTerminalRenderer(resetWriter{b: out}, env)
	scr.SetColorProfile(colorprofile.TrueColor)
	scr.SetScrollOptim(scrollOptim)
	scr.SetMapNewline(false)
	scr.SetFullscreen(true) // alternate screen
	scr.SetRelativeCursor(false)
	return &altDiff{scr: scr, cell: uv.NewScreenBuffer(w, h), out: out, w: w, h: h}
}

// frame draws one view into the reused screen buffer, diffs it, and returns the
// raw bytes emitted to the terminal for this frame.
func (d *altDiff) frame(content string) []byte {
	d.out.Reset()
	if lines := strings.Split(content, "\n"); len(lines) > d.h {
		content = strings.Join(lines[len(lines)-d.h:], "\n")
	}
	d.cell.Clear()
	uv.NewStyledString(content).Draw(d.cell, uv.Rect(0, 0, d.w, d.h))
	d.scr.Render(d.cell.RenderBuffer)
	_ = d.scr.Flush()
	return append([]byte(nil), d.out.Bytes()...)
}

// runEventCmd executes a tea.Cmd tree (unwrapping batches) so a blocking Turn
// stub can arm a live turn while the test streams reasoning deltas.
func runEventCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if bm, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range bm {
			runEventCmd(c)
		}
	}
}

// TestAltScreen_scrollOptimizationDisabledForStreamingCoT is the regression
// guard for the alternate-screen scroll-optimization corruption behind the
// "chain-of-thought tokens in two places" report. Upstream ultraviolet's hard
// scroll optimization (scrollOptimize) corrupts the screen when a live region
// scrolls while a line's content also changes: it leaves an earlier frame's line
// painted in place while painting the new one elsewhere, and the corruption
// never recovers. Eitri's streaming chain-of-thought — a block that reflows and
// scrolls on every delta while its token-count header keeps changing — triggers
// it reliably.
//
// The fix (third_party/ultraviolet patch) ignores the optimization request and
// always uses the per-line diff path, which is correct. This test drives the
// real Model through a long streamed reasoning turn and paints every frame
// through the exact renderer diff bubbletea uses, then asserts the
// scroll-optimization-requested output equals the per-line output. They are
// byte-identical only while the optimization is disabled; against the corrupt
// upstream optimization they diverge, so this test is red-capable.
func TestAltScreen_scrollOptimizationDisabledForStreamingCoT(t *testing.T) {
	feed := NewEventFeed()
	started := make(chan struct{})
	release := make(chan struct{})
	m := NewModelCfg(Dependencies{
		Events: feed,
		Turn: func(ctx context.Context, _ string, _ string) (TurnResult, error) {
			close(started)
			<-release
			return TurnResult{Answer: "final answer", Reasoning: "done"}, nil
		},
	})
	m = resize(t, m)
	m.runtime.SetThinkingEnabled(true)
	m.tx.cotExpanded = true

	m = typeText(t, m, "dig into VM latency")
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = asModel(t, nm)
	if cmd == nil {
		t.Fatal("submit produced no turn command")
	}
	go func() { runEventCmd(cmd) }()
	<-started
	defer close(release)

	requested := newAltDiff(80, 24, true)   // as bubbletea requests
	perLine := newAltDiff(80, 24, false)    // the always-correct path
	var reqBytes, perBytes []byte
	capture := func() {
		content := m.View().Content
		reqBytes = append(reqBytes, requested.frame(content)...)
		perBytes = append(perBytes, perLine.frame(content)...)
	}
	capture()
	for i := 0; i < 120; i++ {
		delta := fmt.Sprintf("step %d: analyze the vm-exit profile, the msr traps, the tick, and the mwait idle spin\n", i)
		nm, _ := m.applyEvent(Event{Stream: &StreamUpdate{Kind: ReasoningStream, Delta: delta}})
		m = asModel(t, nm)
		capture()
	}

	if !bytes.Equal(reqBytes, perBytes) {
		t.Fatalf("scroll-optimization-requested render diverged from the per-line render "+
			"(%d vs %d bytes): the corrupt alternate-screen scroll path is active and leaves "+
			"stale rows, showing chain-of-thought in two places", len(reqBytes), len(perBytes))
	}
}
