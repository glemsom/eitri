package provider

import (
	"errors"
	"io"
)

// consume reads a Stream to completion, returning the concatenated assistant
// answer content and the terminal usage, if any. Test-only helper: it is not
// referenced by any production source, so it lives in the test build.
func consume(s Stream) (string, *Usage, error) {
	var answer string
	var usage *Usage
	for {
		c, err := s.Next()
		if errors.Is(err, io.EOF) {
			return answer, usage, nil
		}
		if err != nil {
			return "", nil, err
		}
		answer += c.Content
		if c.Usage != nil {
			usage = c.Usage
		}
		if c.Done {
			return answer, usage, nil
		}
	}
}
