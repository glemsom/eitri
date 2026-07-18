package litellm

import (
	"io"
	"strings"
)

// ————— SSE scanner —————

type sseScanner struct {
	body  io.Reader
	buf   []byte
	event string
	data  string
	done  bool
}

func newSSEScanner(body io.Reader) *sseScanner {
	return &sseScanner{
		body: body,
		buf:  make([]byte, 0, 4096),
	}
}

func (s *sseScanner) Scan() bool {
	if s.done {
		return false
	}
	s.event = ""
	s.data = ""

	tmp := make([]byte, 1)
	for {
		n, err := s.body.Read(tmp)
		if n > 0 {
			s.buf = append(s.buf, tmp[0])
			if tmp[0] == '\n' && len(s.buf) >= 2 {
				// Check if we have a blank line (double newline = event boundary)
				ends := string(s.buf)
				if strings.HasSuffix(strings.TrimRight(ends, "\r"), "\n\n") {
					break
				}
			}
		}
		if err != nil {
			s.done = true
			break
		}
	}

	if len(s.buf) == 0 {
		return false
	}

	lines := strings.Split(string(s.buf), "\n")
	s.buf = s.buf[:0]

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			s.event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			s.data = strings.TrimPrefix(line, "data: ")
		}
	}

	return true
}

func (s *sseScanner) Event() string { return s.event }
func (s *sseScanner) Data() string  { return s.data }
