package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// agent-loop.sh's reusable verdict plumbing (T3, #1188): run_persona_batch runs
// a persona batch in a worktree and surfaces its verdict, and extract_verdict
// parses the latest VERDICT: line out of a batch log. These are pure-shell
// helpers inside the dispatcher script, so we exercise them exactly as T4/T5
// will: source agent-loop.sh (the script is guarded so sourcing only defines
// helpers, it never starts the dispatcher) with a stub `eitri` on PATH that
// emits a configurable verdict, then assert on the three result lines the
// helper prints.

func TestExtractVerdict(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "sample.log")
	writeExecutable(t, filepath.Join(dir, "eitri"), "#!/usr/bin/env bash\n")
	_ = os.WriteFile(log, []byte("VERDICT: APPROVED\n"), 0o644)

	cases := []struct {
		name string
		log  string
		want string
	}{
		{"blank line prefix accepted", "VERDICT: APPROVED\n", "APPROVED"},
		{"changes required", "VERDICT: CHANGES_REQUIRED\n", "CHANGES_REQUIRED"},
		{"blocked", "VERDICT: BLOCKED\n", "BLOCKED"},
		{"latest wins", "VERDICT: APPROVED\nVERDICT: BLOCKED\n", "BLOCKED"},
		{"missing verdict file", "", ""},
		{"no verdict line", "build succeeded\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := log
			if tc.log != "" {
				if err := os.WriteFile(log, []byte(tc.log), 0o644); err != nil {
					t.Fatalf("write log: %v", err)
				}
			} else {
				path = filepath.Join(dir, "does-not-exist.log")
			}
			out := runSourcedBash(t, `printf '%s' "$(extract_verdict "`+path+`")"`)
			if out != tc.want {
				t.Fatalf("extract_verdict(%q) = %q, want %q", path, out, tc.want)
			}
		})
	}
}

func TestRunPersonaBatch_Verdicts(t *testing.T) {
	cases := []struct {
		name        string
		eitriBody   string
		wantVerdict string
		wantStatus  string
	}{
		{
			name:        "approved",
			eitriBody:   `echo "VERDICT: APPROVED"`,
			wantVerdict: "APPROVED",
			wantStatus:  "0",
		},
		{
			name:        "changes required",
			eitriBody:   `echo "VERDICT: CHANGES_REQUIRED"`,
			wantVerdict: "CHANGES_REQUIRED",
			wantStatus:  "0",
		},
		{
			name:        "blocked",
			eitriBody:   `echo "VERDICT: BLOCKED"`,
			wantVerdict: "BLOCKED",
			wantStatus:  "0",
		},
		{
			name:        "hard-fail on non-zero exit",
			eitriBody:   `echo "VERDICT: APPROVED"; exit 3`,
			wantVerdict: "hard-fail",
			wantStatus:  "3",
		},
		{
			name:        "hard-fail on missing verdict",
			eitriBody:   `echo "no verdict"`,
			wantVerdict: "hard-fail",
			wantStatus:  "0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wt := t.TempDir()
			shim := t.TempDir()
			writeExecutable(t, filepath.Join(shim, "eitri"),
				"#!/usr/bin/env bash\n"+tc.eitriBody+"\n")

			wtQuoted := strings.ReplaceAll(wt, "'", "'\"'\"'")
			script := `run_persona_batch '` + wtQuoted + `' test code-test "run the tests"`
			cmd := exec.Command("bash", "-c", "source agent-loop.sh; "+script)
			cmd.Dir = "."
			cmd.Env = append(os.Environ(), "PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run_persona_batch failed: %v\n%s", err, out)
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) != 3 {
				t.Fatalf("want 3 result lines, got %d:\n%s", len(lines), out)
			}
			if lines[0] != tc.wantVerdict {
				t.Fatalf("verdict = %q, want %q (full:\n%s)", lines[0], tc.wantVerdict, out)
			}
			if lines[1] != tc.wantStatus {
				t.Fatalf("status = %q, want %q", lines[1], tc.wantStatus)
			}
			if lines[2] != filepath.Join(wt, "log.test") {
				t.Fatalf("log path = %q, want %q", lines[2], filepath.Join(wt, "log.test"))
			}
			// per-stage log must exist and contain the stub's output
			if _, err := os.Stat(lines[2]); err != nil {
				t.Fatalf("stage log missing: %v", err)
			}
		})
	}
}

// runSourcedBash sources scripts/agent-loop.sh (helper definitions only) then
// runs the given snippet, returning its stdout.
func runSourcedBash(t *testing.T, snippet string) string {
	t.Helper()
	cmd := exec.Command("bash", "-c", `source agent-loop.sh; `+snippet)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sourced script snippet failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}
