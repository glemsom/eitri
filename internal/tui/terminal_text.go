package tui

import (
	"strings"
	"unicode/utf8"
)

// sanitizeTerminalText removes terminal controls from untrusted text before a
// renderer can combine it with its own trusted styling. Newlines and tabs are
// retained so displayed text keeps its ordinary layout.
func sanitizeTerminalText(s string) string {
	if !strings.ContainsAny(s, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f\x7f\u0080\u0081\u0082\u0083\u0084\u0085\u0086\u0087\u0088\u0089\u008a\u008b\u008c\u008d\u008e\u008f\u0090\u0091\u0092\u0093\u0094\u0095\u0096\u0097\u0098\u0099\u009a\u009b\u009c\u009d\u009e\u009f") {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] >= 0x80 && s[i] <= 0x9f {
			switch s[i] {
			case 0x9b:
				i = skipCSI(s, i+1)
			case 0x9d, 0x90, 0x9f, 0x9e, 0x98:
				i = skipStringControl(s, i+1)
			default:
				i++
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == 0x1b:
			i = skipEscape(s, i+size)
		case r == 0x9b:
			i = skipCSI(s, i+size)
		case r == 0x9d || r == 0x90 || r == 0x9f || r == 0x9e || r == 0x98:
			i = skipStringControl(s, i+size)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			i += size
		default:
			out.WriteRune(r)
			i += size
		}
	}
	return out.String()
}

func skipEscape(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[':
		return skipCSI(s, i+1)
	case ']', 'P', '_', '^', 'X':
		return skipStringControl(s, i+1)
	default:
		return i + 1
	}
}

func skipCSI(s string, i int) int {
	for i < len(s) {
		if s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return i
}

func skipStringControl(s string, i int) int {
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == '\a' || r == 0x9c || s[i] == 0x9c {
			return i + size
		}
		if r == 0x1b && i+size < len(s) && s[i+size] == '\\' {
			return i + size + 1
		}
		i += size
	}
	return i
}
