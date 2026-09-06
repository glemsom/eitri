package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// maxStdinContextBytes caps how much piped stdin batch mode will accept as
// fenced context. Larger input is refused outright: a silent truncation could
// hand the model a partial diff and make it answer confidently on data it
// never saw.
const maxStdinContextBytes = 1 << 20 // 1 MiB

// stdinHeader introduces the fenced stdin block inside the batch prompt, so
// upstream data can never be mistaken for instructions: the block is declared
// as input, not as a command to follow.
const stdinHeader = "Stdin input:"

// ErrStdinWithoutBatch is returned when stdin is piped into an interactive
// launch that never reads it: without the explicit `eitri -b "<prompt>"`
// request the piped data would be silently drained, so Eitri refuses instead.
var ErrStdinWithoutBatch = errors.New("stdin is piped but no batch prompt was given; run eitri -b \"<prompt>\" to pipe data in as context — piped stdin is never silently drained")

// stdinIsTerminal reports whether host stdin is an interactive terminal (a
// character device: a TTY, /dev/null, or a closed descriptor). Only non-terminal
// stdin — pipes and file redirects — carries batch context; terminal input is
// never read or drained.
var stdinIsTerminal = func() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// stdinSource returns the stream batch mode should consume as piped context:
// an explicitly supplied stream (the test seam, and always treated as piped),
// else host stdin when it is not an interactive terminal. Terminal stdin yields
// nil, leaving the prompt untouched.
func stdinSource(opt io.Reader) io.Reader {
	if opt != nil {
		return opt
	}
	if stdinIsTerminal() {
		return nil
	}
	return os.Stdin
}

// withStdinContext appends piped stdin to the batch prompt as a fenced block
// introduced by the Stdin input: header. Empty stdin is silently ignored (the
// prompt is returned unchanged), and input larger than maxStdinContextBytes is
// refused with an error rather than truncated.
func withStdinContext(prompt string, r io.Reader) (string, error) {
	if r == nil {
		return prompt, nil
	}
	data, err := io.ReadAll(io.LimitReader(r, maxStdinContextBytes+1))
	if err != nil {
		return prompt, fmt.Errorf("read piped stdin: %w", err)
	}
	if len(data) == 0 {
		return prompt, nil
	}
	if len(data) > maxStdinContextBytes {
		return prompt, fmt.Errorf("piped stdin exceeds the 1 MiB batch-context cap; refusing rather than truncating")
	}
	block := "\n\n" + stdinHeader + "\n```\n" + string(data)
	if !strings.HasSuffix(block, "\n") {
		block += "\n"
	}
	return prompt + block + "```", nil
}
