package tui

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

// wordToken is one token of a word-diffed line: its text and whether it changed between the old and new lines.
type wordToken struct {
	text    string
	changed bool
}

// wordTokRe splits a line into maximal runs of identifier characters ([A-Za-z0-9_]) and everything else (punctuation, whitespace), so " foo(x)" becomes [" ", "foo", "(", "x", ")"] and a single renamed identifier highlights without touching its surrounding punctuation.
var wordTokRe = regexp.MustCompile(`[A-Za-z0-9_]+|[^A-Za-z0-9_]+`)

func tokenize(s string) []string {
	if s == "" {
		return nil
	}
	return wordTokRe.FindAllString(s, -1)
}

// wordDiff marks the changed tokens between two lines using a token-level LCS: tokens on the LCS path are unchanged in both lines; every other token is flagged changed.
func wordDiff(a, b string) (aToks, bToks []wordToken) {
	at := tokenize(a)
	bt := tokenize(b)
	n, m := len(at), len(bt)

	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if at[i] == bt[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	i, j := 0, 0
	for i < n && j < m {
		switch {
		case at[i] == bt[j]:
			aToks = append(aToks, wordToken{at[i], false})
			bToks = append(bToks, wordToken{bt[j], false})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			aToks = append(aToks, wordToken{at[i], true})
			i++
		default:
			bToks = append(bToks, wordToken{bt[j], true})
			j++
		}
	}
	for ; i < n; i++ {
		aToks = append(aToks, wordToken{at[i], true})
	}
	for ; j < m; j++ {
		bToks = append(bToks, wordToken{bt[j], true})
	}
	return aToks, bToks
}

// renderWordDiff renders a paired old/new line with word-level emphasis: the line style (fill + hue) for unchanged tokens, bold for the changed ones.
func renderWordDiff(toks []wordToken, base lipgloss.Style, prefix string) string {
	var sb strings.Builder
	for i, t := range toks {
		if i == 0 {
			if t.changed {
				sb.WriteString(base.Bold(true).Render(prefix + t.text))
			} else {
				sb.WriteString(base.Render(prefix + t.text))
			}
			continue
		}
		if t.changed {
			sb.WriteString(base.Bold(true).Render(t.text))
		} else {
			sb.WriteString(base.Render(t.text))
		}
	}
	if len(toks) == 0 {
		sb.WriteString(prefix) // empty changed line: bare prefix
	}
	return sb.String()
}
