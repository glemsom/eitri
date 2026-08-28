package tui

// T6 integration verification (issue #614, part of #608): the arrows-across-
// `/new`, restart-persistence, fresh-context, and rail-identity behaviors are
// each covered individually by the T2/T3/T4/T5 seams, but these tests verify
// the wiring works as one integrated whole.

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// seqGUID mints sequential session GUIDs so each test can tell re-mints apart.
type seqGUID struct{ n int32 }

func (s *seqGUID) next() string {
	n := atomic.AddInt32(&s.n, 1)
	return "fresh-" + itoa(int(n))
}

// TestT6ArrowsAcrossNew recalls submitted prompts both before and after a `/new`
// confirm: the history ring must survive the session re-mint because it lives on
// the Model, not the transcript or session (T2/T3 + T5).
func TestT6ArrowsAcrossNew(t *testing.T) {
	t.Parallel()
	live := NewLiveSessionKey("old")
	guid := &seqGUID{}
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, _ string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		LiveKey: live,
		NewGUID: guid.next,
	})
	m = resize(t, m)
	m = pushHistory(t, m, "alpha", "beta")

	if live.Get() != "old" {
		t.Fatalf("live key changed before `/new`, got %q", live.Get())
	}

	// Confirm `/new`: re-mints the session, keeps the transcript empty.
	m = typeText(t, m, "/new")
	m = keypress(t, m, "enter")
	m = keypress(t, m, "y")
	if live.Get() != "fresh-1" {
		t.Fatalf("`/new` confirm did not re-mint the live key, got %q", live.Get())
	}

	// Arrows must still recall the pre-`/new` prompts after the re-mint.
	m = keypress(t, m, "up")
	if got := m.composer.Value(); got != "beta" {
		t.Fatalf("up after `/new` should recall newest prompt, got %q (history=%v)", got, m.history.Entries())
	}
	m = keypress(t, m, "up")
	if got := m.composer.Value(); got != "alpha" {
		t.Fatalf("second up after `/new` should walk to the older prompt, got %q (history=%v)", got, m.history.Entries())
	}

	// A fresh prompt appended after `/new` is prepended for recall too. The
	// recalled "alpha" draft is cleared first so the new draft types cleanly.
	m.composer.Reset()
	m = typeText(t, m, "gamma")
	m = submitAndWait(t, m)
	m = keypress(t, m, "up")
	if got := m.composer.Value(); got != "gamma" {
		t.Fatalf("up after post-`/new` submit should recall the newest prompt, got %q (history=%v)", got, m.history.Entries())
	}
}

// TestT6PersistAcrossRestart verifies the full Model round-trips prompt history
// through a file-backed ring across a "restart" (a fresh Model on the same
// HistoryPath), and the recalled entries survive a later `/new` re-mint.
func TestT6PersistAcrossRestart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "prompt_history.json")
	mk := func(live *LiveSessionKey) Model {
		m := NewModelCfg(Dependencies{
			Turn: func(_ context.Context, _ string, _ string) (TurnResult, error) {
				return TurnResult{Answer: "ok"}, nil
			},
			HistoryPath: path,
			LiveKey:     live,
			NewGUID:     func() string { return "fresh" },
		})
		m = resize(t, m)
		return m
	}

	m := mk(NewLiveSessionKey("old"))
	m = typeText(t, m, "persisted prompt")
	m = submitAndWait(t, m)

	// "Restart": a fresh program on the same data dir loads the persisted ring.
	reopened := mk(NewLiveSessionKey("old"))
	if got := reopened.history.Entries(); !equalStrings(got, []string{"persisted prompt"}) {
		t.Fatalf("restart-load history = %v, want [persisted prompt]", got)
	}
	reopened = typeText(t, reopened, "/new")
	reopened = keypress(t, reopened, "enter")
	reopened = keypress(t, reopened, "y")
	reopened = keypress(t, reopened, "up")
	if got := reopened.composer.Value(); got != "persisted prompt" {
		t.Fatalf("recall of restarted history after `/new` = %q, want persisted prompt", got)
	}
}

// TestT6NewYieldsFreshEngineContext verifies the engine-turn seam observes a
// clean session: each submitted turn reads the current session key, so a turn
// before `/new` and the next turn after confirm use different keys — fresh
// engine session history on the new key (T1 + T5).
func TestT6NewYieldsFreshEngineContext(t *testing.T) {
	t.Parallel()
	live := NewLiveSessionKey("old")
	var seen []string
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, _ string, _ string) (TurnResult, error) {
			seen = append(seen, live.Get())
			return TurnResult{Answer: "ok"}, nil
		},
		LiveKey: live,
		NewGUID: func() string { return "fresh" },
	})
	m = resize(t, m)
	m = typeText(t, m, "first turn")
	m = submitAndWait(t, m)

	m = typeText(t, m, "/new")
	m = keypress(t, m, "enter")
	m = keypress(t, m, "y")
	m = typeText(t, m, "next turn")
	m = submitAndWait(t, m)

	if len(seen) != 2 || seen[0] != "old" || seen[1] != "fresh" {
		t.Fatalf("turn seam saw session keys %v, want [old fresh]", seen)
	}
}

// TestT6RailIdentityAfterNew verifies the on-screen rail CONTEXT session id
// reflects the re-minted live key after a `/new` confirm (T1 rail seam + T5).
func TestT6RailIdentityAfterNew(t *testing.T) {
	t.Parallel()
	live := NewLiveSessionKey("old")
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, _ string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		LiveKey: live,
		NewGUID: func() string { return "fresh" },
		Rail:    NewRail("opencode-go", "deepseek-v4-flash", "low", true, "old", "/tmp/old"),
	})
	// The app wires the shared live key into the rail (app/tui.go); tests must
	// mirror that wiring for the CONTEXT session id to stay live across `/new`.
	m.tx.rail.SetLiveKey(live)
	m = resize(t, m)
	if v := view(m); !strings.Contains(v, "session old") {
		t.Fatalf("rail CONTEXT missing pre-`/new` session id, got: %q", v)
	}

	m = typeText(t, m, "/new")
	m = keypress(t, m, "enter")
	m = keypress(t, m, "y")

	v := view(m)
	if !strings.Contains(v, "session fresh") {
		t.Errorf("rail CONTEXT did not refresh to fresh session id after `/new`, got: %q", v)
	}
	if strings.Contains(v, "session old") {
		t.Errorf("rail CONTEXT still shows stale session id after `/new`, got: %q", v)
	}
}

// itoa renders a small non-negative int for sequential GUID synthesis.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
