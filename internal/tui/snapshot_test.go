package tui

// Snapshot frame renderer for the autoresearch aesthetic loop. Gated by
// EITRI_SNAPSHOT=1 (never runs in CI): renders scripted model states to ANSI
// frame files so .auto/measure.sh can rasterize and score the surface.
//
//	EITRI_SNAPSHOT=1 EITRI_SNAPSHOT_DIR=.auto/frames COLORTERM=truecolor \
//	  go test ./internal/tui/ -run TestSnapshot_frames -count=1

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

// newSnapshotRail builds the harness rail with a seeded branch so the frames
// show the CONTEXT branch line.
func newSnapshotRail() *Rail {
	r := NewRail("deepseek", "deepseek-v4-flash", "high", true, "eitri-9f3a", "/tmp/eitri-9f3a")
	r.SetBranch("main")
	return r
}

// snapshotDeps builds the common dependency set for the scripted session:
// telemetry, stream, tool feed, rail, skills, workspace, models.
func snapshotDeps(cfg config.Config) (Dependencies, *Telemetry, *Streamer, *ToolFeed) {
	te := NewTelemetry("deepseek-v4-flash", "high", true, 10)
	stream := NewStreamer()
	tools := NewToolFeed()
	return Dependencies{
		Turn:          streamingTurn,
		WorkspacePath: "/home/dev/acme",
		Config:        cfg,
		Models:        []string{"deepseek-v4-flash", "deepseek-v3", "deepseek-v3-0324"},
		Telemetry:     te,
		Stream:        stream,
		Tools:         tools,
		Rail:          newSnapshotRail(),
		Skills:        &SkillsSurface{Items: []SkillItem{{Name: "rust-review"}, {Name: "refactor"}}},
	}, te, stream, tools
}

// upd delivers one message through the model's Update seam and returns the next
// model, discarding any re-issued wait commands (the harness drives the state
// manually; waiters would block on their channels).
func upd(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	nm, _ := m.Update(msg)
	return asModel(t, nm)
}

// toolStart / toolResult feed tool-call observations like the engine seam
// would through the feed channel path, but applied directly via the message
// seam for deterministic ordering.
func toolStart(t *testing.T, m Model, name, args string) Model {
	t.Helper()
	return upd(t, m, toolUpdateMsg{update: ToolUpdate{Start: &ToolStart{Name: name, Args: args}}})
}

func toolResult(t *testing.T, m Model, r ToolResult) Model {
	t.Helper()
	return upd(t, m, toolUpdateMsg{update: ToolUpdate{Result: &r}})
}

// backdateTool rewinds the given tool entry's start so the elapsed timer shows
// a realistic runtime in the snapshot (real Start/Result land microseconds
// apart). Works for completed entries (frozen span) and pending ones (live
// span while busy).
func backdateTool(m Model, idx int, d time.Duration) Model {
	m.tx.log.SetStart(idx, time.Now().Add(-d))
	return m
}

// writeFrame renders the current view to an .ans file under the output dir.
func writeFrame(t *testing.T, out, name string, m Model) {
	t.Helper()
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(out, name+".ans"), []byte(view(m)), 0o644); err != nil {
		t.Fatalf("write frame %s: %v", name, err)
	}
	t.Logf("wrote %s", name)
}

// loginBefore/loginAfter back the snapshot's edit tool result and the
// expanded card's inline diff: the mock-clock freeze the agent applies to the
// flaky login test.
const (
	loginBefore = `package auth

func TestLogin(t *testing.T) {
	t.Parallel()
	mock := newMockClock()
	user := mustCreateUser(t)
	tok := issueToken(user, mock.Now())
	if tok.IssuedAt() != mock.Now() {
		t.Fatalf("issued-at mismatch")
	}
}
`
	loginAfter = `package auth

func TestLogin(t *testing.T) {
	t.Parallel()
	mock := newMockClock()
	mock.Freeze()
	defer mock.Unfreeze()
	user := mustCreateUser(t)
	tok := issueToken(user, mock.Now())
	if tok.IssuedAt() != mock.Now().UTC() {
		t.Fatalf("issued-at mismatch")
	}
}
`
)

// TestSnapshot_frames renders the scripted session states to .ans frames for
// the aesthetic measure pipeline. Every frame must render without panicking.
func TestSnapshot_frames(t *testing.T) {
	t.Parallel()
	if os.Getenv("EITRI_SNAPSHOT") != "1" {
		t.Skip("set EITRI_SNAPSHOT=1 to render snapshot frames")
	}
	out := os.Getenv("EITRI_SNAPSHOT_DIR")
	if out == "" {
		out = ".auto/frames"
	}

	// ---- default dark theme session ----
	cfg := config.Config{
		Theme:           "dark",
		Provider:        "deepseek",
		Model:           "deepseek-v4-flash",
		ReasoningEffort: "high",
	}
	deps, _, _, _ := snapshotDeps(cfg)
	m := NewModelCfg(deps)
	m = resizeTo(t, m, 120, 40)
	writeFrame(t, out, "01_idle", m)

	// Seed the status strip + rail STATS with a lived-in session picture.
	m = upd(t, m, telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryTurn}})
	m = upd(t, m, telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryUsage, Hit: 12400, Miss: 3600, Output: 2100}})

	// ---- turn 1: reasoning + bash + edit, streamed, then finalized ----
	m = typeText(t, m, "Fix the flaky login test")
	m, _ = submitBusy(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./internal/auth/ -run TestLogin -count=1"}`)
	m = applyReasoningDelta(t, m, "The login test flakes when the mock clock ticks between password hashing and token minting. Freezing time at the start makes the issued-at claim deterministic, and deferring the unfreeze keeps the rest of the suite honest.")
	m = backdateTool(m, 0, 3200*time.Millisecond) // live timer on the pending bash
	writeFrame(t, out, "02_busy_reasoning", m)
	m = toolResult(t, m, ToolResult{
		Name: "bash", Result: "ok (2.1s)\n  PASS  TestLogin\n  2 tests passed", Lines: 3,
	})
	m = backdateTool(m, 0, 2100*time.Millisecond)
	m = toolStart(t, m, "edit", `{"path":"internal/auth/login_test.go"}`)
	m = applyDelta(t, m, "The flake came from a racy mock clock. I froze time before minting the token so the `issued-at` claim is deterministic.")
	m = applyDelta(t, m, "\n\n```go\nmock.Freeze()\ndefer mock.Unfreeze()\n```\n")
	m = applyDelta(t, m, "\n\nThe suite passes consistently now — ")
	writeFrame(t, out, "03_busy_stream", m)
	m = toolResult(t, m, ToolResult{
		Name: "edit", Path: "internal/auth/login_test.go",
		Result: "ok (42ms)\n  PASS  TestLogin\n  1 file changed", Lines: 4,
		Added: 3, Removed: 1, Before: loginBefore, After: loginAfter,
	})
	m = upd(t, m, turnDoneMsg{
		prompt: "Fix the flaky login test",
		answer: "The flake came from a racy mock clock. I froze time before minting the token so the `issued-at` claim is deterministic.\n\n```go\nmock.Freeze()\ndefer mock.Unfreeze()\n```\n\nThe suite passes consistently now, with the clock frozen only around the token mint.",
	})

	// ---- turn 2: reasoning + read + web_fetch failure, richer answer ----
	m = typeText(t, m, "Add retry with exponential backoff to the HTTP client")
	m, _ = submitBusy(t, m)
	m = toolStart(t, m, "read", `{"path":"internal/http/client.go","start_line":40,"end_line":90}`)
	m = toolResult(t, m, ToolResult{
		Name: "read", Path: "internal/http/client.go",
		Result: "ok (1ms)\n  50 lines", Lines: 1,
	})
	m = applyReasoningDelta(t, m, "A 429-safe retry needs jittered backoff so a thundering herd never re-collides. The client already centralizes requests in send(), so the retry loop belongs there with the circuit breaker state it already tracks.")
	m = toolStart(t, m, "web_fetch", `{"url":"https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Retry-After"}`)
	m = toolResult(t, m, ToolResult{
		Name: "web_fetch", Result: "error executing tool: fetch failed: DNS lookup failed for developer.mozilla.org", Lines: 1,
	})
	m = applyDelta(t, m, "I added a jittered exponential backoff to the client's `send()` path:\n\n- retry up to 3 attempts\n- base delay 250ms, doubling per attempt\n- ±20% jitter so concurrent clients don't re-collide\n- honors the `Retry-After` header when present\n\nThe fetch to the MDN docs failed (DNS), so the header honors the spec default instead.")
	m = upd(t, m, turnDoneMsg{
		prompt: "Add retry with exponential backoff to the HTTP client",
		answer: "I added a jittered exponential backoff to the client's `send()` path:\n\n- retry up to 3 attempts\n- base delay 250ms, doubling per attempt\n- ±20% jitter so concurrent clients don't re-collide\n- honors the `Retry-After` header when present\n\nThe MDN fetch failed on DNS, so I honored the spec default instead.",
	})
	m = upd(t, m, telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryTurn}})
	m = upd(t, m, telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryUsage, Hit: 8900, Miss: 1200, Output: 3400}})
	writeFrame(t, out, "04_chat", m)

	// ---- Ctrl+E expanded view: the edit card's inline diff in-flow ----
	m = keypress(t, m, "ctrl+e")
	writeFrame(t, out, "05_expanded_diff", m)
	m = keypress(t, m, "ctrl+e")

	// ---- settings surface ----
	m = keypress(t, m, "ctrl+s")
	writeFrame(t, out, "07_settings", m)
	m = keypress(t, m, "esc")

	// ---- wide window: right rail auto-shows ----
	m = resizeTo(t, m, 150, 42)
	writeFrame(t, out, "08_wide_rail", m)

	// ---- light theme session (second model instance) ----
	lm := scriptedChat(t, config.Config{
		Theme: "light", Provider: "deepseek", Model: "deepseek-v4-flash", ReasoningEffort: "low",
	}, 130, 40)
	lm = typeText(t, lm, "hello")
	writeFrame(t, out, "09_light_rail", lm)

	// ---- alt-theme sessions: chrome palette + markdown tint coherence ----
	for _, theme := range []string{"nord", "dracula", "solarized", "dark-daltonized"} {
		tm := scriptedChat(t, config.Config{
			Theme: theme, Provider: "deepseek", Model: "deepseek-v4-flash", ReasoningEffort: "high",
		}, 130, 40)
		writeFrame(t, out, "10_"+theme, tm)
	}

	// ---- slash-command completion list ----
	sm := scriptedChat(t, config.Config{
		Theme: "dark", Provider: "deepseek", Model: "deepseek-v4-flash", ReasoningEffort: "high",
	}, 120, 40)
	sm = typeText(t, sm, "/ref")
	writeFrame(t, out, "11_slash", sm)

	// ---- max-turns continuation prompt ----
	cm := scriptedChat(t, config.Config{
		Theme: "dark", Provider: "deepseek", Model: "deepseek-v4-flash", ReasoningEffort: "high",
	}, 120, 40)
	cm.continueReq <- struct{}{}
	cm = keypress(t, cm, "x") // any key drains the request and flips to prompting
	writeFrame(t, out, "12_continue", cm)

	// ---- expanded tool result card (Ctrl+E expanded view) ----
	ex := scriptedChat(t, config.Config{
		Theme: "dark", Provider: "deepseek", Model: "deepseek-v4-flash", ReasoningEffort: "high",
	}, 120, 40)
	ex = upd(t, ex, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}) // Ctrl+E: expanded view mode on
	writeFrame(t, out, "13_expanded", ex)

	_ = context.Background // keep the import honest
}

// scriptedChat builds a lived-in two-turn session (reasoning + tool calls +
// answers, seeded telemetry) at the given size, ready for frame capture. It is
// the shared transcript used by every theme frame so themes compare fairly.
func scriptedChat(t *testing.T, cfg config.Config, w, h int) Model {
	t.Helper()
	deps, _, _, _ := snapshotDeps(cfg)
	m := NewModelCfg(deps)
	m = resizeTo(t, m, w, h)

	m = upd(t, m, telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryTurn}})
	m = upd(t, m, telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryUsage, Hit: 12400, Miss: 3600, Output: 2100}})

	// turn 1: reasoning + bash + edit, finalized.
	m = typeText(t, m, "Fix the flaky login test")
	m, _ = submitBusy(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./internal/auth/ -run TestLogin -count=1"}`)
	m = applyReasoningDelta(t, m, "The login test flakes when the mock clock ticks between password hashing and token minting. Freezing time at the start makes the issued-at claim deterministic.")
	m = toolResult(t, m, ToolResult{
		Name: "bash", Result: "ok (2.1s)\n  PASS  TestLogin\n  2 tests passed", Lines: 3,
	})
	m = backdateTool(m, 0, 2100*time.Millisecond)
	m = toolStart(t, m, "edit", `{"path":"internal/auth/login_test.go"}`)
	m = applyDelta(t, m, "The flake came from a racy mock clock. I froze time before minting the token so the `issued-at` claim is deterministic.")
	m = applyDelta(t, m, "\n\n```go\nmock.Freeze()\ndefer mock.Unfreeze()\n```\n")
	m = applyDelta(t, m, "\n\nThe suite passes consistently now — ")
	m = toolResult(t, m, ToolResult{
		Name: "edit", Path: "internal/auth/login_test.go",
		Result: "ok (42ms)\n  PASS  TestLogin\n  1 file changed", Lines: 4,
		Added: 3, Removed: 1, Before: loginBefore, After: loginAfter,
	})
	m = upd(t, m, turnDoneMsg{
		prompt: "Fix the flaky login test",
		answer: "The flake came from a racy mock clock. I froze time before minting the token so the `issued-at` claim is deterministic.\n\n```go\nmock.Freeze()\ndefer mock.Unfreeze()\n```\n\nThe suite passes consistently now, with the clock frozen only around the token mint.",
	})

	// turn 2: reasoning + read + web_fetch failure.
	m = typeText(t, m, "Add retry with exponential backoff to the HTTP client")
	m, _ = submitBusy(t, m)
	m = toolStart(t, m, "read", `{"path":"internal/http/client.go","start_line":40,"end_line":90}`)
	m = toolResult(t, m, ToolResult{
		Name: "read", Path: "internal/http/client.go",
		Result: "ok (1ms)\n  50 lines", Lines: 1,
	})
	m = applyReasoningDelta(t, m, "A 429-safe retry needs jittered backoff so a thundering herd never re-collides. The client already centralizes requests in send(), so the retry loop belongs there.")
	m = toolStart(t, m, "web_fetch", `{"url":"https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Retry-After"}`)
	m = toolResult(t, m, ToolResult{
		Name: "web_fetch", Result: "error executing tool: fetch failed: DNS lookup failed for developer.mozilla.org", Lines: 1,
	})
	m = applyDelta(t, m, "I added a jittered exponential backoff to the client's `send()` path:\n\n- retry up to 3 attempts\n- base delay 250ms, doubling per attempt\n- ±20% jitter so concurrent clients don't re-collide\n- honors the `Retry-After` header when present\n\nThe fetch to the MDN docs failed (DNS), so the header honors the spec default instead.")
	m = upd(t, m, turnDoneMsg{
		prompt: "Add retry with exponential backoff to the HTTP client",
		answer: "I added a jittered exponential backoff to the client's `send()` path:\n\n- retry up to 3 attempts\n- base delay 250ms, doubling per attempt\n- ±20% jitter so concurrent clients don't re-collide\n- honors the `Retry-After` header when present\n\nThe MDN fetch failed on DNS, so I honored the spec default instead.",
	})
	m = upd(t, m, telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryTurn}})
	m = upd(t, m, telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryUsage, Hit: 8900, Miss: 1200, Output: 3400}})
	return m
}

// TestSnapshot_narrow audits the narrow-terminal surface (80x24, no rail —
// auto-hidden below 120): strip collapse, bubble wrapping, band fit.
func TestSnapshot_narrow(t *testing.T) {
	t.Parallel()
	if os.Getenv("EITRI_SNAPSHOT") != "1" {
		t.Skip("set EITRI_SNAPSHOT=1 to render snapshot frames")
	}
	out := os.Getenv("EITRI_SNAPSHOT_DIR")
	if out == "" {
		out = ".auto/frames"
	}
	m := scriptedChat(t, config.Config{
		Theme: "dark", Provider: "deepseek", Model: "deepseek-v4-flash", ReasoningEffort: "high",
	}, 80, 24)
	writeFrame(t, out, "14_narrow", m)
}
