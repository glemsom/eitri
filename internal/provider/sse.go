package provider

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// sseEvent is one parsed Server-Sent Event: a data payload. Eitri's providers
// only read the data field, so the other SSE fields (id, event, retry) are
// intentionally ignored.
type sseEvent struct {
	data string
}

// sse reads Server-Sent Events from a stream. It yields one event per logical
// event: consecutive data: lines within one event are joined with a newline,
// and comment-only (":"-prefixed) lines and blank separators are skipped. It
// returns (event, nil) while data remains and (sseEvent{}, io.EOF) at end of
// stream.
type sse struct {
	r *bufio.Reader
}

// newSSE wraps r in an SSE reader.
func newSSE(r io.Reader) *sse {
	return &sse{r: bufio.NewReader(r)}
}

// Next returns the next event in the stream.
func (s *sse) Next() (sseEvent, error) {
	for {
		line, err := readLine(s.r)
		if errors.Is(err, io.EOF) {
			return sseEvent{}, io.EOF
		}
		if err != nil {
			return sseEvent{}, err
		}
		if line == "" || strings.HasPrefix(line, ":") {
			// Blank separator or comment: skip.
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			// Non-data SSE field we do not consume (id/event/retry).
			continue
		}
		// Gather contiguous data: lines into one event. A blank line, a
		// comment, or a non-data field ends the event; readAhead pushes the
		// terminator back is unnecessary since Next discards it anyway, but
		// we must not consume the first line of the *next* data event. Blank
		// lines are pure separators (no meaningful content), so consuming
		// them is safe.
		var lines []string
		for {
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			next, err := readLine(s.r)
			if errors.Is(err, io.EOF) {
				return sseEvent{data: strings.Join(lines, "\n")}, nil
			}
			if err != nil {
				return sseEvent{}, err
			}
			if !strings.HasPrefix(next, "data:") {
				// Event complete; the next line is a separator/comment/field
				// and will be skipped by the next Next() call.
				return sseEvent{data: strings.Join(lines, "\n")}, nil
			}
			line = next
		}
	}
}

// readLine returns the next line without its trailing newline. It collapses
// "\r\n" and "\n" terminators. It returns a partial final line plus nil when
// the stream ends without a newline, and io.EOF only when no data remains.
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", io.EOF
	}
	if errors.Is(err, io.EOF) && line == "" {
		return "", io.EOF
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}
