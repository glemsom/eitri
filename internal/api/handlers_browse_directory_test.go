package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/api"
)

func TestBrowseDirectory_DefaultPath(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/browse-directory")
	if err != nil {
		t.Fatalf("GET /api/browse-directory failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result api.BrowseDirectoryResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	// Default should be $HOME, which exists
	if result.CurrentPath == "" {
		t.Error("current_path should not be empty")
	}
	if len(result.Breadcrumbs) == 0 {
		t.Error("breadcrumbs should not be empty")
	}
}

func TestBrowseDirectory_ValidPath(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	// Create temp dir with subdirectories
	baseDir := t.TempDir()
	subDir1 := filepath.Join(baseDir, "sub1")
	subDir2 := filepath.Join(baseDir, "sub2")
	os.MkdirAll(subDir1, 0755)
	os.MkdirAll(subDir2, 0755)
	// Create a file (should not appear in listing)
	os.WriteFile(filepath.Join(baseDir, "file.txt"), []byte("hello"), 0644)
	// Create a hidden directory (should not appear in listing)
	os.MkdirAll(filepath.Join(baseDir, ".hidden"), 0755)

	resp, err := http.Get(server.URL + "/api/browse-directory?path=" + baseDir)
	if err != nil {
		t.Fatalf("GET /api/browse-directory failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result api.BrowseDirectoryResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	if result.CurrentPath != baseDir {
		t.Errorf("current_path = %q, want %q", result.CurrentPath, baseDir)
	}

	// Should have exactly 2 directories (sub1, sub2), sorted
	if len(result.Directories) != 2 {
		t.Errorf("expected 2 directories, got %d: %+v", len(result.Directories), result.Directories)
	}

	// Check directories are sorted
	if len(result.Directories) >= 2 {
		if result.Directories[0].Name != "sub1" || result.Directories[1].Name != "sub2" {
			t.Errorf("directories not sorted: got %+v", result.Directories)
		}
	}

	// Check no hidden directories
	for _, d := range result.Directories {
		if strings.HasPrefix(d.Name, ".") {
			t.Errorf("hidden directory included: %q", d.Name)
		}
	}

	// Check no files
	for _, d := range result.Directories {
		if d.Name == "file.txt" {
			t.Errorf("file included in directory listing: %q", d.Name)
		}
	}

	// Parent path should be set (baseDir's parent)
	if result.ParentPath == "" {
		t.Error("parent_path should not be empty for non-root directory")
	}
}

func TestBrowseDirectory_InvalidPath(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/browse-directory?path=%2Fnonexistent")
	if err != nil {
		t.Fatalf("GET /api/browse-directory failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var errResp map[string]string
	if err := json.Unmarshal(body, &errResp); err != nil {
		t.Fatalf("failed to parse JSON error response: %v", err)
	}
	if errResp["error"] == "" {
		t.Error("expected error message in response")
	}
}

func TestBrowseDirectory_RelativePath(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/browse-directory?path=relative%2Fpath")
	if err != nil {
		t.Fatalf("GET /api/browse-directory failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", resp.StatusCode)
	}
}

func TestBrowseDirectory_PathIsFile(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	// Create a file
	baseDir := t.TempDir()
	filePath := filepath.Join(baseDir, "test.txt")
	os.WriteFile(filePath, []byte("hello"), 0644)

	resp, err := http.Get(server.URL + "/api/browse-directory?path=" + filePath)
	if err != nil {
		t.Fatalf("GET /api/browse-directory failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 for file path, got %d", resp.StatusCode)
	}
}

func TestBrowseDirectory_RootPath(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/browse-directory?path=%2F")
	if err != nil {
		t.Fatalf("GET /api/browse-directory failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result api.BrowseDirectoryResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	if result.CurrentPath != "/" {
		t.Errorf("current_path = %q, want %q", result.CurrentPath, "/")
	}
	if result.ParentPath != "" {
		t.Errorf("parent_path should be empty for root, got %q", result.ParentPath)
	}
	if len(result.Breadcrumbs) == 0 {
		t.Error("breadcrumbs should not be empty for root")
	}
	if result.Breadcrumbs[0].Label != "/" {
		t.Errorf("first breadcrumb label = %q, want %q", result.Breadcrumbs[0].Label, "/")
	}
}

func TestBrowseDirectory_Breadcrumbs(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	baseDir := t.TempDir()
	// Create a nested path for breadcrumb testing
	nestedDir := filepath.Join(baseDir, "a", "b", "c")
	os.MkdirAll(nestedDir, 0755)

	resp, err := http.Get(server.URL + "/api/browse-directory?path=" + nestedDir)
	if err != nil {
		t.Fatalf("GET /api/browse-directory failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result api.BrowseDirectoryResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	// Breadcrumbs should have at least 4 entries: /, baseDir basename, a, b, c
	if len(result.Breadcrumbs) < 4 {
		t.Errorf("expected at least 4 breadcrumbs, got %d: %+v", len(result.Breadcrumbs), result.Breadcrumbs)
	}

	// Last breadcrumb should be "c"
	lastCrumb := result.Breadcrumbs[len(result.Breadcrumbs)-1]
	if lastCrumb.Label != "c" {
		t.Errorf("last breadcrumb label = %q, want %q", lastCrumb.Label, "c")
	}
	if lastCrumb.Path != nestedDir {
		t.Errorf("last breadcrumb path = %q, want %q", lastCrumb.Path, nestedDir)
	}
}

func TestBrowseDirectory_ContentType(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	baseDir := t.TempDir()
	resp, err := http.Get(server.URL + "/api/browse-directory?path=" + baseDir)
	if err != nil {
		t.Fatalf("GET /api/browse-directory failed: %v", err)
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}
