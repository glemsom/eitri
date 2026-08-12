package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLISmoke exercises the compiled eitri binary end-to-end: boot success
// under a stubbed data dir, --version, and the hard bwrap-missing failure path.
func TestCLISmoke(t *testing.T) {
	bin := buildBinary(t)

	t.Run("boot creates data dir and exits cleanly", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), ".eitri")
		cmd := exec.Command(bin)
		cmd.Env = append(os.Environ(), "EITRI_DIR="+dataDir)
		out, err := cmd.CombinedOutput()
		// With no args, eitri boots then launches the interactive TUI. A
		// headless run has no TTY, so we tolerate the clean TTY error while
		// still asserting boot completed (the data dir was created).
		if err != nil && !strings.Contains(string(out), "/dev/tty") {
			t.Fatalf("eitri exit error = %v, output:\n%s", err, out)
		}
		fi, err := os.Stat(dataDir)
		if err != nil {
			t.Fatalf("data dir %s not created: %v", dataDir, err)
		}
		if !fi.IsDir() {
			t.Fatalf("data dir %s is not a directory", dataDir)
		}
	})

	t.Run("version prints and exits zero", func(t *testing.T) {
		cmd := exec.Command(bin, "--version")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("eitri --version exit error = %v, output:\n%s", err, out)
		}
		if len(strings.TrimSpace(string(out))) == 0 {
			t.Fatalf("eitri --version printed no output")
		}
	})

	t.Run("hard-fails without bwrap", func(t *testing.T) {
		// A PATH that contains no bwrap forces the missing-prerequisite path.
		empty := t.TempDir()
		dataDir := filepath.Join(t.TempDir(), ".eitri")
		cmd := exec.Command(bin)
		cmd.Env = append(
			cleanEnvs(t, "PATH", "EITRI_DIR"),
			"PATH="+empty, "EITRI_DIR="+dataDir,
		)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("eitri without bwrap exited zero, output:\n%s", out)
		}
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 0 {
			t.Fatalf("eitri without bwrap returned exit code 0, output:\n%s", out)
		}
		if !strings.Contains(string(out), "bwrap") {
			t.Fatalf("eitri without bwrap output %q lacks an install-bubblewrap hint", out)
		}
	})
}

// TestCLIBatchWithStubProvider runs the compiled binary in batch mode against a
// local stub Chat-Completions server, exercising the full CLI → app → engine →
// provider path end-to-end without a network. It asserts the final answer lands
// on stdout.
func TestCLIBatchWithStubProvider(t *testing.T) {
	fixture, err := os.ReadFile("internal/provider/testdata/hello.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write(fixture)
	}))
	defer srv.Close()

	bin := buildBinary(t)
	dataDir := filepath.Join(t.TempDir(), ".eitri")
	cmd := exec.Command(bin, "-b", "hello")
	cmd.Env = append(
		cleanEnvs(t, "EITRI_DIR", "OPENCODE_API_KEY", "EITRI_PROVIDER_URL"),
		"EITRI_DIR="+dataDir, "EITRI_PROVIDER_URL="+srv.URL,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("eitri -b exit error = %v, output:\n%s", err, out)
	}
	if !strings.Contains(string(out), "Hello world") {
		t.Fatalf("batch output %q missing the final answer", out)
	}
}

// buildBinary compiles cmd/eitri into a temp path and returns it.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "eitri")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return bin
}

// cleanEnvs returns the current environment with the named variables unset.
func cleanEnvs(t *testing.T, names ...string) []string {
	t.Helper()
	drop := make(map[string]bool, len(names))
	for _, n := range names {
		drop[n] = true
	}
	var out []string
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 && drop[kv[:i]] {
			continue
		}
		out = append(out, kv)
	}
	return out
}
