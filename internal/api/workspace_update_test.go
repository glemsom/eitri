package api_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/session"
)

// TestHandleUpdateWorkspace_FormEncoded verifies that the workspace update
// handler accepts URL-encoded form data (as sent by HTMX hx-vals).
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
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/sessions/"+sess.ID+"/workspace", strings.NewReader(formData.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Simulate HTMX request
	req.Header.Set("HX-Request", "true")

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
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/sessions/"+sess.ID+"/workspace", strings.NewReader(jsonBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HX-Request", "true")

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
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/sessions/"+sess.ID+"/workspace", strings.NewReader(formData.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")

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
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/sessions/"+sess.ID+"/workspace", strings.NewReader(formData.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST with nonexistent path status = %d, want 400", resp.StatusCode)
	}
}
