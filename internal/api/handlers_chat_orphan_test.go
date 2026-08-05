package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// chatFailingProviderServer returns a mock provider that:
// - Returns valid data for /v1/models (so config validation passes)
// - Returns error for /v1/chat/completions (so StartRun fails)
type chatFailingProviderServer struct {
	server *httptest.Server
	mu     sync.RWMutex
	fail   bool
}

func newChatFailingProviderServer(t *testing.T) *chatFailingProviderServer {
	t.Helper()
	m := &chatFailingProviderServer{fail: false}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		
		if r.URL.Path == "/v1/models" {
			// Always return valid models list for config validation
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4"}]}`))
			return
		}
		
		if r.URL.Path == "/v1/chat/completions" {
			m.mu.RLock()
			defer m.mu.RUnlock()
			if m.fail {
				// Return error to make StartRun fail
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"message":"Internal server error","type":"server_error"}}`))
				return
			}
			// Normal successful response
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chatcmpl-123","object":"chat.completion","created":1234567890,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"Hello! How can I help you?"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":10,"total_tokens":20}}`))
			return
		}
		
		http.NotFound(w, r)
	}))
	t.Cleanup(m.server.Close)
	return m
}

func (m *chatFailingProviderServer) URL() string {
	return m.server.URL
}

func (m *chatFailingProviderServer) SetFailChat(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fail = fail
}

// TestChatOrphanedMessageOnStartRunFailure verifies that when StartRun fails,
// the user message is not left in the conversation (issue #972).
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

	// Create a mock provider that validates config but fails during chat
	provider := newChatFailingProviderServer(t)
	
	// Configure with valid provider that passes validation
	putJSONConfig(t, h.server, `{"provider":"custom_openai","base_url":"`+provider.URL()+`","api_key":"sk-test","model":"gpt-4"}`)

	// Make the chat endpoint fail so StartRun fails
	provider.SetFailChat(true)

	// Send a chat message
	chatReq, _ := http.NewRequest(http.MethodPost, h.server.URL+"/api"+loc+"/chat", strings.NewReader("message=orphaned+message"))
	chatReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	chatReq.Header.Set("X-HTMX-Request", "true")
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
