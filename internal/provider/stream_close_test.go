package provider

import "testing"

type closeFlag struct{ closed bool }

func (c *closeFlag) Close() error {
	c.closed = true
	return nil
}

func TestCloseBodyOnDoneClosesOnDoneChunk(t *testing.T) {
	body := &closeFlag{}
	s := closeBodyOnDone(StreamFunc(Chunk{Done: true, FinishReason: "stop"}), body)
	if _, err := s.Next(); err != nil {
		t.Fatalf("Next() error = %v, want nil", err)
	}
	if !body.closed {
		t.Fatal("body not closed after done chunk")
	}
}

func TestCloseBodyOnDoneClosesOnError(t *testing.T) {
	body := &closeFlag{}
	s := closeBodyOnDone(StreamFunc(), body)
	_, _ = s.Next()
	if !body.closed {
		t.Fatal("body not closed after stream error/EOF")
	}
}
