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

func TestEditEmptyOldString(t *testing.T) {
	t.Parallel()
	r, ws := newTestRegistry(t, nil)
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	_, err := r.Run(context.Background(), "edit", argMap("path", path, "old_string", "", "new_string", "x"))
	if err == nil {
		t.Fatal("empty old_string error = nil, want descriptive error")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("empty old_string error = %v, want \"must not be empty\" message", err)
	}
	data, rerr := os.ReadFile(path)
	if rerr != nil || string(data) != "content" {
		t.Fatalf("after empty old_string edit = %q err=%v, want file untouched", data, rerr)
	}
}

func TestEditNoOpEdit(t *testing.T) {
	t.Parallel()
	r, ws := newTestRegistry(t, nil)
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("same text"), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	_, err := r.Run(context.Background(), "edit", argMap("path", path, "old_string", "same text", "new_string", "same text"))
	if err == nil {
		t.Fatal("no-op edit error = nil, want error")
	}
	if !strings.Contains(err.Error(), "no-op") {
		t.Fatalf("no-op edit error = %v, want \"no-op\" message", err)
	}
	data, rerr := os.ReadFile(path)
	if rerr != nil || string(data) != "same text" {
		t.Fatalf("after no-op edit = %q err=%v, want file untouched", data, rerr)
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

func TestEditTrimmedFallbackApplies(t *testing.T) {
	t.Parallel()
	r, ws := newTestRegistry(t, nil)
	path := filepath.Join(ws, "f.txt")
	content := "func main() {\n\treturn   value\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if _, err := r.Run(context.Background(), "edit", argMap(
		"path", path,
		"old_string", "func main() {\n\treturn value\n}",
		"new_string", "func main() {\n\treturn other\n}",
	)); err != nil {
		t.Fatalf("trimmed fallback edit error = %v, want nil", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "func main() {\n\treturn other\n}\n"
	if string(data) != want {
		t.Fatalf("after fallback edit = %q, want %q", data, want)
	}
}

func TestEditTrimmedFallbackAmbiguousFails(t *testing.T) {
	t.Parallel()
	r, ws := newTestRegistry(t, nil)
	path := filepath.Join(ws, "f.txt")
	content := "\treturn  one\n\n\treturn  one\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	_, err := r.Run(context.Background(), "edit", argMap(
		"path", path,
		"old_string", "return one",
		"new_string", "return X",
	))
	if err == nil {
		t.Fatal("ambiguous trimmed match error = nil, want hard error")
	}
	data, rerr := os.ReadFile(path)
	if rerr != nil || string(data) != content {
		t.Fatalf("after ambiguous edit = %q err=%v, want file untouched", data, rerr)
	}
}

func TestEditTrimmedFallbackNoMatchStillErrors(t *testing.T) {
	t.Parallel()
	r, ws := newTestRegistry(t, nil)
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	_, err := r.Run(context.Background(), "edit", argMap(
		"path", path,
		"old_string", "alpha\ngamma",
		"new_string", "x",
	))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("no-match error = %v, want \"not found\"", err)
	}
}

func TestEditTrimmedFallbackPreservesCRLF(t *testing.T) {
	t.Parallel()
	r, ws := newTestRegistry(t, nil)
	path := filepath.Join(ws, "f.txt")
	content := "first\r\n\tsecond   line\r\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if _, err := r.Run(context.Background(), "edit", argMap(
		"path", path,
		"old_string", "first\n\tsecond line",
		"new_string", "FIRST\nSECOND",
	)); err != nil {
		t.Fatalf("CRLF fallback edit error = %v, want nil", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "FIRST\r\nSECOND\r\n"
	if string(data) != want {
		t.Fatalf("after CRLF fallback = %q, want %q", data, want)
	}
}
