package tui

import (
	"strings"
	"testing"
)

// This test locks the deepened transcript invariant (issue #537) end to end:
// a full turn driven through the TurnSession verbs and Fold, followed by
// non-turn message appends and a resize, must leave the row->tool-entry /
// row->message hit-test correct — rebuilt lazily by ensureLayout alone, with
// no test or caller code writing the dirty flag anywhere in the path.
//
// Each mutation phase is followed by a hit-test query that drains the stale
// flag, so every later phase's internal invalidation is load-bearing: drop
// any one dirty-marking and the corresponding assertion fails.
func TestMutationsKeepHitTestCorrectWithoutCallerInvalidation(t *testing.T) {
	s := NewTurnSession(stubTurn("final answer", nil))
	tx := newTestTx()
	tx.SetSize(100, 30)

	// rebuild queries the hit-test seam once and asserts the lazy rebuild
	// happened exactly then, returning the plain rendered rows.
	rebuild := func(when string) []string {
		builds := tx.layout.builds
		plain := tx.plainLines()
		if got := tx.layout.builds - builds; got != 1 {
			t.Fatalf("%s: layout builds = %d, want exactly 1", when, got)
		}
		return plain
	}
	findLine := func(plain []string, marker string) int {
		for i, line := range plain {
			if strings.Contains(line, marker) {
				return i
			}
		}
		t.Fatalf("marker %q not found in rendered history:\n%s", marker, strings.Join(plain, "\n"))
		return -1
	}

	// Phase 1: Begin appends the user prompt.
	cmd := s.Begin(&tx, "run the thing", "")
	plain := rebuild("after Begin")
	userLine := findLine(plain, "run the thing")
	if idx, ok := tx.messageAtLine(userLine); !ok || idx != 0 {
		t.Errorf("after Begin: messageAtLine(%d) = (%d,%v), want message 0", userLine, idx, ok)
	}

	// Phase 2: Fold streams the answer and lands a tool observation while
	// the turn runs.
	f := NewFold(s)
	f.Stream(&tx, AnswerStream, "final answer")
	f.Tool(&tx, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	f.Tool(&tx, ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2}})
	plain = rebuild("after Fold")
	answerLine := findLine(plain, "final")
	if idx, ok := tx.messageAtLine(answerLine); !ok || idx != 1 {
		t.Errorf("after Fold: messageAtLine(%d) = (%d,%v), want streaming message 1", answerLine, idx, ok)
	}
	toolLine := findLine(plain, "bash")
	if idx, _, ok := tx.toolEntryAtLine(toolLine); !ok || idx != 0 {
		t.Errorf("after Fold: toolEntryAtLine(%d) = (%d,%v), want tool entry 0", toolLine, idx, ok)
	}

	// Phase 3: Commit finalizes the streamed assistant message.
	msg := cmd().(turnDoneMsg)
	if _, err := s.Commit(&tx, msg); err != nil {
		t.Fatalf("Commit returned err %v", err)
	}
	plain = rebuild("after Commit")
	answerLine = findLine(plain, "final answer")
	if idx, ok := tx.messageAtLine(answerLine); !ok || idx != 1 {
		t.Errorf("after Commit: messageAtLine(%d) = (%d,%v), want assistant message 1", answerLine, idx, ok)
	}
	// Phase 4: mutate outside any turn — two appends plus a resize — and
	// check the whole mapping survives with indexes shifted by the appends.
	tx.appendUserMsg("help me")
	tx.appendMsg("a standalone note")
	tx.SetSize(80, 30)
	plain = rebuild("after appends+resize")

	cases := []struct {
		marker string
		line   int
		want   int
	}{
		{"run the thing", findLine(plain, "run the thing"), 0},
		{"final answer", findLine(plain, "final answer"), 1},
		{"help me", findLine(plain, "help me"), 2},
		{"a standalone note", findLine(plain, "a standalone note"), 3},
	}
	for _, c := range cases {
		if idx, ok := tx.messageAtLine(c.line); !ok || idx != c.want {
			t.Errorf("after appends+resize: messageAtLine(%d) for %q = (%d,%v), want message %d", c.line, c.marker, idx, ok, c.want)
		}
	}
	toolLine = findLine(plain, "bash")
	if idx, _, ok := tx.toolEntryAtLine(toolLine); !ok || idx != 0 {
		t.Errorf("after appends+resize: toolEntryAtLine(%d) = (%d,%v), want tool entry 0", toolLine, idx, ok)
	}

	// Repeat queries serve from the cache: no further rebuilds.
	_, _ = tx.messageAtLine(answerLine)
	_, _, _ = tx.toolEntryAtLine(toolLine)
	if tx.layout.builds != 4 {
		t.Errorf("layout builds = %d after repeat queries, want 4 (cache must survive until next mutation)", tx.layout.builds)
	}
}
