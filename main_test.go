package main

import (
	"bytes"
	"encoding/json"
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

	t.Run("usage states the full dependency contract", func(t *testing.T) {
		cmd := exec.Command(bin, "--help")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("eitri --help exit error = %v, output:\n%s", err, out)
		}
		// The usage text reflects the full dependency contract, not just bubblewrap.
		for _, name := range []string{"bwrap", "bash", "rg", "curl", "lynx", "patch", "python3", "git", "jq", "xdg-open", "xdg-utils"} {
			if !strings.Contains(string(out), name) {
				t.Fatalf("usage output %q does not name declared tool %q", out, name)
			}
		}
		for _, hint := range []string{"sudo apt install", "sudo dnf install", "sudo pacman -S"} {
			if !strings.Contains(string(out), hint) {
				t.Fatalf("usage output %q lacks per-distro install hint %q", out, hint)
			}
		}
		if strings.Contains(string(out), "soft dependency") {
			t.Fatalf("usage output %q still classifies xdg-open as a soft dependency", out)
		}
		if !strings.Contains(string(out), "coreutils") {
			t.Fatalf("usage output %q lacks the base-toolset marker", out)
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
		for _, name := range []string{"bwrap", "bash", "rg", "curl", "lynx", "patch", "python3", "git", "jq", "xdg-open", "xdg-utils"} {
			if !strings.Contains(string(out), name) {
				t.Fatalf("eitri without declared deps output %q does not name missing tool %q", out, name)
			}
		}
		if !strings.Contains(string(out), "sudo apt install") {
			t.Fatalf("eitri without declared deps output %q lacks an install hint", out)
		}
	})
}

func TestRenderDiagnosticsDocsGiveBenchmarkComparisonWorkflow(t *testing.T) {
	b, err := os.ReadFile("docs/render-diagnostics.md")
	if err != nil {
		t.Fatalf("read render diagnostics docs: %v", err)
	}
	doc := string(b)

	for _, seam := range []string{"Model view", "Transcript render", "live turn rendering", "markdown rendering", "viewport rendering"} {
		if !strings.Contains(doc, seam) {
			t.Fatalf("render diagnostics docs do not identify benchmark seam %q", seam)
		}
	}
	for _, guidance := range []string{"go test -run '^$' -bench", "-count=10", "benchstat", "pprof alone", "measure, change one thing, and re-measure"} {
		if !strings.Contains(doc, guidance) {
			t.Fatalf("render diagnostics docs lack benchmark comparison guidance %q", guidance)
		}
	}
	if !strings.Contains(doc, "Existing render benchmarks remain the starting point") {
		t.Fatalf("render diagnostics docs must keep existing render benchmarks as the starting point")
	}
}

func TestRenderDiagnosticsDocsDescribeSupportedWorkflows(t *testing.T) {
	b, err := os.ReadFile("docs/render-diagnostics.md")
	if err != nil {
		t.Fatalf("read render diagnostics docs: %v", err)
	}
	doc := string(b)

	for _, want := range []string{"Performance symptoms", "go tool pprof -seconds 30"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("render diagnostics docs lack supported workflow detail %q", want)
		}
	}
	for _, removed := range []string{"DiagnosticsConfig", "FrameSnapshotDir", "RawFrameCaptureDir", "RenderDiagnosticFrames"} {
		if strings.Contains(doc, removed) {
			t.Fatalf("render diagnostics docs still describe removed TUI diagnostic %q", removed)
		}
	}
}

func TestCLIUnknownFormatDiesBeforeBoot(t *testing.T) {
	// `eitri -b "x" --format bogus` must die at flag parse with the unknown-format
	// usage message, exit 1, before any provider boot — no session directory
	// (indeed no data directory) is created.
	bin := buildBinary(t)
	dataDir := filepath.Join(t.TempDir(), ".eitri")
	cmd := exec.Command(bin, "-b", "hello", "--format", "bogus")
	cmd.Env = append(cleanEnvs(t, "EITRI_DIR", "OPENCODE_API_KEY", "EITRI_PROVIDER_URL"), "EITRI_DIR="+dataDir)

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("eitri --format bogus exited zero, output:\n%s", out)
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("unknown --format exit = %v, want exit code 1", err)
	}
	if !strings.Contains(string(out), `unknown --format "bogus" (want: text, json)`) {
		t.Fatalf("unknown-format output %q lacks the want: text, json usage message", out)
	}
	if _, statErr := os.Stat(dataDir); statErr == nil {
		t.Fatalf("data dir %s was created before format validation refused boot", dataDir)
	}
}

func TestCLIJSONFormatPrintsEnvelope(t *testing.T) {
	srv := stubProviderServer(t)
	bin := buildBinary(t)
	cmd := exec.Command(bin, "-b", "hello", "--format", "json")
	cmd.Env, _ = batchRunEnv(t, srv.URL)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("eitri -b --format json exit error = %v, stderr:\n%s", err, stderr.String())
	}
	var env map[string]any
	if jerr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &env); jerr != nil {
		t.Fatalf("--format json stdout %q is not one JSON object: %v", stdout.String(), jerr)
	}
	for _, key := range []string{"answer", "session", "turns", "stopped"} {
		if _, ok := env[key]; !ok {
			t.Fatalf("--format json envelope lacks key %q: %v", key, env)
		}
	}
}

func TestCLIBatchWithStubProvider(t *testing.T) {
	srv := stubProviderServer(t)
	bin := buildBinary(t)
	cmd := exec.Command(bin, "-b", "hello")
	cmd.Env, _ = batchRunEnv(t, srv.URL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("eitri -b exit error = %v, output:\n%s", err, out)
	}
	if !strings.Contains(string(out), "Hello world") {
		t.Fatalf("batch output %q missing the final answer", out)
	}
}

// stubProviderServer serves the hello.sse fixture over SSE, standing in for a
// model endpoint so CLI-level batch tests can boot without a network.
func stubProviderServer(t *testing.T) *httptest.Server {
	t.Helper()
	fixture, err := os.ReadFile("internal/provider/testdata/hello.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(fixture)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// batchRunEnv builds the environment a booted batch CLI run needs: a fresh
// data dir and a stubbed provider endpoint.
func batchRunEnv(t *testing.T, providerURL string) ([]string, string) {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), ".eitri")
	// Point HOME at a virgin temp dir so the booted binary's skill discovery
	// never reads the developer's ~/.agents/skills.
	home := filepath.Join(t.TempDir(), "home")
	env := append(
		cleanEnvs(t, "EITRI_DIR", "OPENCODE_API_KEY", "EITRI_PROVIDER_URL"),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"EITRI_DIR="+dataDir, "EITRI_PROVIDER_URL="+providerURL, "OPENCODE_API_KEY=test-key",
	)
	return env, dataDir
}

func TestCLIBatchConsumesPipedStdin(t *testing.T) {
	// `git diff | eitri -b "review this diff"` must work end to end: piped stdin
	// is consumed as context and the run completes cleanly.
	srv := stubProviderServer(t)
	bin := buildBinary(t)
	cmd := exec.Command(bin, "-b", "review this diff")
	cmd.Env, _ = batchRunEnv(t, srv.URL)
	cmd.Stdin = strings.NewReader("diff --git a/x b/x\n--- a/x\n+++ b/x\n")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("eitri -b with piped stdin exit error = %v, output:\n%s", err, out)
	}
	if !strings.Contains(string(out), "Hello world") {
		t.Fatalf("batch output %q missing the final answer", out)
	}
}

func TestCLIBatchRefusesOversizedStdinWithExitOne(t *testing.T) {
	srv := stubProviderServer(t)
	bin := buildBinary(t)
	cmd := exec.Command(bin, "-b", "summarize")
	cmd.Env, _ = batchRunEnv(t, srv.URL)
	// One byte over the 1 MiB cap: refusal beats truncation, exit code 1.
	cmd.Stdin = strings.NewReader(strings.Repeat("x", 1<<20+1))

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("eitri -b with >1 MiB stdin exited zero, output:\n%s", out)
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("oversized-stdin refusal exit = %v, want exit code 1", err)
	}
	if !strings.Contains(string(out), "1 MiB") {
		t.Fatalf("oversized-stdin refusal stderr %q does not name the 1 MiB cap", out)
	}
}

func TestCLIRefusesPipedStdinWithoutBatch(t *testing.T) {
	// `git diff | eitri` (no -b) must refuse rather than silently drain the pipe,
	// pointing the user at `eitri -b "<prompt>"`, exit code 1.
	srv := stubProviderServer(t)
	bin := buildBinary(t)
	cmd := exec.Command(bin)
	cmd.Env, _ = batchRunEnv(t, srv.URL)
	cmd.Stdin = strings.NewReader("git diff output")

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("eitri with piped stdin and no -b exited zero, output:\n%s", out)
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("no--b piped-stdin refusal exit = %v, want exit code 1", err)
	}
	if !strings.Contains(string(out), `eitri -b "<prompt>"`) {
		t.Fatalf("no--b refusal stderr %q does not point at `eitri -b \"<prompt>\"`", out)
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
	// Drop HOME and the XDG homes as well: a spawned eitri must not see the
	// developer's real ~/.agents/skills, whose user-skill skip warnings would
	// pollute a pure --format json stdout.
	names = append(names, "HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME")
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
