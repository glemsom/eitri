package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
)

// BenchRoot cause the reported CPU crawl during long busy turns: a live turn's
// event timeline re-renders on every busy frame, and each completed tool card
// re-paid lipgloss Style.Render on every one. The completed-entry render cache
// (toolLog.entryCache) makes frame cost flat in the number of already-committed
// tool entries, so the per-frame cost of a long tool-heavy live turn stays near
// the short-turn floor instead of growing linearly with the timeline.
//
// Run: go test ./internal/tui -run xxx -bench BenchmarkLiveTurnTimeline -benchmem
func BenchmarkLiveTurnTimeline(b *testing.B) {
	b.Setenv("EITRI_ASCII_GLYPHS", "1")
	for _, events := range []int{200, 8000} {
		b.Run("events_"+strconv.Itoa(events), func(b *testing.B) {
			th := themeFor(config.DefaultTheme)
			f := &Fold{}
			f.session = NewTurnSession(nil)
			tx := &Transcript{theme: th, configTheme: config.DefaultTheme, width: 120, height: 40,
				histFollow: true, histViewport: newHistoryViewport(), busy: true}
			f.session.flow.Reset()
			tx.messages = append(tx.messages, message{role: "you", content: "research task", events: synthAnswerLog("research task")})
			f.session.curStream = -1
			tx.live = f.session
			for i := 0; i < events; i++ {
				tx.log.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"run cmd"}`}})
				tx.log.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "ok", Lines: 1}})
				f.session.flow.ObserveTool(TimelineEvent{Kind: EventToolStart, Seq: i * 2, Start: &ToolStart{Name: "bash", Args: `{"command":"cmd"}`}})
				f.session.flow.ObserveTool(TimelineEvent{Kind: EventToolResult, Seq: i*2 + 1, Result: &ToolResult{Name: "bash", Result: "ok"}})
			}
			tx.renderPaneContent()
			tx.busyPrefixDirty = false
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tx.spinner = (tx.spinner + 1) % 10
				_ = tx.renderPaneContent()
			}
		})
	}
}

// TestLiveTurnToolCache_byteIdentical locks that the completed-entry render
// cache never changes the bytes a fresh renderer produces, across every surface
// variant an entry can hit (outcome, expanded, focused). It is the correctness
// guard behind BenchmarkLiveTurnTimeline: the cache may only make frames
// cheaper, not different.
func TestLiveTurnToolCache_byteIdentical(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	results := []string{"ok", "line1\nline2\nline3", "error executing tool: boom", ""}
	for _, res := range results {
		for _, expanded := range []bool{false, true} {
			for _, focused := range []bool{false, true} {
				l := &toolLog{}
				e := toolEntry{name: "bash", args: "run it", anchor: 0, complete: true,
					result: res, lines: 3, startedAt: tt0, doneAt: tt1}
				l.entries = []toolEntry{e}
				want := renderToolEntry(th, e, expanded, ttMid, 120, true, focused)
				got := l.renderEntry(0, 120, th, expanded, focused, true)
				if got != want {
					t.Errorf("res=%q expanded=%v focused=%v byte-mismatch", res, expanded, focused)
				}
				// A repeat frame must serve the same cached bytes.
				if again := l.renderEntry(0, 120, th, expanded, focused, true); again != want {
					t.Errorf("repeated cached render drifted: res=%q", res)
				}
			}
		}
	}
}

// TestLiveTurnToolCache_widthIsKeyed locks that cache entries are keyed by the
// render width: a width change must not serve bytes built for another width,
// and the original width must still hit on return.
func TestLiveTurnToolCache_widthIsKeyed(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	l := &toolLog{}
	l.entries = []toolEntry{{name: "bash", args: "a very long command argument that should wrap under narrow widths in interesting ways", anchor: 0, complete: true, result: "ok", startedAt: tt0, doneAt: tt1}}
	wide := l.renderEntry(0, 120, th, false, false, false)
	narrow := l.renderEntry(0, 40, th, false, false, false)
	if wide == narrow {
		t.Fatal("different widths must render differently")
	}
	if back := l.renderEntry(0, 40, th, false, false, false); back != narrow {
		t.Fatal("narrow-width cache was not reused after a wide render")
	}
}

// TestLiveTurnToolCache_dropsOnMutation locks that a completed entry whose
// result arrives (Apply Result) invalidates its memo so the filled render is
// produced, and a new entry (Apply Start) does not touch earlier siblings'
// cached rows, because toolLog entries are append-only and their indexes are
// therefore stable.
func TestLiveTurnToolCache_completionInvalidatesStartRetains(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	l := &toolLog{}
	l.SetAnchor(0)
	// Start an entry and let a stale collapsed render land before it completes.
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	l.entries[0].startedAt = tt0
	l.entries[0].doneAt = tt1
	_ = l.renderEntry(0, 120, th, false, false, false) // complete=false already; not cached
	// Complete it: cache must not hold the pre-result bytes.
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2}})
	if _, ok := l.cached(0, 120, false, false); ok {
		t.Fatal("completed entry must not be served from a stale pre-result cache")
	}
	got := l.renderEntry(0, 120, th, false, false, false)
	if !strings.Contains(got, "2 line") || !strings.Contains(got, "ok") {
		t.Fatalf("completed result must render its summary, got %q", got)
	}
	// A completed entry renders once and lands in the cache.
	if _, ok := l.cached(0, 120, false, false); !ok {
		t.Fatal("completed entry should now be cached")
	}
	// Appending a new entry must not evict a completed sibling's cached row:
	// indexes are stable (append-only) and the fresh entry is incomplete, so it
	// needs no invalidation and earlier cards stay in the memo across the turn.
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"x"}`}})
	if len(l.entries) != 2 {
		t.Fatalf("start must append one entry, got %d", len(l.entries))
	}
	if l.entries[1].complete {
		t.Fatal("a fresh start must be incomplete")
	}
	if _, ok := l.cached(0, 120, false, false); !ok {
		t.Fatal("appending a new entry must not evict a completed sibling's cached render")
	}
	if got := l.renderEntry(0, 120, th, false, false, false); !strings.Contains(got, "2 line") || !strings.Contains(got, "ok") {
		t.Fatalf("completed sibling should still render from cache, got %q", got)
	}
}

var (
	tt0   = parseUTC("2026-01-01T00:00:00Z")
	tt1   = parseUTC("2026-01-01T00:00:02Z")
	ttMid = parseUTC("2026-01-01T00:00:01Z")
)

func parseUTC(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }

// BenchmarkToolEntryRender_CachedAcrossStarts isolates the completed-entry render
// cache (toolLog.renderEntry) itself, stripping the whole-page rebuild/copy that
// BenchmarkLiveTurnTimeline is dominated by. A live turn's committed tool cards
// re-render each busy frame; entryCache must make that cost flat in the number
// of already-completed tools instead of re-paying lipgloss Style.Render per card
// per frame. With the memo surviving each new Apply(Start) (entries are
// append-only so indexes are stable), a frame that re-renders N cached cards
// stays near the N=1 cost.
//
// Run: go test ./internal/tui -run xxx -bench BenchmarkToolEntryRender -benchmem
func BenchmarkToolEntryRender_CachedAcrossStarts(b *testing.B) {
	b.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	for _, n := range []int{1, 200} {
		b.Run("tools_"+strconv.Itoa(n), func(b *testing.B) {
			l := &toolLog{}
			l.SetAnchor(0)
			for i := 0; i < n; i++ {
				l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"run cmd"}`}})
				l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "ok", Lines: 1}})
			}
			// Seed the memo once: completed cards are immutable between frames.
			for i := 0; i < n; i++ {
				_ = l.renderEntry(i, 120, th, false, false, false)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for j := 0; j < n; j++ {
					_ = l.renderEntry(j, 120, th, false, false, true)
				}
			}
		})
	}
}
