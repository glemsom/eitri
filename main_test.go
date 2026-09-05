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

func TestYoloUnsafeDocumentationHonest(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	doc := string(readme)

	// The README describes --yolo-unsafe and no longer claims Eitri never runs
	// unsandboxed, so no stale unqualified claim survives anywhere human-facing.
	if strings.Contains(doc, "never runs unsandboxed") {
		t.Fatalf("README still carries an unqualified %q claim", "never runs unsandboxed")
	}
	// It names the flag and its meaning (direct host execution, no cage).
	for _, want := range []string{"--yolo-unsafe", "bubblewrap cage", "directly as your user"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("README does not describe --yolo-unsafe meaning %q", want)
		}
	}
	// It states the risk: full host permissions, and that Eitri does not
	// represent itself as contained in this mode.
	for _, want := range []string{"full host permissions", "does not represent itself as contained"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("README does not describe the --yolo-unsafe risk %q", want)
		}
	}

	ctx, err := os.ReadFile("CONTEXT.md")
	if err != nil {
		t.Fatalf("read CONTEXT.md: %v", err)
	}
	glossary := string(ctx)
	// The domain glossary captures the yolo-mode vocabulary and the
	// sandbox/yolo distinction.
	for _, want := range []string{"Yolo mode", "unsandboxed", "sandbox"} {
		if !strings.Contains(glossary, want) {
			t.Fatalf("CONTEXT glossary does not capture yolo vocabulary %q", want)
		}
	}
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
		"EITRI_DIR="+dataDir, "EITRI_PROVIDER_URL="+srv.URL, "OPENCODE_API_KEY=test-key",
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
