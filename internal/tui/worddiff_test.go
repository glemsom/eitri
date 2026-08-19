package tui

import (
	"strings"
	"testing"
)

// TestWordDiff_marksChangedTokens asserts wordDiff flags exactly the tokens
// that differ between two lines, keeping shared words and punctuation
// unchanged (benchmark §4.2 word-level diff emphasis).
func TestWordDiff_marksChangedTokens(t *testing.T) {
	t.Parallel()
	oldToks, newToks := wordDiff("func start(port int) error {", "func start(port uint16) error {")
	oldJoin, newJoin := "", ""
	for _, tok := range oldToks {
		oldJoin += tok.text
		if !tok.changed && tok.text == "int" {
			t.Errorf("'int' must be marked changed in the old line")
		}
	}
	for _, tok := range newToks {
		newJoin += tok.text
	}
	if oldJoin != "func start(port int) error {" || newJoin != "func start(port uint16) error {" {
		t.Fatalf("wordDiff joined output = %q / %q, want the original lines", oldJoin, newJoin)
	}
	// The shared words must be unchanged in both.
	for _, toks := range [][]wordToken{oldToks, newToks} {
		for _, tok := range toks {
			if (tok.text == "func" || tok.text == "start" || tok.text == "port" || tok.text == "error") && tok.changed {
				t.Errorf("shared word %q must be unchanged, got changed", tok.text)
			}
		}
	}
	changedOld := changedText(oldToks)
	changedNew := changedText(newToks)
	if changedOld != "int" || changedNew != "uint16" {
		t.Errorf("changed tokens = %q / %q, want int / uint16", changedOld, changedNew)
	}
}

func changedText(toks []wordToken) string {
	var sb strings.Builder
	for _, tok := range toks {
		if tok.changed {
			sb.WriteString(tok.text)
		}
	}
	return sb.String()
}

// TestRenderWordDiff_prefixContiguous asserts the +/- prefix folds into the
// first styled token so the rendered text stays contiguous (+world, not
// +<escape>world) and the changed token carries bold emphasis (SGR 1).
func TestRenderWordDiff_prefixContiguous(t *testing.T) {
	t.Parallel()
	_, newToks := wordDiff("a=b", "a==b")
	rendered := renderWordDiff(newToks, defaultTheme.diffAddStyle, "+")
	if !strings.Contains(rendered, "+") {
		t.Fatalf("prefix missing, got %q", rendered)
	}
	// "+" must be immediately followed by the styled text, not an escape.
	if idx := strings.Index(rendered, "+"); idx >= 0 {
		if rendered[idx+1] == '\x1b' {
			t.Errorf("prefix must fold into the first styled token, got %q", rendered)
		}
	}
	if !strings.Contains(rendered, "\x1b[1;") && !hasSGRBold(rendered) {
		t.Errorf("changed tokens must render bold, got %q", rendered)
	}
}
