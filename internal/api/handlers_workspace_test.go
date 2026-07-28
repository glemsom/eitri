package api_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/session"
)

func getWorkspaceBrowser(t *testing.T, rawURL, browserID string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if browserID != "" {
		req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/sessions/{id}/directory-browser failed: %v", err)
	}
	return resp
}

// TestHandleSessionDirectoryBrowser_SessionFound verifies that the directory
// browser overlay renders for a valid session owner.
func TestHandleSessionDirectoryBrowser_SessionFound(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	browserID := "test-browser-dir"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// Create a subdirectory to browse
	subDir := filepath.Join(workspace, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	resp := getWorkspaceBrowser(t, server.URL+"/api/sessions/"+sess.ID+"/directory-browser?path="+url.QueryEscape(workspace), browserID)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])

	if !strings.Contains(content, "subdir") {
		t.Errorf("directory listing missing 'subdir': %s", content)
	}
	if !strings.Contains(content, "DirectoryBrowser") {
		t.Errorf("response missing expected directory browser HTML: %s", content)
	}
}

// TestHandleSessionDirectoryBrowser_DefaultPath uses the session's workspace
// when no explicit path is provided.
func TestHandleSessionDirectoryBrowser_DefaultPath(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	// Create a subdirectory inside the workspace so we have something to list
	subDir := filepath.Join(workspace, "projects")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	browserID := "test-browser-default"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// No path parameter — should default to session's workspace
	resp := getWorkspaceBrowser(t, server.URL+"/api/sessions/"+sess.ID+"/directory-browser", browserID)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])

	if !strings.Contains(content, "projects") {
		t.Errorf("directory listing missing 'projects' subdirectory: %s", content)
	}
}

// TestHandleSessionDirectoryBrowser_MissingBrowserID returns 401 when an
// existing session is requested without a browser ownership cookie.
func TestHandleSessionDirectoryBrowser_MissingBrowserID(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	sess, err := sessionMgr.Create("test-browser-missing")
	if err != nil {
		t.Fatal(err)
	}

	resp := getWorkspaceBrowser(t, server.URL+"/api/sessions/"+sess.ID+"/directory-browser", "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", resp.StatusCode)
	}
}

// TestHandleSessionDirectoryBrowser_WrongBrowserID returns 404 when browser
// ownership does not match the session.
func TestHandleSessionDirectoryBrowser_WrongBrowserID(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	sess, err := sessionMgr.Create("test-browser-owner")
	if err != nil {
		t.Fatal(err)
	}

	resp := getWorkspaceBrowser(t, server.URL+"/api/sessions/"+sess.ID+"/directory-browser", "different-browser")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

// TestHandleSessionDirectoryBrowser_SessionNotFound returns 404 for
// non-existent session when request has browser ownership cookie.
func TestHandleSessionDirectoryBrowser_SessionNotFound(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	resp := getWorkspaceBrowser(t, server.URL+"/api/sessions/nonexistent/directory-browser", "test-browser-notfound")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

// TestHandleSessionDirectoryBrowser_InvalidPath returns 400 for invalid path.
func TestHandleSessionDirectoryBrowser_InvalidPath(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	browserID := "test-browser-badpath"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// Relative path should be rejected
	resp := getWorkspaceBrowser(t, server.URL+"/api/sessions/"+sess.ID+"/directory-browser?path=relative/path", browserID)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 for relative path, got %d", resp.StatusCode)
	}
}

// TestHandleSessionDirectoryBrowser_Breadcrumbs checks that breadcrumbs
// are rendered in the directory browser overlay.
func TestHandleSessionDirectoryBrowser_Breadcrumbs(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	browserID := "test-browser-crumbs"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	resp := getWorkspaceBrowser(t, server.URL+"/api/sessions/"+sess.ID+"/directory-browser?path="+url.QueryEscape(workspace), browserID)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])

	// Should indicate the current path in the breadcrumb or heading
	if !strings.Contains(content, filepath.Base(workspace)) {
		t.Errorf("breadcrumbs missing workspace basename %q: %s", filepath.Base(workspace), content)
	}
}
