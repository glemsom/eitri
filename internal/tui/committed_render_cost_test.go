package tui

import (
	"strconv"
	"testing"
)

// buildCommittedTurns appends n settled turns directly and materializes them
// into the committed render memo, so a transcript already holds n committed
// turns when a test measures the marginal cost of committing one more. Building
// by direct append (rather than replaying n full streaming commits) keeps the
// N=1000 sweep fast enough for a normal test run; the memo state it produces is
// byte-identical to n real commits because both funnel through the single
// committedEmission emitter.
func buildCommittedTurns(tx *Transcript, n int) {
	for i := 0; i < n; i++ {
		tx.messages = append(tx.messages, message{role: "you", content: "a prompt"})
		tx.messages = append(tx.messages, message{role: "eitri", content: "an answer", events: synthAnswerLog("an answer")})
	}
	tx.ensureCommittedUnits(len(tx.messages))
}

// TestCommittedCommitCostFlatInHistorySize is the T4 size-sweep regression
// guard: it builds N committed turns and measures the marginal cost of
// committing one more. Cost is the number of settled messages the committed
// memo re-renders for that commit; the memo must serve every prior unit and
// render only the new turn's prompt + answer, so the marginal cost is exactly 2
// regardless of N. A change that re-derives prior units on commit makes the
// marginal cost exceed 2, and the excess grows with N — so the 1000-turn case
// flags a regression the small fixtures miss.
//
// Run: go test ./internal/tui -run TestCommittedCommitCostFlatInHistorySize
func TestCommittedCommitCostFlatInHistorySize(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	for _, n := range []int{10, 100, 1000} {
		t.Run("N="+strconv.Itoa(n), func(t *testing.T) {
			tx := memoTestTx()
			buildCommittedTurns(tx, n)
			base := tx.committedUnitRenders

			commitTurn(t, tx, "one more", "answer")

			if got := tx.committedUnitRenders - base; got != 2 {
				t.Fatalf("committing one turn on top of %d prior committed turns re-rendered %d committed units, want exactly 2 (its own prompt + answer): per-turn commit cost grew with history size", n, got)
			}
		})
	}
}

// BenchmarkCommittedCommitCost_FlatInHistory is the T4 perf surface: it measures
// the marginal cost of committing one turn on top of N prior committed turns
// across an order-of-magnitude sweep. The memo renders only the new turn's two
// units, so per-commit work stays flat (zero extra allocs) as N grows; a
// regression that re-renders prior history makes both allocs and time grow with
// N.
//
// "Flat" means the marginal commit cost at N=1000 lands near the N=10 cost —
// not growing with prior history. The committedUnitRenders counter in the size-
// sweep test above is the exact deterministic threshold (2 units per commit);
// this benchmark is the wall-clock/allocation surface that catches the same
// regression empirically.
//
// Run: go test ./internal/tui -run xxx -bench BenchmarkCommittedCommitCost -benchmem -benchtime 30x
func BenchmarkCommittedCommitCost_FlatInHistory(b *testing.B) {
	b.Setenv("EITRI_ASCII_GLYPHS", "1")
	for _, n := range []int{10, 100, 1000} {
		b.Run("N="+strconv.Itoa(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				tx := memoTestTx()
				buildCommittedTurns(tx, n)
				b.ResetTimer()
				tx.messages = append(tx.messages, message{role: "you", content: "one more"})
				tx.messages = append(tx.messages, message{role: "eitri", content: "answer", events: synthAnswerLog("answer")})
				tx.ensureCommittedUnits(len(tx.messages))
				b.StopTimer()
			}
		})
	}
}
