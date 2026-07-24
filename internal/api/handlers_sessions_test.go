package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/session"
)

// ————— handleRoot —————

func TestHandleRoot_NoCookie_CreatesSessionAndRedirects(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	client := noRedirectClient()

	// First request without browser cookie
	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer resp.Body.Close()

	// Should be a redirect
	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected status 302, got %d", resp.StatusCode)
	}

	// Should set a browser_id cookie
	var browserCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	if browserCookie == nil {
		t.Fatal("browser_id cookie not set on first request")
	}
	if browserCookie.Value == "" {
		t.Fatal("browser_id cookie value is empty")
	}

	// Should redirect to a session
	location := resp.Header.Get("Location")
	if location == "" {
		t.Fatal("expected redirect Location header")
	}
	if !strings.HasPrefix(location, "/sessions/") {
		t.Errorf("expected redirect to /sessions/{id}, got %q", location)
	}
}

func TestHandleRoot_WithCookie_RedirectsToLastActiveSession(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	client := noRedirectClient()

	// Create a session via the root endpoint to get a cookie
	sessionID1, browserCookie := createSessionForBrowser(t, server, client)

	// Create a second session
	req2, _ := http.NewRequest("POST", server.URL+"/api/sessions", nil)
	req2.AddCookie(browserCookie)
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("POST /api/sessions failed: %v", err)
	}
	resp2.Body.Close()

	// Now hitting / with the cookie should redirect to the last active session
	// (which is session 2 since we just created it)
	req3, _ := http.NewRequest("GET", server.URL+"/", nil)
	req3.AddCookie(browserCookie)

	resp3, err := client.Do(req3)
	if err != nil {
		t.Fatalf("GET / with cookie failed: %v", err)
	}
	defer resp3.Body.Close()

	location := resp3.Header.Get("Location")
	if location == "" {
		t.Fatal("expected redirect Location header")
	}
	if !strings.HasPrefix(location, "/sessions/") {
		t.Errorf("expected redirect to /sessions/{id}, got %q", location)
	}
	// Should be different from sessionID1
	redirectedID := strings.TrimPrefix(location, "/sessions/")
	if redirectedID == sessionID1 {
		t.Errorf("expected redirect to last active session (not %s), got %s", sessionID1, redirectedID)
	}
}

func TestHandleRoot_SessionCapReached(t *testing.T) {
	workspace := t.TempDir()
	// Manager with very small cap
	sessionMgr := session.NewManager(1, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	// Fill the cap
	_, err := sessionMgr.Create("browser-cap-test")
	if err != nil {
		t.Fatal(err)
	}

	// Second request (different browser) should hit cap
	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", resp.StatusCode)
	}
}

// ————— ensureBrowserID —————

func TestEnsureBrowserID_CreatesCookie(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	client := noRedirectClient()

	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer resp.Body.Close()

	var browserCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	if browserCookie == nil {
		t.Fatal("browser_id cookie not set")
	}
	if browserCookie.Value == "" {
		t.Fatal("browser_id cookie value is empty")
	}
	if !browserCookie.HttpOnly {
		t.Error("browser_id cookie should be HttpOnly")
	}
}

func TestEnsureBrowserID_ReturnsExistingCookie(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	client := noRedirectClient()

	// First request sets cookie
	resp1, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	resp1.Body.Close()

	var browserCookie *http.Cookie
	for _, c := range resp1.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	if browserCookie == nil {
		t.Fatal("browser_id cookie not set on first request")
	}

	// Second request with same cookie
	req2, _ := http.NewRequest("GET", server.URL+"/", nil)
	req2.AddCookie(browserCookie)

	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("GET / with cookie failed: %v", err)
	}
	defer resp2.Body.Close()

	// Should not set a new Set-Cookie header (browser_id already exists)
	for _, c := range resp2.Cookies() {
		if c.Name == "browser_id" && c.Value != browserCookie.Value {
			t.Errorf("browser_id changed from %s to %s", browserCookie.Value, c.Value)
		}
	}
}

// ————— browserIDFromRequest —————

// browserIDFromRequest is tested implicitly through the session flows above.

// ————— chatPathForRequest —————

func TestChatPathForRequest_NoCookieReturnsRoot(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	// Hitting the settings page which uses chatPathForRequest for the back link
	resp, err := http.Get(server.URL + "/settings")
	if err != nil {
		t.Fatalf("GET /settings failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

// ————— hxRedirect —————

func TestHxRedirect_HTMXRequest(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	client := noRedirectClient()

	browserID := "test-hx-redirect"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// Delete session triggers hxRedirect
	req, _ := http.NewRequest("DELETE", server.URL+"/api/sessions/"+sess.ID, nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})
	req.Header.Set("HX-Request", "true")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/sessions/{id} failed: %v", err)
	}
	defer resp.Body.Close()

	// With HX-Request header, should get HX-Redirect header
	hxRedirect := resp.Header.Get("HX-Redirect")
	if hxRedirect == "" {
		t.Error("expected HX-Redirect header for HTMX request")
	}

	// Should redirect to root since no other sessions exist
	if hxRedirect != "/" && !strings.HasPrefix(hxRedirect, "/sessions/") {
		t.Errorf("unexpected HX-Redirect value: %q", hxRedirect)
	}
}

func TestHxRedirect_StandardRequest(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	client := noRedirectClient()

	browserID := "test-std-redirect"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// Delete session without HX-Request header
	req, _ := http.NewRequest("DELETE", server.URL+"/api/sessions/"+sess.ID, nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/sessions/{id} failed: %v", err)
	}
	defer resp.Body.Close()

	// Without HX-Request, should get standard 302 redirect
	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected status 302, got %d", resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	if location == "" {
		t.Error("expected Location header for standard redirect")
	}
}

// ————— handleCreateSession —————

func TestHandleCreateSession_CreatesNewSession(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	client := noRedirectClient()

	// Create session via root to get cookie
	_, browserCookie := createSessionForBrowser(t, server, client)

	// Create another session via API
	req, _ := http.NewRequest("POST", server.URL+"/api/sessions", nil)
	req.AddCookie(browserCookie)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions failed: %v", err)
	}
	defer resp.Body.Close()

	// Should redirect via hxRedirect (no HX-Request header, so standard redirect)
	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected status 302, got %d", resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	if location == "" || !strings.HasPrefix(location, "/sessions/") {
		t.Errorf("expected redirect to /sessions/{id}, got Location=%q", location)
	}
}

func TestHandleCreateSession_SessionCapReached(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(1, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	// Fill the cap via a different browser
	sessionMgr.Create("other-browser")

	// Try to create a session
	browserID := "test-cap-reached"
	req, _ := http.NewRequest("POST", server.URL+"/api/sessions", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", resp.StatusCode)
	}
}

// ————— handleGetSession —————

func TestHandleGetSession_ValidSession(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	browserID := "test-get-session"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("GET", server.URL+"/sessions/"+sess.ID, nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /sessions/{id} failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	content := string(body)
	if !strings.Contains(content, "chat-view") && !strings.Contains(content, "chat") {
		t.Errorf("response missing chat page content: %s", content[:min(len(content), 200)])
	}
}

func TestHandleGetSession_SessionNotFound(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	client := noRedirectClient()

	browserID := "test-get-nonexistent"

	req, _ := http.NewRequest("GET", server.URL+"/sessions/nonexistent-id", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /sessions/nonexistent-id failed: %v", err)
	}
	defer resp.Body.Close()

	// Should redirect to / via hxRedirect (since session is nil)
	location := resp.Header.Get("Location")
	hxRedirect := resp.Header.Get("HX-Redirect")
	if location != "/" && hxRedirect != "/" {
		t.Errorf("expected redirect to /, got Location=%q HX-Redirect=%q", location, hxRedirect)
	}
}

func TestHandleGetSession_OwnershipMismatch(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	sess, err := sessionMgr.Create("owner-browser")
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("GET", server.URL+"/sessions/"+sess.ID, nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: "different-browser"})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /sessions/{id} failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

func TestHandleGetSession_WithoutCookie_GetsOne(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	sess, err := sessionMgr.Create("test-browser")
	if err != nil {
		t.Fatal(err)
	}

	// Request without browser_id cookie — handler calls ensureBrowserID
	req, _ := http.NewRequest("GET", server.URL+"/sessions/"+sess.ID, nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /sessions/{id} failed: %v", err)
	}
	defer resp.Body.Close()

	// Since browser_id doesn't match session owner, should get 404
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404 for ownership mismatch, got %d", resp.StatusCode)
	}
}

// ————— handleDeleteSession —————

func TestHandleDeleteSession_DeletesAndRedirects(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	client := noRedirectClient()

	browserID := "test-delete-session"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("DELETE", server.URL+"/api/sessions/"+sess.ID, nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/sessions/{id} failed: %v", err)
	}
	defer resp.Body.Close()

	// Should redirect to a new session (handler creates one when last is deleted)
	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected status 302, got %d", resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	if !strings.HasPrefix(location, "/sessions/") {
		t.Errorf("expected redirect to /sessions/{id}, got %q", location)
	}

	// Session should be deleted
	if deleted := sessionMgr.Get(sess.ID); deleted != nil {
		t.Error("session was not deleted from manager")
	}
}

func TestHandleDeleteSession_NoBrowserID(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	sess, err := sessionMgr.Create("some-browser")
	if err != nil {
		t.Fatal(err)
	}

	// Delete without cookie
	req, _ := http.NewRequest("DELETE", server.URL+"/api/sessions/"+sess.ID, nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/sessions/{id} failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", resp.StatusCode)
	}
}

func TestHandleDeleteSession_OwnershipMismatch(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	sess, err := sessionMgr.Create("owner-browser")
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("DELETE", server.URL+"/api/sessions/"+sess.ID, nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: "different-browser"})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/sessions/{id} failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

func TestHandleDeleteSession_NonExistentSession(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	client := noRedirectClient()

	browserID := "test-delete-nonexistent"

	req, _ := http.NewRequest("DELETE", server.URL+"/api/sessions/nonexistent", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/sessions/nonexistent failed: %v", err)
	}
	defer resp.Body.Close()

	// Should redirect to /
	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected status 302, got %d", resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	if location != "/" {
		t.Errorf("expected redirect to /, got %q", location)
	}
}

func TestHandleDeleteSession_RedirectsToNextSession(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	client := noRedirectClient()

	browserID := "test-delete-multi"
	sess1, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}
	sess2, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// Delete the first session
	req, _ := http.NewRequest("DELETE", server.URL+"/api/sessions/"+sess1.ID, nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/sessions/{id} failed: %v", err)
	}
	defer resp.Body.Close()

	// Should redirect to the remaining session
	location := resp.Header.Get("Location")
	if location != "/sessions/"+sess2.ID {
		t.Errorf("expected redirect to /sessions/%s, got %q", sess2.ID, location)
	}
}
