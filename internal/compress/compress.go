// Package compress applies deterministic pattern compression to bash tool
// output. Patterns are matched by command name (ls, find, grep, rg) and
// produce regrouped, summarized output that fits more useful information
// into fewer tokens.
//
// Every compressor guarantees it never inflates: if the compressed result
// would use more estimated tokens than the original, the original is returned
// unchanged.
package compress

import (
	"strings"

	"github.com/glemsom/eitri/internal/tokenizer"
)

// Compress applies deterministic pattern compression to bash tool output.
// Returns the compressed text when a pattern matches and compression is
// beneficial (fewer estimated tokens), or the original output unchanged.
func Compress(command, output string) string {
	if output == "" {
		return output
	}

	cmd := strings.TrimSpace(strings.ToLower(command))

	// Strip leading $ if present (LeanCTX convention for command hints).
	cmd = strings.TrimPrefix(cmd, "$ ")

	var fn func(string) *string
	switch {
	case cmd == "ls" || strings.HasPrefix(cmd, "ls "):
		fn = compressLs
	case cmd == "find" || strings.HasPrefix(cmd, "find "):
		fn = compressFind
	case cmd == "grep" || strings.HasPrefix(cmd, "grep "):
		fn = compressGrep
	case cmd == "rg" || strings.HasPrefix(cmd, "rg "):
		fn = compressGrep
	case cmd == "ripgrep" || strings.HasPrefix(cmd, "ripgrep "):
		fn = compressGrep
	}

	if fn == nil {
		return output
	}

	compressed := fn(output)
	if compressed == nil {
		return output
	}

	// Anti-inflation guard: only use compressed output if it actually
	// reduces estimated tokens (default chars/token heuristic).
	origTokens := tokenizer.Estimate(output, nil, "")
	compTokens := tokenizer.Estimate(*compressed, nil, "")
	if compTokens >= origTokens {
		return output
	}

	return *compressed
}
