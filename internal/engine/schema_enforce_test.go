package engine

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// schemaHandler records every request it serves and returns a fixed final
// answer, so a test can assert exactly what a tool-capable agent loop opted the
// provider request into (issue #62).
type schemaHandler struct {
	requests []provider.Request
}

func (s *schemaHandler) stream(_ context.Context, req provider.Request) (provider.Stream, error) {
	s.requests = append(s.requests, req)
	return provider.StreamFunc(
		provider.Chunk{Content: "done", FinishReason: "stop", Done: true, Usage: &provider.Usage{PromptTokens: 3, CompletionTokens: 2}},
	), nil
}

// capableScriptedSchema is a Scripted provider that additionally declares
// tool_schema_enforcement support through the generation-control capability
// surface (issue #62).
type capableScriptedSchema struct {
	*provider.Scripted
}

func (c *capableScriptedSchema) SupportedGenerationControls(context.Context) ([]provider.GenerationControl, error) {
	return []provider.GenerationControl{provider.GenerationControlToolSchemaEnforcement}, nil
}

// TestRunAgentOptsToolSchemaEnforcementOnSupportingProvider verifies that an
// agent loop with ToolSchemaEnforcement opted in, on a provider that honors the
// tool_schema_enforcement control, flags the provider request so the supporting
// client wire-emits strict tool manifests (issue #62).
func TestRunAgentOptsToolSchemaEnforcementOnSupportingProvider(t *testing.T) {
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

// TestRunAgentDegradesWhenToolSchemaUnsupported verifies the deterministic
// degradation spelled out by the generation-control contract (issue #62): a provider
// without the tool_schema_enforcement capability honors
// no controls, so an opted-in optional requirement is dropped — strict is
// omitted on the wire — while the loop still runs with local validation as the
// safety floor.
func TestRunAgentDegradesWhenToolSchemaUnsupported(t *testing.T) {
	h := &schemaHandler{}
	// NewScripted has no generation-control capability surface: it honors nothing.
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

// TestRunAgentDefaultOmitsToolSchemaEnforcement verifies that an ordinary agent
// loop without the opt-in never flags the provider request, keeping the default
// wire surface byte-identical (issue #62).
func TestRunAgentDefaultOmitsToolSchemaEnforcement(t *testing.T) {
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

// TestRunAgentLocalValidationStaysMandatoryWhenEnforcementActive verifies the
// second acceptance half of issue #62: even when provider-side Tool Schema
// Enforcement is active on a supporting provider, Eitri's local tool-argument
// validation remains the mandatory safety floor before execution — a
// schema-violating tool call is still rejected and the executor is never
// called, exactly as without enforcement.
func TestRunAgentLocalValidationStaysMandatoryWhenEnforcementActive(t *testing.T) {
	capable := &capableScriptedSchema{}
	capable.Scripted = provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		if len(toolResultContents(req.Messages)) == 0 {
			// bash requires "command"; the call omits it.
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
