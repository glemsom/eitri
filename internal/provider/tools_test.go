package provider

import (
	"encoding/json"
	"errors"
	"io"
	"testing"
)

func TestParseEventAccumulatesToolCall(t *testing.T) {
	t.Parallel()
	var last Chunk
	var finish string
	s := &toolFixtureStream{data: []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"comman"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"d\":\"echo hi\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"usage":{"prompt_tokens":1,"completion_tokens":2}}`,
		`[DONE]`,
	}}
	for {
		c, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v, want nil", err)
		}
		last = c
		if c.FinishReason != "" && finish == "" {
			finish = c.FinishReason
		}
	}
	if !last.Done {
		t.Fatalf("terminal chunk Done = false")
	}
	if len(last.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %d, want 1 (got %+v)", len(last.ToolCalls), last.ToolCalls)
	}
	tc := last.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "bash" {
		t.Fatalf("tool call = %+v, want id=call_1 name=bash", tc)
	}
	if tc.Arguments != `{"command":"echo hi"}` {
		t.Fatalf("arguments = %q, want reassembled JSON", tc.Arguments)
	}
	if finish != "tool_calls" {
		t.Fatalf("FinishReason = %q, want tool_calls", finish)
	}
}

func TestToolMessageMarshalsEmptyContent(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(Message{Role: RoleTool, ToolCallID: "call_9", Content: ""})
	if err != nil {
		t.Fatalf("marshal error = %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("unmarshal error = %v", err)
	}
	if _, ok := parsed["content"]; !ok {
		t.Fatalf("tool message with empty content missing 'content' field: %s", b)
	}
}

func TestToolMessageMarshalsWithToolCallID(t *testing.T) {
	t.Parallel()
	m := Message{Role: RoleTool, ToolCallID: "call_9", Content: "ok"}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal error = %v", err)
	}
	want := `{"role":"tool","content":"ok","tool_call_id":"call_9"}`
	if string(b) != want {
		t.Fatalf("tool message = %s, want %s", b, want)
	}
}

func TestToolCallMarshalsNestedFunction(t *testing.T) {
	t.Parallel()
	type wireShape struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Func struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}

	b, err := json.Marshal(ToolCall{ID: "call_1", Type: "function", Name: "bash", Arguments: `{"command":"whoami"}`})
	if err != nil {
		t.Fatalf("marshal error = %v", err)
	}
	var w wireShape
	if err := json.Unmarshal(b, &w); err != nil {
		t.Fatalf("unmarshal marshaled tool call: %v (%s)", err, b)
	}
	if w.ID != "call_1" || w.Type != "function" {
		t.Fatalf("tool call head = %s, want id/type retained", b)
	}
	if w.Func.Name != "bash" || w.Func.Arguments != `{"command":"whoami"}` {
		t.Fatalf("tool call function = %+v, want name+arguments nested", w.Func)
	}
}

type toolFixtureStream struct {
	data []string
	idx  int
	acc  *toolAccumulator
}

func (s *toolFixtureStream) Next() (Chunk, error) {
	if s.acc == nil {
		s.acc = newToolAccumulator()
	}
	if s.idx >= len(s.data) {
		return Chunk{}, io.EOF
	}
	data := s.data[s.idx]
	s.idx++
	return parseEvent(data, s.acc)
}
