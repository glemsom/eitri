package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWalkWorkspace_Basic(t *testing.T) {
	dir := t.TempDir()
	files := []string{"a.go", "b.go", "sub/c.go"}
	for _, f := range files {
		fp := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(fp), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	var visited []string
	err := WalkWorkspace(dir, func(path, relPath string, d os.DirEntry) error {
		visited = append(visited, relPath)
		return nil
	}, "")
	if err != nil {
		t.Fatalf("WalkWorkspace: %v", err)
	}

	// All files should be visited
	expected := []string{"a.go", "b.go", "sub/c.go"}
	if len(visited) != len(expected) {
		t.Fatalf("got %d files, want %d: %v", len(visited), len(expected), visited)
	}
	for _, e := range expected {
		found := false
		for _, v := range visited {
			if v == e {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %q in visited, got %v", e, visited)
		}
	}
}

func TestWalkWorkspace_HiddenDirExclusion(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".hidden"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden", "secret.go"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "visible.go"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	var visited []string
	err := WalkWorkspace(dir, func(path, relPath string, d os.DirEntry) error {
		visited = append(visited, relPath)
		return nil
	}, "")
	if err != nil {
		t.Fatalf("WalkWorkspace: %v", err)
	}

	if len(visited) != 1 || visited[0] != "visible.go" {
		t.Errorf("expected only visible.go, got %v", visited)
	}
}

func TestWalkWorkspace_VendorExclusion(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "vendor"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vendor", "dep.go"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	var visited []string
	err := WalkWorkspace(dir, func(path, relPath string, d os.DirEntry) error {
		visited = append(visited, relPath)
		return nil
	}, "")
	if err != nil {
		t.Fatalf("WalkWorkspace: %v", err)
	}

	if len(visited) != 1 || visited[0] != "main.go" {
		t.Errorf("expected only main.go, got %v", visited)
	}
}

func TestWalkWorkspace_FilePatternFilter(t *testing.T) {
	dir := t.TempDir()
	files := []string{"a.go", "b.txt", "sub/c.go"}
	for _, f := range files {
		fp := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(fp), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	var visited []string
	err := WalkWorkspace(dir, func(path, relPath string, d os.DirEntry) error {
		visited = append(visited, relPath)
		return nil
	}, "*.go")
	if err != nil {
		t.Fatalf("WalkWorkspace: %v", err)
	}

	// Now *.go should match both a.go (root) and sub/c.go (nested)
	expected := []string{"a.go", "sub/c.go"}
	if len(visited) != len(expected) {
		t.Fatalf("got %v, want %v", visited, expected)
	}
	for i := range expected {
		if visited[i] != expected[i] {
			t.Errorf("visited[%d] = %q, want %q", i, visited[i], expected[i])
		}
	}
}

func TestWalkWorkspace_FilePatternPathPrefixed(t *testing.T) {
	dir := t.TempDir()
	files := []string{"a.go", "sub/c.go", "other/d.go"}
	for _, f := range files {
		fp := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(fp), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	var visited []string
	err := WalkWorkspace(dir, func(path, relPath string, d os.DirEntry) error {
		visited = append(visited, relPath)
		return nil
	}, "sub/*")
	if err != nil {
		t.Fatalf("WalkWorkspace: %v", err)
	}

	expected := []string{"sub/c.go"}
	if len(visited) != len(expected) {
		t.Fatalf("got %v, want %v", visited, expected)
	}
	for i := range expected {
		if visited[i] != expected[i] {
			t.Errorf("visited[%d] = %q, want %q", i, visited[i], expected[i])
		}
	}
}

func TestWalkWorkspace_EarlyStop(t *testing.T) {
	dir := t.TempDir()
	files := []string{"a.go", "b.go", "c.go"}
	for _, f := range files {
		fp := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(fp), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	var visited []string
	err := WalkWorkspace(dir, func(path, relPath string, d os.DirEntry) error {
		visited = append(visited, relPath)
		if len(visited) >= 2 {
			return &WalkStop{}
		}
		return nil
	}, "")
	if err != nil {
		t.Fatalf("WalkWorkspace: %v", err)
	}

	if len(visited) != 2 {
		t.Errorf("expected 2 files visited, got %d: %v", len(visited), visited)
	}
}

func TestWalkWorkspace_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	var visited []string
	err := WalkWorkspace(dir, func(path, relPath string, d os.DirEntry) error {
		visited = append(visited, relPath)
		return nil
	}, "")
	if err != nil {
		t.Fatalf("WalkWorkspace: %v", err)
	}
	if len(visited) != 0 {
		t.Errorf("expected 0 files, got %d", len(visited))
	}
}

func TestWalkWorkspace_NonExistentRoot(t *testing.T) {
	err := WalkWorkspace("/nonexistent/path", func(path, relPath string, d os.DirEntry) error {
		return nil
	}, "")
	if err == nil {
		t.Fatal("expected error for non-existent root")
	}
}

func TestWalkWorkspace_NestedHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	// Create visible/nested/.hidden
	visibleNested := filepath.Join(dir, "visible", ".hidden")
	if err := os.MkdirAll(visibleNested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(visibleNested, "secret.go"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	// File in visible/ but not in .hidden
	if err := os.WriteFile(filepath.Join(dir, "visible", "ok.go"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	var visited []string
	err := WalkWorkspace(dir, func(path, relPath string, d os.DirEntry) error {
		visited = append(visited, relPath)
		return nil
	}, "")
	if err != nil {
		t.Fatalf("WalkWorkspace: %v", err)
	}

	if len(visited) != 1 || visited[0] != "visible/ok.go" {
		t.Errorf("expected only visible/ok.go, got %v", visited)
	}
}
