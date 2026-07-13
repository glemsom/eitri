package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBuild(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "eitri")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	// Verify binary exists and is executable
	if _, err := exec.LookPath(binary); err != nil {
		t.Fatalf("binary not found after build: %v", err)
	}
}
