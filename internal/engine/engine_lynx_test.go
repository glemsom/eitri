package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// lynxCaptureHandler records each request's messages so tests can assert the
// system-layer HTML-rendering directive placement without going to the wire.
type lynxCaptureHandler struct {
	requests []provider.Request
}

func (c *lynxCaptureHandler) stream(ctx context.Context, req provider.Request) (provider.Stream, error) {
	c.requests = append(c.requests, req)
	return provider.StreamFunc(
		provider.Chunk{Content: "ok", FinishReason: "stop", Done: true},
	), nil
}

func TestRunAgentLynxAbsentKeepsByteIdenticalBaseline(t *testing.T) {
	t.Parallel()
	c := &lynxCaptureHandler{}
	e := New(provider.NewScripted(c.stream), nil)

	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "hi",
		SessionKey: "sess-abc",
		Workspace:  "/tmp/ws",
	}, AgentOptions{MaxTurns: 1}); err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}

	msgs := c.requests[0].Messages
	var lynxCount int
	for _, m := range msgs {
		if strings.Contains(m.Content, "## HTML rendering") {
			lynxCount++
		}
	}
	if lynxCount != 0 {
		t.Fatalf("lynx directive present with Lynx=false: %d system message(s)", lynxCount)
	}
}

func TestRunAgentLynxPresentInjectsHTMLRenderingAfterWorkspace(t *testing.T) {
	t.Parallel()
	c := &lynxCaptureHandler{}
	e := New(provider.NewScripted(c.stream), nil)

	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "hi",
		SessionKey: "sess-abc",
		Workspace:  "/tmp/ws",
		Lynx:       true,
	}, AgentOptions{MaxTurns: 1}); err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}

	msgs := c.requests[0].Messages
	// persona head, workspace directive, lynx directive, then user prompt.
	var lynxIndex, wsIndex = -1, -1
	var lynxMsgs int
	for i, m := range msgs {
		switch {
		case strings.Contains(m.Content, "## HTML rendering"):
			lynxIndex = i
			lynxMsgs++
		case strings.Contains(m.Content, "## Working directory"):
			wsIndex = i
		}
	}
	if lynxMsgs != 1 {
		t.Fatalf("got %d lynx directive message(s), want exactly 1", lynxMsgs)
	}
	if lynxIndex != wsIndex+1 {
		t.Fatalf("lynx directive at index %d, want immediately after workspace directive at %d", lynxIndex, wsIndex)
	}
	if lynxIndex > len(msgs)-2 {
		t.Fatalf("expected user prompt to follow the lynx directive; messages=%+v", msgs)
	}
	if !strings.Contains(msgs[lynxIndex].Content, "lynx -dump") {
		t.Fatalf("lynx directive missing the curl|lynx guidance:\n%s", msgs[lynxIndex].Content)
	}
}

func TestRunAgentLynxNotPersistedInSessionHistory(t *testing.T) {
	t.Parallel()
	c := &lynxCaptureHandler{}
	e := New(provider.NewScripted(c.stream), nil)

	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "first",
		SessionKey: "sess-abc",
		Lynx:       true,
	}, AgentOptions{MaxTurns: 1}); err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}

	// Second run under the same session key must not see the directive twice:
	// it is re-injected fresh from req.Lynx and stripped from persisted history.
	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "second",
		SessionKey: "sess-abc",
		Lynx:       true,
	}, AgentOptions{MaxTurns: 1}); err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}

	second := c.requests[1].Messages
	var lynxCount int
	for _, m := range second {
		if strings.Contains(m.Content, "## HTML rendering") {
			lynxCount++
		}
	}
	if lynxCount != 1 {
		t.Errorf("second run sees the lynx directive %d times, want 1 (injected once, never persisted/duplicated)", lynxCount)
	}
}