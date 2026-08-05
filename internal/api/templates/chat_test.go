package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/session"
)

// TestChatView_ToolMessagesDoNotRenderAsUserBubbles guards against tool
// messages leaking into the chat as user bubbles. The parent conversation
// (synced from the run's live history) contains tool-role messages — e.g.
// the collect result carrying a sub-agent's full response. Those are shown
// as sidebar tool cards during the run, never as bubbles, so they must not
// appear in the chat view with the user's own avatar (issue: sub-agent
// responses showing in the main view after reload).
func TestChatView_ToolMessagesDoNotRenderAsUserBubbles(t *testing.T) {
	sess := &session.UISession{
		ID: "sess-1",
		Messages: []message.Message{
			{Role: "user", Content: "<p>please delegate something</p>", CreatedAt: time.Now()},
			// Assistant tool-call turn — empty content, no components/chips: not a bubble.
			{Role: "assistant", Content: "", CreatedAt: time.Now()},
			// delegate result
			{Role: "tool", Content: `{"task_id": "task_1"}`, CreatedAt: time.Now()},
			// collect result — the sub-agent's response lives here.
			{Role: "tool", Content: `{"task_1":{"status":"completed","result":"SUBAGENT-RESPONSE-MARKER","turn_count":1}}`, CreatedAt: time.Now()},
			{Role: "assistant", Content: "<p>parent done</p>", CreatedAt: time.Now()},
		},
	}

	var buf bytes.Buffer
	if err := ChatView(sess, true, "user@example.com", nil).Render(context.Background(), &buf); err != nil {
		t.Fatalf("ChatView render: %v", err)
	}
	html := buf.String()

	if strings.Contains(html, "SUBAGENT-RESPONSE-MARKER") {
		t.Errorf("chat view renders sub-agent response text; tool results must stay out of bubbles:\n%s", html)
	}
	if strings.Contains(html, "task_id") {
		t.Errorf("chat view renders delegate tool result text:\n%s", html)
	}

	// Exactly one user bubble (the real user message) and one assistant bubble
	// (the empty tool-call turn is skipped).
	if got := strings.Count(html, `class="message message-user"`); got != 1 {
		t.Errorf("user bubble count = %d, want 1", got)
	}
	if got := strings.Count(html, `class="message message-assistant"`); got != 1 {
		t.Errorf("assistant bubble count = %d, want 1 (empty tool-call turn must be skipped)", got)
	}
}

// TestChatView_EmptyAssistantWithQuickRepliesStillRenders ensures the bubble
// filter does not hide quick-reply chips: an assistant message with no text
// but inline quick replies is a real UI element (render_quick_replies).
func TestChatView_EmptyAssistantWithQuickRepliesStillRenders(t *testing.T) {
	sess := &session.UISession{
		ID: "sess-1",
		Messages: []message.Message{
			{Role: "user", Content: "<p>hi</p>", CreatedAt: time.Now()},
			{Role: "assistant", Content: "", QuickReplies: []string{"Yes", "No"}, CreatedAt: time.Now()},
		},
	}

	var buf bytes.Buffer
	if err := ChatView(sess, true, "user@example.com", nil).Render(context.Background(), &buf); err != nil {
		t.Fatalf("ChatView render: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, ">Yes<") || !strings.Contains(html, ">No<") {
		t.Errorf("quick-reply chips missing from empty assistant message:\n%s", html)
	}
}
