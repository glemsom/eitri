package provider

import (
	"io"
	"sync"
)

type closeOnDoneStream struct {
	inner Stream
	body  io.Closer
	once  sync.Once
}

func closeBodyOnDone(inner Stream, body io.Closer) Stream {
	return &closeOnDoneStream{inner: inner, body: body}
}

func (s *closeOnDoneStream) Next() (Chunk, error) {
	c, err := s.inner.Next()
	if err != nil || c.Done {
		s.close()
	}
	return c, err
}

func (s *closeOnDoneStream) close() {
	s.once.Do(func() {
		if s.body != nil {
			_ = s.body.Close()
		}
	})
}
