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

func TestCLISmoke(t *testing.T) {
	bin := buildBinary(t)

	t.Run("boot creates data dir and exits cleanly", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), ".eitri")
		cmd := exec.Command(bin)
		cmd.Env = append(os.Environ(), "EITRI_DIR="+dataDir)
		out, err := cmd.CombinedOutput()
		if err != nil && !strings.Contains(string(out), "-b") {
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

	t.Run("hard-fails when declared dependencies are missing", func(t *testing.T) {
		empty := t.TempDir()
		dataDir := filepath.Join(t.TempDir(), ".eitri")
		cmd := exec.Command(bin)
		cmd.Env = append(
			cleanEnvs(t, "PATH", "EITRI_DIR"),
			"PATH="+empty, "EITRI_DIR="+dataDir,
		)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("eitri without declared deps exited zero, output:\n%s", out)
		}
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 0 {
			t.Fatalf("eitri without declared deps returned exit code 0, output:\n%s", out)
		}
		// The refusal names every missing declared tool (bwrap..python3) with
		// an install hint, not just the first miss.
		for _, name := range []string{"bwrap", "bash", "rg", "curl", "lynx", "patch", "python3"} {
			if !strings.Contains(string(out), name) {
				t.Fatalf("eitri without declared deps output %q does not name missing tool %q", out, name)
			}
		}
		if !strings.Contains(string(out), "sudo apt install") {
			t.Fatalf("eitri without declared deps output %q lacks an install hint", out)
		}
	})
}

func TestCLIBatchWithStubProvider(t *testing.T) {
	fixture, err := os.ReadFile("internal/provider/testdata/hello.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(fixture)
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
