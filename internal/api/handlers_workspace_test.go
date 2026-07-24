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

// TestHandleSessionDirectoryBrowser_SessionFound verifies that the directory
// browser overlay renders for a valid session.
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

	resp, err := http.Get(server.URL + "/api/sessions/" + sess.ID + "/directory-browser?path=" + url.QueryEscape(workspace))
	if err != nil {
		t.Fatalf("GET /api/sessions/{id}/directory-browser failed: %v", err)
	}
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
	resp, err := http.Get(server.URL + "/api/sessions/" + sess.ID + "/directory-browser")
	if err != nil {
		t.Fatalf("GET /api/sessions/{id}/directory-browser failed: %v", err)
	}
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

// TestHandleSessionDirectoryBrowser_SessionNotFound returns 404 for
// non-existent session.
func TestHandleSessionDirectoryBrowser_SessionNotFound(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	resp, err := http.Get(server.URL + "/api/sessions/nonexistent/directory-browser")
	if err != nil {
		t.Fatalf("GET /api/sessions/{id}/directory-browser failed: %v", err)
	}
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
	resp, err := http.Get(server.URL + "/api/sessions/" + sess.ID + "/directory-browser?path=relative/path")
	if err != nil {
		t.Fatalf("GET /api/sessions/{id}/directory-browser failed: %v", err)
	}
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

	resp, err := http.Get(server.URL + "/api/sessions/" + sess.ID + "/directory-browser?path=" + url.QueryEscape(workspace))
	if err != nil {
		t.Fatalf("GET /api/sessions/{id}/directory-browser failed: %v", err)
	}
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
