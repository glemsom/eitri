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
	r := NewRegistry(Deps{
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
