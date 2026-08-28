package provider

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// sseEvent is one parsed Server-Sent Event: a data payload.
type sseEvent struct {
	data string
}

// sse reads Server-Sent Events from a stream.
type sse struct {
	r *bufio.Reader
}

// newSSE wraps r in an SSE reader.
func newSSE(r io.Reader) *sse {
	return &sse{r: bufio.NewReader(r)}
}

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
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		// Gather contiguous data: lines into one event.
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
				return sseEvent{data: strings.Join(lines, "\n")}, nil
			}
			line = next
		}
	}
}

// readLine returns the next line without its trailing newline.
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if errors.Is(err, io.EOF) && line == "" {
		return "", io.EOF
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}
