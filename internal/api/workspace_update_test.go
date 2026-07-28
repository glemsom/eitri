package api_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/session"
)

func newWorkspaceUpdateRequest(t *testing.T, serverURL, sessionID, browserID, body, contentType string) *http.Request {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/sessions/"+sessionID+"/workspace", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("HX-Request", "true")
	if browserID != "" {
		req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})
	}
	return req
}

// TestHandleUpdateWorkspace_FormEncoded verifies that the workspace update
// handler accepts URL-encoded form data (as sent by HTMX hx-vals) from the
// session owner.
// See: hx-vals sends URL-encoded form data, not JSON.
func TestHandleUpdateWorkspace_FormEncoded(t *testing.T) {
	workspace := t.TempDir()
	newWorkspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	// Create a session with the expected browser_id
	browserID := "test-browser"

	// Use the session manager directly to get a session with our browser ID
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// Verify initial workspace
	sessCheck := sessionMgr.Get(sess.ID)
	if sessCheck.Workspace != workspace {
		t.Fatalf("initial workspace = %q, want %q", sessCheck.Workspace, workspace)
	}

	// POST with URL-encoded form data (simulating HTMX hx-vals)
	formData := url.Values{}
	formData.Set("path", newWorkspace)
	req := newWorkspaceUpdateRequest(t, server.URL, sess.ID, browserID, formData.Encode(), "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/sessions/{id}/workspace status = %d, want 200", resp.StatusCode)
	}

	// Verify HX-Redirect header
	redirect := resp.Header.Get("HX-Redirect")
	if redirect == "" {
		t.Error("missing HX-Redirect header")
	} else if !strings.Contains(redirect, "/sessions/"+sess.ID) {
		t.Errorf("HX-Redirect = %q, want redirect to session page", redirect)
	}

	// Verify workspace was updated
	sessCheck = sessionMgr.Get(sess.ID)
	if sessCheck.Workspace != newWorkspace {
		t.Errorf("workspace after update = %q, want %q", sessCheck.Workspace, newWorkspace)
	}
}

// TestHandleUpdateWorkspace_JSON verifies backward compatibility with JSON body.
func TestHandleUpdateWorkspace_JSON(t *testing.T) {
	workspace := t.TempDir()
	newWorkspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	browserID := "test-browser-json"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// POST with JSON body (API client style)
	jsonBody := `{"path":"` + newWorkspace + `"}`
	req := newWorkspaceUpdateRequest(t, server.URL, sess.ID, browserID, jsonBody, "application/json")

	// Use a client that doesn't follow redirects so we can inspect the response
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/sessions/{id}/workspace status = %d, want 200", resp.StatusCode)
	}

	// Verify HX-Redirect header
	redirect := resp.Header.Get("HX-Redirect")
	if redirect == "" {
		t.Error("missing HX-Redirect header")
	} else if !strings.Contains(redirect, "/sessions/"+sess.ID) {
		t.Errorf("HX-Redirect = %q, want redirect to session page", redirect)
	}

	// Verify workspace was updated
	sessCheck := sessionMgr.Get(sess.ID)
	if sessCheck.Workspace != newWorkspace {
		t.Errorf("workspace after update = %q, want %q", sessCheck.Workspace, newWorkspace)
	}
}

// TestHandleUpdateWorkspace_MissingBrowserID rejects workspace updates without
// a browser ownership cookie.
func TestHandleUpdateWorkspace_MissingBrowserID(t *testing.T) {
	workspace := t.TempDir()
	newWorkspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	sess, err := sessionMgr.Create("test-browser-missing-update")
	if err != nil {
		t.Fatal(err)
	}

	formData := url.Values{}
	formData.Set("path", newWorkspace)
	req := newWorkspaceUpdateRequest(t, server.URL, sess.ID, "", formData.Encode(), "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST without browser_id status = %d, want 401", resp.StatusCode)
	}

	sessCheck := sessionMgr.Get(sess.ID)
	if sessCheck.Workspace != workspace {
		t.Errorf("workspace after rejected update = %q, want %q", sessCheck.Workspace, workspace)
	}
}

// TestHandleUpdateWorkspace_WrongBrowserID returns 404 when browser ownership
// does not match the session.
func TestHandleUpdateWorkspace_WrongBrowserID(t *testing.T) {
	workspace := t.TempDir()
	newWorkspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	sess, err := sessionMgr.Create("test-browser-update-owner")
	if err != nil {
		t.Fatal(err)
	}

	formData := url.Values{}
	formData.Set("path", newWorkspace)
	req := newWorkspaceUpdateRequest(t, server.URL, sess.ID, "different-browser", formData.Encode(), "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST with wrong browser_id status = %d, want 404", resp.StatusCode)
	}

	sessCheck := sessionMgr.Get(sess.ID)
	if sessCheck.Workspace != workspace {
		t.Errorf("workspace after rejected update = %q, want %q", sessCheck.Workspace, workspace)
	}
}

// TestHandleUpdateWorkspace_EmptyPath verifies validation rejects empty path.
func TestHandleUpdateWorkspace_EmptyPath(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	browserID := "test-browser-empty"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// POST with empty path
	formData := url.Values{}
	formData.Set("path", "")
	req := newWorkspaceUpdateRequest(t, server.URL, sess.ID, browserID, formData.Encode(), "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST with empty path status = %d, want 400", resp.StatusCode)
	}
}

// TestHandleUpdateWorkspace_NonexistentPath verifies validation rejects non-existent path.
func TestHandleUpdateWorkspace_NonexistentPath(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	browserID := "test-browser-nonexist"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	formData := url.Values{}
	formData.Set("path", "/nonexistent/path/that/does/not/exist")
	req := newWorkspaceUpdateRequest(t, server.URL, sess.ID, browserID, formData.Encode(), "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST with nonexistent path status = %d, want 400", resp.StatusCode)
	}
}
