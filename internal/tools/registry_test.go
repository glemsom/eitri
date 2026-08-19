package tools

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// newTestRegistry builds a registry against a temp workspace with an injectable
// runner for bash, mirroring the per-session wiring the app performs at boot.
func newTestRegistry(t *testing.T, rr Runner) (*Registry, string) {
	t.Helper()
	// Workspace must not live under /tmp or the sandbox/temp /tmp remap would
	// translate it; use a home-relative path like a real project dir.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	top := filepath.Join(home, ".eitri-test-reg-"+strings.ReplaceAll(t.Name(), "/", "_"))
	ws := filepath.Join(top, "proj")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(top) })
	if rr == nil {
		rr = &recordingRunner{out: &Output{Stdout: "ls-output\n"}}
	}
	r := NewRegistry(Deps{
		Workspace: ws,
		TempHost:  filepath.Join(t.TempDir(), "eitri-g"),
		GUID:      GUID("tguid"),
		Runner:    rr,
	})
	return r, ws
}

// TestReadSchemaPlainOptionalTypes locks that the optional start_line/end_line
// are plain {"type":"integer"} properties rather than nullable-union arrays:
// wire tolerance for omitted optionals (issue #396) makes the union form
// unnecessary, and the plain form is standard JSON-Schema a provider
// validator accepts.
func TestReadSchemaPlainOptionalTypes(t *testing.T) {
	// Schema() needs no validator, so the type can be exercised directly.
	schema := (&readTool{}).Schema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema: no properties map, got %T", schema["properties"])
	}
	for _, field := range []string{"start_line", "end_line"} {
		node, ok := props[field].(map[string]any)
		if !ok {
			t.Fatalf("schema property %q: expected plain object, got %T (%v)", field, props[field], props[field])
		}
		typ, ok := node["type"].(string)
		if !ok || typ != "integer" {
			t.Fatalf("schema property %q: expected plain type %q, got %v", field, "integer", node["type"])
		}
	}
}

// TestReadSchemaRequiredSubset locks the read schema's required-subset shape:
// only path is required (whole-file reads need no start_line/end_line
// placeholders) and additionalProperties is false. The line-range optionals
// are declared as ordinary integer properties that a caller may omit.
// (The plain-type form itself is guarded by TestReadSchemaPlainOptionalTypes.)
func TestReadSchemaRequiredSubset(t *testing.T) {
	schema := (&readTool{}).Schema()
	if schema["additionalProperties"] != false {
		t.Fatalf("schema additionalProperties = %v, want false", schema["additionalProperties"])
	}
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("schema required = %T (%v), want []string", schema["required"], schema["required"])
	}
	if len(required) != 1 || required[0] != "path" {
		t.Fatalf("schema required = %v, want only [\"path\"]", required)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema: no properties map, got %T", schema["properties"])
	}
	for _, field := range []string{"path", "start_line", "end_line"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("schema property %q missing", field)
		}
	}
}

// TestReadWholeFileRequiresOnlyPath verifies a whole-file read needs no
// start_line/end_line placeholders: read with just path dumps every line.
func TestReadWholeFileRequiresOnlyPath(t *testing.T) {
	r, ws := newTestRegistry(t, nil)
	path := filepath.Join(ws, "whole.txt")
	content := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// Omitted optionals — no null placeholders. The map has only path.
	args := map[string]any{"path": path}
	res, err := r.Run(context.Background(), "read", args)
	if err != nil {
		t.Fatalf("read (path only) error = %v, want nil", err)
	}
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("read (path only) output = %q, want whole-file content including %q", res.Text, want)
		}
	}
}

// TestReadToleratesNullOptionals verifies a model that still sends null for the
// line-range optionals is not broken: null falls to the whole-file default.
func TestReadToleratesNullOptionals(t *testing.T) {
	r, ws := newTestRegistry(t, nil)
	path := filepath.Join(ws, "null.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	args := map[string]any{"path": path, "start_line": nil, "end_line": nil}
	res, err := r.Run(context.Background(), "read", args)
	if err != nil {
		t.Fatalf("read (null optionals) error = %v, want nil", err)
	}
	if !strings.Contains(res.Text, "one") || !strings.Contains(res.Text, "two") {
		t.Fatalf("read (null optionals) output = %q, want whole-file content", res.Text)
	}
}

func argMap(kv ...string) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

// TestRegistryExposesTools locks the six-tool surface.
func TestRegistryExposesTools(t *testing.T) {
	r, _ := newTestRegistry(t, nil)
	got := map[string]bool{}
	for _, n := range r.Names() {
		got[n] = true
	}
	for _, want := range []string{"bash", "read", "write", "edit", "web_fetch", "open_in_browser"} {
		if !got[want] {
			t.Fatalf("registry missing tool %q; names = %v", want, r.Names())
		}
	}
}

// TestBashRunsInSandbox verifies the bash tool invokes the sandbox runner with
// the command and returns its combined output.
func TestBashRunsInSandbox(t *testing.T) {
	rr := &recordingRunner{out: &Output{Stdout: "$HOME\n"}}
	r, _ := newTestRegistry(t, rr)
	res, err := r.Run(context.Background(), "bash", argMap("command", "echo $HOME"))
	got := res.Text
	_ = got
	if err != nil {
		t.Fatalf("bash error = %v, want nil", err)
	}
	if got != "$HOME\n" {
		t.Fatalf("bash output = %q, want %q", got, "$HOME\n")
	}
}

// TestBashCompressesNoisyOutputAtBoundary verifies a noisy, high-volume bash
// read is compressed at the tool-result boundary: the output
// returned into the conversation is truncated with the explicit "+ more" marker
// and is strictly shorter, but the raw command can be re-run for recovery.
func TestBashCompressesNoisyOutputAtBoundary(t *testing.T) {
	var raw strings.Builder
	for i := 0; i < 400; i++ {
		raw.WriteString("src/file_")
		raw.WriteString(strconv.Itoa(i))
		raw.WriteString(".go          1234 bytes\n")
	}
	rr := &recordingRunner{out: &Output{Stdout: raw.String()}}
	r, _ := newTestRegistry(t, rr)
	res, err := r.Run(context.Background(), "bash", argMap("command", "ls -R ."))
	got := res.Text
	if err != nil {
		t.Fatalf("bash error = %v, want nil", err)
	}
	if !strings.Contains(got, " more") {
		t.Fatalf("compressed bash output missing explicit tail marker: %q", got)
	}
	if len(got) >= len(raw.String()) {
		t.Fatalf("compressed output not shorter: raw=%d bytes, got=%d bytes", len(raw.String()), len(got))
	}
	// Deterministic: re-running the same command returns the same compressed form.
	againRes, err := r.Run(context.Background(), "bash", argMap("command", "ls -R ."))
	again := againRes.Text
	if err != nil {
		t.Fatalf("bash second run error = %v, want nil", err)
	}
	if again != got {
		t.Fatalf("compression not deterministic: first=%q second=%q", got, again)
	}
}

// TestReadLineRange verifies line-range reads: lines before start omitted,
// range bounded, and the requested slice returned with content.
func TestReadLineRange(t *testing.T) {
	r, ws := newTestRegistry(t, nil)
	path := filepath.Join(ws, "f.txt")
	content := "l1\nl2\nl3\nl4\nl5\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write setup: %v", err)
	}
	res, err := r.Run(context.Background(), "read", argMap("path", path, "start_line", "2", "end_line", "4"))
	got := res.Text
	if err != nil {
		t.Fatalf("read error = %v, want nil", err)
	}
	if !strings.Contains(got, "l2") || !strings.Contains(got, "l3") || !strings.Contains(got, "l4") {
		t.Fatalf("read range output = %q, want lines 2-4", got)
	}
	if strings.Contains(got, "l1") || strings.Contains(got, "l5") {
		t.Fatalf("read range output = %q, should exclude lines 1 and 5", got)
	}
}

// TestReadResolvesRelativeWorkspacePath verifies read accepts a path relative
// to the workspace (bash's cwd), not only host-absolute paths. The model
// commonly emits "AGENTS.md" after a `ls`; it must resolve against the
// workspace root via the translator.
func TestReadResolvesRelativeWorkspacePath(t *testing.T) {
	r, ws := newTestRegistry(t, nil)
	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("hello\nworld\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	res, err := r.Run(context.Background(), "read", argMap("path", "AGENTS.md"))
	got := res.Text
	if err != nil {
		t.Fatalf("read relative path error = %v, want nil", err)
	}
	if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Fatalf("read output = %q, want file contents", got)
	}
}

// TestReadUsesSessionTempNamespace verifies a sandbox /tmp path is translated
// and read host-side from the session temp root.
func TestReadUsesSessionTempNamespace(t *testing.T) {
	r := NewRegistry(Deps{
		Workspace: "/home/u/proj",
		TempHost:  "/tmp/eitri-g",
		GUID:      GUID("g"),
		Runner:    &recordingRunner{},
	})
	// Place a file at the host temp root as the sandbox /tmp/x would materialize.
	if err := os.MkdirAll("/tmp/eitri-g", 0o700); err != nil {
		t.Fatalf("mkdir temp host: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll("/tmp/eitri-g") })
	if err := os.WriteFile("/tmp/eitri-g/data.txt", []byte("tmp-content"), 0o600); err != nil {
		t.Fatalf("write temp host: %v", err)
	}
	res, err := r.Run(context.Background(), "read", argMap("path", "/tmp/data.txt"))
	got := res.Text
	if err != nil {
		t.Fatalf("read /tmp error = %v, want nil", err)
	}
	if !strings.Contains(got, "tmp-content") {
		t.Fatalf("read output = %q, want tmp-content", got)
	}
}

// TestReadOutsideWritableRoots verifies read is not gated by the writable
// roots: a host file outside the workspace, extra writable paths, and session
// temp reads fine (the sandbox already exposes it via bash), and a missing
// file fails only on the genuine I/O error.
func TestReadOutsideWritableRoots(t *testing.T) {
	r, ws := newTestRegistry(t, nil)
	// Fixture lives outside the workspace and outside session temp: a sibling
	// dir of the workspace root under the test top dir, so it is outside every
	// writable root yet still host-readable.
	top := filepath.Dir(ws)
	outside := filepath.Join(top, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside-root-content\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	res, err := r.Run(context.Background(), "read", argMap("path", outside))
	if err != nil {
		t.Fatalf("read outside writable root error = %v, want nil", err)
	}
	if !strings.Contains(res.Text, "outside-root-content") {
		t.Fatalf("read output = %q, want file contents", res.Text)
	}
	if _, err := r.Run(context.Background(), "read", argMap("path", filepath.Join(top, "no_such_ghost_file"))); err == nil {
		t.Fatal("read of missing file error = nil, want I/O error")
	}
}

// TestWriteCreatesFile verifies write targets validate and create the file.
func TestWriteCreatesFile(t *testing.T) {
	r, ws := newTestRegistry(t, nil)
	res, err := r.Run(context.Background(), "write", argMap("path", filepath.Join(ws, "new.txt"), "content", "hello"))
	out := res.Text
	if err != nil {
		t.Fatalf("write error = %v, want nil", err)
	}
	if out == "" {
		t.Fatal("write returned no confirmation")
	}
	data, err := os.ReadFile(filepath.Join(ws, "new.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("write content = %q err=%v, want hello", data, err)
	}
}

// TestEditReplacesText verifies edit replaces the first unique occurrence.
func TestEditReplacesText(t *testing.T) {
	r, ws := newTestRegistry(t, nil)
	path := filepath.Join(ws, "f.txt")
	if err := os.WriteFile(path, []byte("alpha beta gamma"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, err := r.Run(context.Background(), "edit", argMap("path", path, "old_string", "beta", "new_string", "BETA"))
	if err != nil {
		t.Fatalf("edit error = %v, want nil", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "alpha BETA gamma" {
		t.Fatalf("after edit = %q, want alpha BETA gamma", data)
	}
}

// TestWriteRejectsOutsideRoots verifies write fails hard on paths outside the
// writable roots.
func TestWriteRejectsOutsideRoots(t *testing.T) {
	r, _ := newTestRegistry(t, nil)
	if _, err := r.Run(context.Background(), "write", argMap("path", "/etc/pwned", "content", "x")); err == nil {
		t.Fatal("write to /etc/pwned error = nil, want hard error")
	}
}

// TestEditRejectsOutsideRoots verifies edit keeps the writable-root guard:
// a host path outside every writable root is a hard error, never a genuine
// read/write attempt.
func TestEditRejectsOutsideRoots(t *testing.T) {
	r, _ := newTestRegistry(t, nil)
	if _, err := r.Run(context.Background(), "edit", argMap("path", "/etc/passwd", "old_string", "root", "new_string", "root")); err == nil {
		t.Fatal("edit to /etc/passwd error = nil, want hard error")
	}
}
