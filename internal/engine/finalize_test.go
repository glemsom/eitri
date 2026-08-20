package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

type jsonScripted struct {
	provider.Scripted
}

func (j *jsonScripted) SupportedGenerationControls(context.Context) ([]provider.GenerationControl, error) {
	return []provider.GenerationControl{provider.GenerationControlJSONObjectMode}, nil
}

type jsonHandler struct {
	requests []provider.Request
}

func (j *jsonHandler) stream(_ context.Context, req provider.Request) (provider.Stream, error) {
	j.requests = append(j.requests, req)
	return provider.StreamFunc(
		provider.Chunk{Content: `{"status":"ok","work":"done"}`},
		provider.Chunk{FinishReason: "stop", Done: true, Usage: &provider.Usage{PromptTokens: 5, CompletionTokens: 3}},
	), nil
}

func TestRunJSONObjectModeFinalizesOnSupportingProvider(t *testing.T) {
	t.Parallel()
	h := &jsonHandler{}
	e := New(&jsonScripted{Scripted: *provider.NewScripted(h.stream)}, &mockTranscript{})

	res, err := e.RunJSONObjectMode(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     `Finalize as JSON: {"ok": true}`,
		SessionKey: "sess-finalize",
	})
	if err != nil {
		t.Fatalf("RunJSONObjectMode() error = %v, want nil", err)
	}
	if res.Answer != `{"status":"ok","work":"done"}` {
		t.Fatalf("Answer = %q, want the finalized JSON object", res.Answer)
	}
	if res.Usage == nil || res.Usage.CompletionTokens != 3 {
		t.Fatalf("Usage = %+v, want completion tokens 3 threaded through", res.Usage)
	}
	if len(h.requests) != 1 {
		t.Fatalf("provider requests = %d, want exactly 1 finalization turn", len(h.requests))
	}
	if !h.requests[0].JSONObjectMode {
		t.Fatalf("finalization request JSONObjectMode = false, want true (wire emits response_format json_object)")
	}
	if len(h.requests[0].Tools) != 0 {
		t.Fatalf("finalization request carried tools, want a non-tool special turn")
	}
	if got := h.requests[0].SessionKey; got != "sess-finalize" {
		t.Fatalf("finalization SessionKey = %q, want sess-finalize", got)
	}
}

func TestRunJSONObjectModeFailsFastWhenUnsupported(t *testing.T) {
	t.Parallel()
	h := &jsonHandler{}
	e := New(provider.NewScripted(h.stream), &mockTranscript{})

	_, err := e.RunJSONObjectMode(context.Background(), RunRequest{
		Model:  "deepseek-v4-flash",
		Prompt: `Finalize as JSON: {}`,
	})
	var unsupported *provider.UnsupportedRequiredControlError
	if !errors.As(err, &unsupported) {
		t.Fatalf("RunJSONObjectMode() error = %v, want *provider.UnsupportedRequiredControlError", err)
	}
	if unsupported.Control != provider.GenerationControlJSONObjectMode {
		t.Fatalf("unsupported control = %q, want json_object_mode", unsupported.Control)
	}
	if len(h.requests) != 0 {
		t.Fatalf("provider requests = %d, want 0 — the turn must fail before any wire call", len(h.requests))
	}
}

func TestRunJSONObjectModeAppendsJsonHintWhenPromptLacksIt(t *testing.T) {
	t.Parallel()
	h := &jsonHandler{}
	e := New(&jsonScripted{Scripted: *provider.NewScripted(h.stream)}, &mockTranscript{})

	res, err := e.RunJSONObjectMode(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "Finalize the gathered findings into structured output.",
		SessionKey: "sess-finalize",
	})
	if err != nil {
		t.Fatalf("RunJSONObjectMode() error = %v, want nil", err)
	}
	_ = res
	if len(h.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(h.requests))
	}
	userMsg := h.requests[0].Messages[1]
	if !strings.Contains(strings.ToLower(userMsg.Content), "json") {
		t.Fatalf("json-object-mode user prompt = %q, must contain the word 'json' (provider contract)", userMsg.Content)
	}
}

func TestJSONObjectPromptPreservesPromptThatRequestsJSON(t *testing.T) {
	t.Parallel()
	in := `Return JSON like {"ok":true}`
	if got := jsonObjectPrompt(in); got != in {
		t.Fatalf("jsonObjectPrompt(%q) = %q, want unchanged", in, got)
	}
}

func TestJSONObjectPromptAppendsSuffix(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"Finalize the answer", "Do not use JSON here", ""} {
		got := jsonObjectPrompt(in)
		if !strings.Contains(strings.ToLower(got), "json") {
			t.Fatalf("jsonObjectPrompt(%q) = %q, want it to advertise json", in, got)
		}
	}
}
