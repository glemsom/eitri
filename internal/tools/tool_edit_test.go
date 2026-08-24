package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditDescriptionGuidance(t *testing.T) {
	t.Parallel()
	desc := (&editTool{}).Description()
	folded := strings.ToLower(desc)
	for _, want := range []string{
		"occur exactly once",  // old_string must occur exactly once
		"widen",               // widen old_string with surrounding context
		"surrounding context", // context = enclosing signature / neighbouring line
		"fresh read",          // base old_string on a fresh read of the file
		"writable root",       // target inside a writable root
		"hard error",          // zero/multi matches are a hard error
		"no silent partial",   // never partially apply
	} {
		if !strings.Contains(folded, want) {
			t.Fatalf("edit description missing %q: %s", want, desc)
		}
	}
}

func TestEditAtomicWrite(t *testing.T) {
	t.Parallel()
	r, ws := newTestRegistry(t, nil)
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	// Make the directory read-only so creating the temp file fails; the
	// target must be untouched and no partial state may appear.
	if err := os.Chmod(ws, 0o500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	defer os.Chmod(ws, 0o700) //nolint:errcheck — restore for cleanup

	if _, err := r.Run(context.Background(), "edit", argMap("path", path, "old_string", "keep", "new_string", "drop")); err == nil {
		t.Fatal("edit in read-only dir error = nil, want hard error")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep me" {
		t.Fatalf("after failed edit = %q err=%v, want original content intact", data, err)
	}
}

func TestEditAtomicWriteLeavesNoTempFiles(t *testing.T) {
	t.Parallel()
	r, ws := newTestRegistry(t, nil)
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("alpha beta"), 0o640); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if _, err := r.Run(context.Background(), "edit", argMap("path", path, "old_string", "beta", "new_string", "BETA")); err != nil {
		t.Fatalf("edit error = %v, want nil", err)
	}
	entries, err := os.ReadDir(ws)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "f.txt" {
			t.Fatalf("stray file left behind after edit: %s", e.Name())
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode after edit = %v, want original 0640 preserved", info.Mode().Perm())
	}
}
