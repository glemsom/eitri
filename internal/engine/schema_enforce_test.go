package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

type schemaHandler struct {
	requests []provider.Request
}

func (s *schemaHandler) stream(_ context.Context, req provider.Request) (provider.Stream, error) {
	s.requests = append(s.requests, req)
	return provider.StreamFunc(
		provider.Chunk{Content: "done", FinishReason: "stop", Done: true, Usage: &provider.Usage{PromptTokens: 3, CompletionTokens: 2}},
	), nil
}

type capableScriptedSchema struct {
	*provider.Scripted
}

func (c *capableScriptedSchema) SupportedGenerationControls(context.Context) ([]provider.GenerationControl, error) {
	return []provider.GenerationControl{provider.GenerationControlToolSchemaEnforcement}, nil
}

func TestRunAgentOptsToolSchemaEnforcementOnSupportingProvider(t *testing.T) {
	t.Parallel()
	h := &schemaHandler{}
	e := New(&capableScriptedSchema{Scripted: provider.NewScripted(h.stream)}, &mockTranscript{})

	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:  "deepseek-v4-flash",
		Prompt: "list the file",
	}, AgentOptions{
		Tools:                 strictToolDefs(),
		Executor:              &mockToolRecorder{},
		ToolSchemaEnforcement: true,
	}); err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}
	if len(h.requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(h.requests))
	}
	if !h.requests[0].ToolSchemaEnforcement {
		t.Fatalf("request ToolSchemaEnforcement = false, want true (supporting provider should wire strict manifests)")
	}
}

func TestRunAgentDegradesWhenToolSchemaUnsupported(t *testing.T) {
	t.Parallel()
	h := &schemaHandler{}
	e := New(provider.NewScripted(h.stream), &mockTranscript{})

	res, err := e.RunAgent(context.Background(), RunRequest{
		Model:  "deepseek-v4-flash",
		Prompt: "list the file",
	}, AgentOptions{
		Tools:                 strictToolDefs(),
		Executor:              &mockToolRecorder{},
		ToolSchemaEnforcement: true,
	})
	if err != nil {
		t.Fatalf("RunAgent error = %v, want nil — an optional unsupported control must not fail the loop", err)
	}
	if res.Answer != "done" {
		t.Fatalf("Answer = %q, want %q", res.Answer, "done")
	}
	if len(h.requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(h.requests))
	}
	if h.requests[0].ToolSchemaEnforcement {
		t.Fatalf("request ToolSchemaEnforcement = true, want false (unsupported provider omits strict)")
	}
}

func TestRunAgentDefaultOmitsToolSchemaEnforcement(t *testing.T) {
	t.Parallel()
	h := &schemaHandler{}
	e := New(&capableScriptedSchema{Scripted: provider.NewScripted(h.stream)}, &mockTranscript{})

	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:  "deepseek-v4-flash",
		Prompt: "list the file",
	}, AgentOptions{
		Tools:    strictToolDefs(),
		Executor: &mockToolRecorder{},
	}); err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}
	if len(h.requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(h.requests))
	}
	if h.requests[0].ToolSchemaEnforcement {
		t.Fatalf("request ToolSchemaEnforcement = true, want false by default")
	}
}

func TestRunAgentLocalValidationStaysMandatoryWhenEnforcementActive(t *testing.T) {
	t.Parallel()
	capable := &capableScriptedSchema{}
	capable.Scripted = provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		if len(toolResultContents(req.Messages)) == 0 {
			return provider.StreamFunc(
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_bad", Type: "function", Name: "bash", Arguments: `{"typo":"echo hi"}`},
				}, Done: true},
			), nil
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "fixed"},
			provider.Chunk{FinishReason: "stop", Done: true},
		), nil
	})
	e := New(capable, &mockTranscript{})

	rec := &mockToolRecorder{}
	res, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{
		Tools:                 strictToolDefs(),
		Executor:              rec,
		MaxTurns:              5,
		ToolSchemaEnforcement: true,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if res.Answer != "fixed" {
		t.Fatalf("Answer = %q, want recovery answer", res.Answer)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("executor called with %+v, want no calls for a schema-violating call even with provider enforcement active", rec.calls)
	}
}

func runReadEnforcement(t *testing.T, args string) ([]callRecord, []string, string) {
	t.Helper()
	var results []string
	capable := &capableScriptedSchema{Scripted: provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		results = toolResultContents(req.Messages)
		if len(results) == 0 {
			return provider.StreamFunc(
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_read", Type: "function", Name: "read", Arguments: args},
				}, Done: true},
			), nil
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "read it"},
			provider.Chunk{FinishReason: "stop", Done: true, Usage: &provider.Usage{PromptTokens: 4, CompletionTokens: 3}},
		), nil
	})}
	e := New(capable, &mockTranscript{})
	rec := &mockToolRecorder{}
	res, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{
		Tools:                 strictToolDefs(),
		Executor:              rec,
		MaxTurns:              5,
		ToolSchemaEnforcement: true,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	return rec.calls, results, res.Answer
}

func TestReadRequiredSubsetExecutesUnderSchemaEnforcement(t *testing.T) {
	t.Parallel()
	calls, results, answer := runReadEnforcement(t, `{"path":"f.txt"}`)
	if answer != "read it" {
		t.Fatalf("Answer = %q, want recovery answer", answer)
	}
	if len(calls) != 1 || calls[0].name != "read" {
		t.Fatalf("executor calls = %+v, want exactly one read call", calls)
	}
	for _, r := range results {
		if strings.Contains(r, "invalid tool arguments") {
			t.Fatalf("schema-error result resubmitted for a required-subset read: %q", r)
		}
	}
}

func TestReadNullOptionalsToleratedUnderSchemaEnforcement(t *testing.T) {
	t.Parallel()
	calls, results, answer := runReadEnforcement(t, `{"path":"f.txt","start_line":null,"end_line":null}`)
	if answer != "read it" {
		t.Fatalf("Answer = %q, want recovery answer", answer)
	}
	if len(calls) != 1 || calls[0].name != "read" {
		t.Fatalf("executor calls = %+v, want exactly one read call", calls)
	}
	for _, r := range results {
		if strings.Contains(r, "invalid tool arguments") {
			t.Fatalf("schema-error result resubmitted for a null-optional read: %q", r)
		}
	}
}
