package api

import (
	"testing"

	"google.golang.org/adk/v2/model"
	adksession "google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/glemsom/eitri/internal/executor"
	runner "github.com/glemsom/eitri/internal/runner"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
)

func TestAppendEvent_FinalEventWithContent_AppendsAssistantMessage(t *testing.T) {
	uiMgr := uisession.NewManager(10)
	sess, err := uiMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Use the real RunService to get runSvc for AppendEvent
	runSvc := runner.NewRunService(runner.RunServiceDeps{
		RunnerManager:  runner.NewManager(),
		SessionManager: executor.NewSessionManager(t.TempDir(), 0, 0),
		UISessionMgr:   uiMgr,
	})

	events := make(chan *adksession.Event, 1)
	errs := make(chan error)
	sseState := runstate.New()
	state := &runner.RunState{
		SessionID: sess.ID,
		Events:    events,
		Errors:    errs,
		Done:      make(chan struct{}),
		SSE:       sseState,
	}

	events <- &adksession.Event{
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{
				Role:  genai.RoleModel,
				Parts: []*genai.Part{{Text: "hello from final event"}},
			},
			TurnComplete: true,
		},
	}
	close(events)

	got := runSvc.AppendEvent(state)
	if got != "hello from final event" {
		t.Fatalf("AppendEvent() = %q, want %q", got, "hello from final event")
	}

	stored := uiMgr.Get(sess.ID)
	if stored == nil {
		t.Fatal("session missing after append")
	}
	if len(stored.Messages) != 1 {
		t.Fatalf("stored messages = %d, want 1", len(stored.Messages))
	}
	if stored.Messages[0].Role != "assistant" {
		t.Fatalf("stored role = %q, want assistant", stored.Messages[0].Role)
	}
	if stored.Messages[0].Content != "hello from final event" {
		t.Fatalf("stored content = %q, want %q", stored.Messages[0].Content, "hello from final event")
	}

	foundToken := false
	foundDone := false
	for _, evt := range sseState.History() {
		if evt.Type == "token" && evt.Content == "hello from final event" {
			foundToken = true
		}
		if evt.Type == "done" {
			foundDone = true
		}
	}
	if !foundToken {
		t.Fatal("expected token event for final-only content")
	}
	if !foundDone {
		t.Fatal("expected done event")
	}

	if !sseState.Closed() {
		t.Fatal("run state streams not closed")
	}
}

func TestAppendEvent_ToolCallTurnCompleteDoesNotEndRun(t *testing.T) {
	uiMgr := uisession.NewManager(10)
	sess, err := uiMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	runSvc := runner.NewRunService(runner.RunServiceDeps{
		RunnerManager:  runner.NewManager(),
		SessionManager: executor.NewSessionManager(t.TempDir(), 0, 0),
		UISessionMgr:   uiMgr,
	})

	events := make(chan *adksession.Event, 2)
	errs := make(chan error)
	sseState := runstate.New()
	state := &runner.RunState{
		SessionID: sess.ID,
		Events:    events,
		Errors:    errs,
		Done:      make(chan struct{}),
		SSE:       sseState,
	}

	events <- &adksession.Event{
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{
				Role: genai.RoleModel,
				Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{
					Name: "activate_skill",
					Args: map[string]any{"name": "code-review"},
				}}},
			},
			TurnComplete: true,
		},
	}
	events <- &adksession.Event{
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{
				Role:  genai.RoleModel,
				Parts: []*genai.Part{{Text: "done after tool"}},
			},
			TurnComplete: true,
		},
	}
	close(events)

	got := runSvc.AppendEvent(state)
	if got != "done after tool" {
		t.Fatalf("AppendEvent() = %q, want %q", got, "done after tool")
	}

	foundToolCall := false
	foundDone := false
	for _, evt := range sseState.History() {
		if evt.Type == "tool_call" && evt.Tool == "activate_skill" {
			foundToolCall = true
		}
		if evt.Type == "done" {
			foundDone = true
		}
	}
	if !foundToolCall {
		t.Fatal("expected tool_call event")
	}
	if !foundDone {
		t.Fatal("expected done event")
	}
}
