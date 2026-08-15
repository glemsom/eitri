package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// jsonScripted wraps a Scripted handler so it also declares, via the
// generation-control capability surface, that it honors JSON Object Mode — the
// wire-emitting control a supporting provider advertises. The engine opts
// a JSON-Object-Mode finalization turn into that
// control, so the finalization request must carry the JSON object request flag.
type jsonScripted struct {
	provider.Scripted
}

// SupportedGenerationControls implements provider.GenerationControlProvider.
func (j *jsonScripted) SupportedGenerationControls(context.Context) ([]provider.GenerationControl, error) {
	return []provider.GenerationControl{provider.GenerationControlJSONObjectMode}, nil
}

// jsonHandler records every request it serves and returns a fixed JSON object
// as the finalized answer so a test can assert the path end to end.
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

// TestRunJSONObjectModeFinalizesOnSupportingProvider verifies the JSON
// Object Mode finalization special turn: on a
// provider that honors the control, the engine issues a non-tool turn flagged
// for JSON Object Mode — so the wire carries response_format:{type:json_object}
// — and returns the finalized JSON object as the final answer. The session key
// and prompt thread through unchanged.
func TestRunJSONObjectModeFinalizesOnSupportingProvider(t *testing.T) {
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

// TestRunJSONObjectModeFailsFastWhenUnsupported verifies the generation-control
// contract: a provider without the JSON Object
// Mode capability honors no controls, so a required json_object_mode
// finalization fails negotiation fast — before any wire call — and ordinary
// agent runs remain on the untouched non-tool path.
func TestRunJSONObjectModeFailsFastWhenUnsupported(t *testing.T) {
	// NewScripted has no generation-control capability surface: it honors no
	// controls, so a required JSON Object Mode fails the contract.
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

// TestRunJSONObjectModeAppendsJsonHintWhenPromptLacksIt pins the provider
// contract that tripped the REAL opencode-go/DeepSeek endpoint (issue #59 + the
// reproduced HTTP 400): response_format:{type:json_object} hard-requires the
// prompt to contain the word "json", else the gateway rejects the request with
// 400 "Prompt must contain the word 'json' in some form". The engine must append
// a JSON hint so the finalization turn always satisfies that contract.
func TestRunJSONObjectModeAppendsJsonHintWhenPromptLacksIt(t *testing.T) {
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

// TestJSONObjectPromptPreservesPromptThatRequestsJSON verifies jsonObjectPrompt
// leaves a prompt already mentioning JSON byte-identical, so an explicit caller
// request is never mutated.
func TestJSONObjectPromptPreservesPromptThatRequestsJSON(t *testing.T) {
	in := `Return JSON like {"ok":true}`
	if got := jsonObjectPrompt(in); got != in {
		t.Fatalf("jsonObjectPrompt(%q) = %q, want unchanged", in, got)
	}
}

// TestJSONObjectPromptAppendsSuffix verifies jsonObjectPrompt appends the fixed
// JSON hint to a prompt that does not already mention JSON (case-insensitive).
func TestJSONObjectPromptAppendsSuffix(t *testing.T) {
	for _, in := range []string{"Finalize the answer", "Do not use JSON here", ""} {
		got := jsonObjectPrompt(in)
		if !strings.Contains(strings.ToLower(got), "json") {
			t.Fatalf("jsonObjectPrompt(%q) = %q, want it to advertise json", in, got)
		}
	}
}
