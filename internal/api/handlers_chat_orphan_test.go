package api_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestChatOrphanedMessageOnStartRunFailure verifies that when StartRun fails,
// the user message is not left in the conversation (issue #972).
//
// The fix moves AppendMessage to after StartRun succeeds, so if starting
// the run fails, the message is never added to the conversation.
func TestChatOrphanedMessageOnStartRunFailure(t *testing.T) {
	h := newManagedTestServerWithRuns(t)
	client := noRedirectClient()

	// Create a session
	rootResp, err := client.Get(h.server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer rootResp.Body.Close()
	loc := rootResp.Header.Get("Location")

	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	if browserCookie == nil {
		t.Fatal("missing browser cookie")
	}

	sessionID := strings.TrimPrefix(loc, "/sessions/")

	// Verify session starts with no messages
	convo := h.sessionMgr.GetConversation(sessionID)
	if convo == nil {
		t.Fatal("session not found")
	}
	initialMsgCount := len(convo.Messages)

	// Configure invalid provider to force StartRun failure.
	// We use an unreachable base_url so buildLLMService fails inside StartRun.
	// This simulates the exact scenario from issue #972: LLM service construction error.
	putJSONConfig(t, h.server, `{"provider":"custom_openai","base_url":"http://127.0.0.1:1","api_key":"sk-test","model":"gpt-4"}`)

	// Send a chat message that will fail during StartRun
	chatReq, _ := http.NewRequest(http.MethodPost, h.server.URL+"/api"+loc+"/chat", strings.NewReader("message=orphaned+message"))
	chatReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	chatReq.Header.Set("HX-Request", "true")
	chatReq.AddCookie(browserCookie)
	chatResp, err := client.Do(chatReq)
	if err != nil {
		t.Fatal(err)
	}
	defer chatResp.Body.Close()

	// Should get 500 from StartRun failure
	if chatResp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", chatResp.StatusCode)
	}

	// Verify the message was NOT added to the conversation
	convo = h.sessionMgr.GetConversation(sessionID)
	if convo == nil {
		t.Fatal("session not found after chat")
	}
	if len(convo.Messages) != initialMsgCount {
		t.Errorf("message count = %d, want %d (message should not be added when StartRun fails)", len(convo.Messages), initialMsgCount)
		for i, msg := range convo.Messages {
			t.Logf("  [%d] role=%s content=%q", i, msg.Role, msg.Content)
		}
	}
}
