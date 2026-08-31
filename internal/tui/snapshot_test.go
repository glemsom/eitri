package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

func newSnapshotRail() *Rail {
	r := NewRail("deepseek", "deepseek-v4-flash", "high", true, "eitri-9f3a", "/tmp/eitri-9f3a")
	r.SetBranch("main")
	return r
}

func snapshotDeps(cfg config.Config) (Dependencies, *Telemetry) {
	te := NewTelemetry("deepseek-v4-flash", "high", true, 10)
	return Dependencies{
		Turn:          streamingTurn,
		WorkspacePath: "/home/dev/acme",
		Config:        cfg,
		Models:        []string{"deepseek-v4-flash", "deepseek-v3", "deepseek-v3-0324"},
		Telemetry:     te,
		Events:        NewEventFeed(),
		Rail:          newSnapshotRail(),
		Skills:        &SkillsSurface{Items: []SkillItem{{Name: "rust-review"}, {Name: "refactor"}}},
	}, te
}

func upd(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	nm, _ := m.Update(msg)
	return asModel(t, nm)
}

func toolStart(t *testing.T, m Model, name, args string) Model {
	t.Helper()
	return upd(t, m, eventMsg{update: Event{Tool: &ToolUpdate{Start: &ToolStart{Name: name, Args: args}}}})
}

func toolResult(t *testing.T, m Model, r ToolResult) Model {
	t.Helper()
	return upd(t, m, eventMsg{update: Event{Tool: &ToolUpdate{Result: &r}}})
}

func backdateTool(m Model, idx int, d time.Duration) Model {
	m.tx.log.SetStart(idx, time.Now().Add(-d))
	return m
}

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

func TestSnapshot_frames(t *testing.T) {
	t.Parallel()
	if os.Getenv("EITRI_SNAPSHOT") != "1" {
		t.Skip("set EITRI_SNAPSHOT=1 to render snapshot frames")
	}
	out := os.Getenv("EITRI_SNAPSHOT_DIR")
	if out == "" {
		out = ".auto/frames"
	}

	cfg := config.Config{
		Theme:           "dark",
		Provider:        "deepseek",
		Model:           "deepseek-v4-flash",
		ReasoningEffort: "high",
	}
	deps, _ := snapshotDeps(cfg)
	m := NewModelCfg(deps)
	m = resizeTo(t, m, 120, 40)
	writeFrame(t, out, "01_idle", m)

	m = upd(t, m, telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryTurn}})
	m = upd(t, m, telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryUsage, Hit: 12400, Miss: 3600, Output: 2100}})

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
	m = toolStart(t, m, "bash", `{"command":"go test ./internal/auth/ -run TestLogin -count=1"}`)
	m = applyDelta(t, m, "The flake came from a racy mock clock. I froze time before minting the token so the `issued-at` claim is deterministic.")
	m = applyDelta(t, m, "\n\n```go\nmock.Freeze()\ndefer mock.Unfreeze()\n```\n")
	m = applyDelta(t, m, "\n\nThe suite passes consistently now — ")
	writeFrame(t, out, "03_busy_stream", m)
	m = toolResult(t, m, ToolResult{
		Name:   "bash",
		Result: "ok (42ms)\n  PASS  TestLogin\n  1 file changed", Lines: 4,
	})
	m = upd(t, m, turnDoneMsg{
		prompt: "Fix the flaky login test",
		answer: "The flake came from a racy mock clock. I froze time before minting the token so the `issued-at` claim is deterministic.\n\n```go\nmock.Freeze()\ndefer mock.Unfreeze()\n```\n\nThe suite passes consistently now, with the clock frozen only around the token mint.",
	})

	m = typeText(t, m, "Add retry with exponential backoff to the HTTP client")
	m, _ = submitBusy(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	m = toolResult(t, m, ToolResult{
		Name:   "bash",
		Result: "ok (1ms)\n  2 tests passed", Lines: 2,
	})
	m = applyReasoningDelta(t, m, "A 429-safe retry needs jittered backoff so a thundering herd never re-collides. The client already centralizes requests in send(), so the retry loop belongs there with the circuit breaker state it already tracks.")
	m = toolStart(t, m, "bash", `{"command":"curl --fail --max-time 30 https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Retry-After | lynx -dump -nolist -stdin"}`)
	m = toolResult(t, m, ToolResult{
		Name: "bash", Result: "error executing tool: fetch failed: DNS lookup failed for developer.mozilla.org", Lines: 1,
	})
	m = applyDelta(t, m, "I added a jittered exponential backoff to the client's `send()` path:\n\n- retry up to 3 attempts\n- base delay 250ms, doubling per attempt\n- ±20% jitter so concurrent clients don't re-collide\n- honors the `Retry-After` header when present\n\nThe fetch to the MDN docs failed (DNS), so the header honors the spec default instead.")
	m = upd(t, m, turnDoneMsg{
		prompt: "Add retry with exponential backoff to the HTTP client",
		answer: "I added a jittered exponential backoff to the client's `send()` path:\n\n- retry up to 3 attempts\n- base delay 250ms, doubling per attempt\n- ±20% jitter so concurrent clients don't re-collide\n- honors the `Retry-After` header when present\n\nThe MDN fetch failed on DNS, so I honored the spec default instead.",
	})
	m = upd(t, m, telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryTurn}})
	m = upd(t, m, telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryUsage, Hit: 8900, Miss: 1200, Output: 3400}})
	writeFrame(t, out, "04_chat", m)

	m = keypress(t, m, "ctrl+e")
	writeFrame(t, out, "05_expanded", m)
	m = keypress(t, m, "ctrl+e")

	m = keypress(t, m, "ctrl+s")
	writeFrame(t, out, "07_settings", m)
	m = keypress(t, m, "esc")

	m = resizeTo(t, m, 150, 42)
	writeFrame(t, out, "08_wide_rail", m)

	lm := scriptedChat(t, config.Config{
		Theme: "light", Provider: "deepseek", Model: "deepseek-v4-flash", ReasoningEffort: "low",
	}, 130, 40)
	lm = typeText(t, lm, "hello")
	writeFrame(t, out, "09_light_rail", lm)

	for _, theme := range []string{"nord", "dracula", "solarized", "dark-daltonized"} {
		tm := scriptedChat(t, config.Config{
			Theme: theme, Provider: "deepseek", Model: "deepseek-v4-flash", ReasoningEffort: "high",
		}, 130, 40)
		writeFrame(t, out, "10_"+theme, tm)
	}

	sm := scriptedChat(t, config.Config{
		Theme: "dark", Provider: "deepseek", Model: "deepseek-v4-flash", ReasoningEffort: "high",
	}, 120, 40)
	sm = typeText(t, sm, "/ref")
	writeFrame(t, out, "11_slash", sm)

	cm := scriptedChat(t, config.Config{
		Theme: "dark", Provider: "deepseek", Model: "deepseek-v4-flash", ReasoningEffort: "high",
	}, 120, 40)
	cm.continueReq <- struct{}{}
	cm = keypress(t, cm, "x") // any key drains the request and flips to prompting
	writeFrame(t, out, "12_continue", cm)

	ex := scriptedChat(t, config.Config{
		Theme: "dark", Provider: "deepseek", Model: "deepseek-v4-flash", ReasoningEffort: "high",
	}, 120, 40)
	ex = upd(t, ex, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}) // Ctrl+E: expanded view mode on
	writeFrame(t, out, "13_expanded", ex)

	_ = context.Background // keep the import honest

	// flow — the empty pre-stream gap while busy, and the instant-error turn.
	gm := scriptedChat(t, config.Config{
		Theme: "dark", Provider: "deepseek", Model: "deepseek-v4-flash", ReasoningEffort: "high",
	}, 120, 40)
	gm = typeText(t, gm, "Summarize the diff")
	gm, _ = submitBusy(t, gm)
	writeFrame(t, out, "14_busy_empty_gap", gm)
	gm = upd(t, gm, turnDoneMsg{prompt: "Summarize the diff", err: errors.New("no login flow available")})
	writeFrame(t, out, "15_instant_error", gm)
}

func scriptedChat(t *testing.T, cfg config.Config, w, h int) Model {
	t.Helper()
	deps, _ := snapshotDeps(cfg)
	m := NewModelCfg(deps)
	m = resizeTo(t, m, w, h)

	m = upd(t, m, telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryTurn}})
	m = upd(t, m, telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryUsage, Hit: 12400, Miss: 3600, Output: 2100}})

	m = typeText(t, m, "Fix the flaky login test")
	m, _ = submitBusy(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./internal/auth/ -run TestLogin -count=1"}`)
	m = applyReasoningDelta(t, m, "The login test flakes when the mock clock ticks between password hashing and token minting. Freezing time at the start makes the issued-at claim deterministic.")
	m = toolResult(t, m, ToolResult{
		Name: "bash", Result: "ok (2.1s)\n  PASS  TestLogin\n  2 tests passed", Lines: 3,
	})
	m = backdateTool(m, 0, 2100*time.Millisecond)
	m = toolStart(t, m, "bash", `{"command":"go test ./internal/auth/ -run TestLogin -count=1"}`)
	m = applyDelta(t, m, "The flake came from a racy mock clock. I froze time before minting the token so the `issued-at` claim is deterministic.")
	m = applyDelta(t, m, "\n\n```go\nmock.Freeze()\ndefer mock.Unfreeze()\n```\n")
	m = applyDelta(t, m, "\n\nThe suite passes consistently now — ")
	m = toolResult(t, m, ToolResult{
		Name:   "bash",
		Result: "ok (42ms)\n  PASS  TestLogin\n  1 file changed", Lines: 4,
	})
	m = upd(t, m, turnDoneMsg{
		prompt: "Fix the flaky login test",
		answer: "The flake came from a racy mock clock. I froze time before minting the token so the `issued-at` claim is deterministic.\n\n```go\nmock.Freeze()\ndefer mock.Unfreeze()\n```\n\nThe suite passes consistently now, with the clock frozen only around the token mint.",
	})

	m = typeText(t, m, "Add retry with exponential backoff to the HTTP client")
	m, _ = submitBusy(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	m = toolResult(t, m, ToolResult{
		Name:   "bash",
		Result: "ok (1ms)\n  PASS  TestHTTPClient\n  50 lines", Lines: 2,
	})
	m = applyReasoningDelta(t, m, "A 429-safe retry needs jittered backoff so a thundering herd never re-collides. The client already centralizes requests in send(), so the retry loop belongs there.")
	m = toolStart(t, m, "bash", `{"command":"curl --fail --max-time 30 https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Retry-After | lynx -dump -nolist -stdin"}`)
	m = toolResult(t, m, ToolResult{
		Name: "bash", Result: "error executing tool: fetch failed: DNS lookup failed for developer.mozilla.org", Lines: 1,
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
