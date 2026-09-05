package tools

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func newTestRegistry(t *testing.T, rr Runner) (*Registry, string) {
	t.Helper()
	top := filepath.Join(t.TempDir(), ".eitri-test-reg-"+strings.ReplaceAll(t.Name(), "/", "_"))
	ws := filepath.Join(top, "proj")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if rr == nil {
		rr = &recordingRunner{out: &Output{Stdout: "ls-output\n"}}
	}
	r, _ := NewRegistry(Deps{
		Workspace: ws,
		TempHost:  filepath.Join(t.TempDir(), "eitri-g"),
		Runner:    rr,
	})
	return r, ws
}

func argMap(kv ...string) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func TestRegistryExposesTools(t *testing.T) {
	t.Parallel()
	r, _ := newTestRegistry(t, nil)
	got := map[string]bool{}
	for _, n := range r.Names() {
		got[n] = true
	}
	for _, want := range []string{"bash", "open_in_browser"} {
		if !got[want] {
			t.Fatalf("registry missing tool %q; names = %v", want, r.Names())
		}
	}
	for _, banned := range []string{"read", "write", "edit", "skill", "web_fetch"} {
		if got[banned] {
			t.Fatalf("registry still exposes tool %q; names = %v", banned, r.Names())
		}
	}
}

func TestBashRunsInSandbox(t *testing.T) {
	t.Parallel()
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

func TestRegistrySelectsSandboxBackendByDefault(t *testing.T) {
	t.Parallel()
	rr := &recordingRunner{out: &Output{Stdout: "x"}}
	r, _ := newTestRegistry(t, rr)
	if _, err := r.Run(context.Background(), "bash", argMap("command", "echo hi")); err != nil {
		t.Fatalf("bash error = %v, want nil", err)
	}
	if len(rr.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(rr.calls))
	}
	if rr.calls[0].Name != "bwrap" {
		t.Fatalf("default backend exec = %q, want bwrap (sandboxed)", rr.calls[0].Name)
	}
}

func TestRegistrySelectsDirectBackendInYolo(t *testing.T) {
	t.Parallel()
	rr := &recordingRunner{out: &Output{Stdout: "x"}}
	top := filepath.Join(t.TempDir(), ".eitri-test-reg-yolo-"+strings.ReplaceAll(t.Name(), "/", "_"))
	ws := filepath.Join(top, "proj")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	r, err := NewRegistry(Deps{Workspace: ws, TempHost: filepath.Join(t.TempDir(), "eitri-g"), Runner: rr, Yolo: true})
	if err != nil {
		t.Fatalf("NewRegistry(yolo) error = %v, want nil", err)
	}
	if _, err := r.Run(context.Background(), "bash", argMap("command", "echo hi")); err != nil {
		t.Fatalf("bash error = %v, want nil", err)
	}
	if len(rr.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(rr.calls))
	}
	spec := rr.calls[0]
	if spec.Name != "/bin/bash" {
		t.Fatalf("yolo backend exec = %q, want /bin/bash (direct, no bwrap)", spec.Name)
	}
	if spec.Dir != ws {
		t.Fatalf("yolo backend working dir = %q, want workspace %q", spec.Dir, ws)
	}
}

func TestYoloBashCompressesOutput(t *testing.T) {
	t.Parallel()
	var raw strings.Builder
	for i := 0; i < 600; i++ {
		raw.WriteString("src/file_")
		raw.WriteString(strconv.Itoa(i))
		raw.WriteString(".go          1234 bytes\n")
	}
	rr := &recordingRunner{out: &Output{Stdout: raw.String()}}
	r, _ := NewRegistry(Deps{
		Workspace: t.TempDir(),
		TempHost:  t.TempDir(),
		Runner:    rr,
		Yolo:      true,
	})
	res, err := r.Run(context.Background(), "bash", argMap("command", "ls -R ."))
	if err != nil {
		t.Fatalf("bash error = %v, want nil", err)
	}
	if !strings.Contains(res.Text, " more") {
		t.Fatalf("yolo bash output missing explicit tail marker: %q", res.Text)
	}
	if len(res.Text) >= len(raw.String()) {
		t.Fatalf("yolo bash output not compressed: raw=%d bytes, got=%d bytes", len(raw.String()), len(res.Text))
	}
}

func TestSetTempHostRewiresYoloBackend(t *testing.T) {
	t.Parallel()
	rr := &recordingRunner{out: &Output{Stdout: "x"}}
	r, _ := NewRegistry(Deps{
		Workspace: t.TempDir(),
		TempHost:  filepath.Join(t.TempDir(), "old"),
		Runner:    rr,
		Yolo:      true,
	})
	newTemp := filepath.Join(t.TempDir(), "new")
	if err := r.SetTempHost(newTemp); err != nil {
		t.Fatalf("SetTempHost() error = %v, want nil", err)
	}
	if got := r.TempHost(); got != newTemp {
		t.Fatalf("TempHost() = %q, want %q", got, newTemp)
	}
	if _, err := r.Run(context.Background(), "bash", argMap("command", "echo hi")); err != nil {
		t.Fatalf("bash error = %v, want nil", err)
	}
	spec := rr.calls[0]
	env := map[string]string{}
	for _, kv := range spec.Env {
		p := strings.SplitN(kv, "=", 2)
		env[p[0]] = p[1]
	}
	if env["TMPDIR"] != newTemp {
		t.Fatalf("yolo TMPDIR after rewire = %q, want %q", env["TMPDIR"], newTemp)
	}
}

func TestBashCompressesNoisyOutputAtBoundary(t *testing.T) {
	t.Parallel()
	var raw strings.Builder
	for i := 0; i < 600; i++ {
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
	againRes, err := r.Run(context.Background(), "bash", argMap("command", "ls -R ."))
	again := againRes.Text
	if err != nil {
		t.Fatalf("bash second run error = %v, want nil", err)
	}
	if again != got {
		t.Fatalf("compression not deterministic: first=%q second=%q", got, again)
	}
}

func TestNewRegistryRejectsInvalidSandboxDependencies(t *testing.T) {
	t.Parallel()
	_, err := NewRegistry(Deps{Workspace: "", TempHost: "/tmp/session", Runner: &recordingRunner{}})
	if err == nil || !strings.Contains(err.Error(), "workspace path is empty") {
		t.Fatalf("NewRegistry() error = %v, want actionable workspace error", err)
	}
}

func TestRegistryRejectsInvalidSessionTempRewire(t *testing.T) {
	r, _ := newTestRegistry(t, nil)
	for _, path := range []string{"", "relative/session"} {
		if err := r.SetTempHost(path); err == nil || !strings.Contains(err.Error(), "session temp path") {
			t.Fatalf("SetTempHost(%q) error = %v, want actionable error", path, err)
		}
	}
}
