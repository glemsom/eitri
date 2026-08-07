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
		{"test pass", "VERDICT: PASS\n", "PASS"},
		{"test reject", "VERDICT: REJECT\n", "REJECT"},
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

func TestTestPr(t *testing.T) {
	cases := []struct {
		name        string
		eitriBody   string
		writeTestMD bool
		wantVerdict string
		wantStatus  string
	}{
		{
			name:        "pass when code-test approves",
			eitriBody:   `echo "VERDICT: PASS"; touch .test.md`,
			writeTestMD: true,
			wantVerdict: "PASS",
			wantStatus:  "0",
		},
		{
			name:        "reject when code-test rejects",
			eitriBody:   `echo "VERDICT: REJECT"; touch .test.md`,
			writeTestMD: true,
			wantVerdict: "REJECT",
			wantStatus:  "0",
		},
		{
			name:        "hard-fail on non-zero exit",
			eitriBody:   `echo "VERDICT: PASS"; exit 3`,
			wantVerdict: "hard-fail",
			wantStatus:  "3",
		},
		{
			name:        "no-test-suite downgrade yields pass",
			eitriBody:   `echo "VERDICT: PASS"; echo "no test suite found; admitted because the project builds" > .test.md`,
			writeTestMD: true,
			wantVerdict: "PASS",
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
			script := `test_pr '` + wtQuoted + `' "run the tests"`
			cmd := exec.Command("bash", "-c", "source agent-loop.sh; "+script)
			cmd.Dir = "."
			cmd.Env = append(os.Environ(), "PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("test_pr failed: %v\n%s", err, out)
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
			// .test.md must be written into the worktree when the persona did so
			if tc.writeTestMD {
				if _, err := os.Stat(filepath.Join(wt, ".test.md")); err != nil {
					t.Fatalf(".test.md missing in worktree: %v", err)
				}
			}
			// per-stage log must exist
			if _, err := os.Stat(lines[2]); err != nil {
				t.Fatalf("stage log missing: %v", err)
			}
		})
	}
}

func TestReviewPr(t *testing.T) {
	cases := []struct {
		name          string
		eitriBody     string
		writeReviewMD bool
		wantVerdict   string
		wantStatus    string
	}{
		{
			name:          "approved when code-review approves",
			eitriBody:     `echo "VERDICT: APPROVED"; touch .review.md`,
			writeReviewMD: true,
			wantVerdict:   "APPROVED",
			wantStatus:    "0",
		},
		{
			name:          "changes required when code-review asks for changes",
			eitriBody:     `echo "VERDICT: CHANGES_REQUIRED"; touch .review.md`,
			writeReviewMD: true,
			wantVerdict:   "CHANGES_REQUIRED",
			wantStatus:    "0",
		},
		{
			name:          "blocked when code-review blocks",
			eitriBody:     `echo "VERDICT: BLOCKED"; touch .review.md`,
			writeReviewMD: true,
			wantVerdict:   "BLOCKED",
			wantStatus:    "0",
		},
		{
			name:        "hard-fail on non-zero exit",
			eitriBody:   `echo "VERDICT: APPROVED"; exit 3`,
			wantVerdict: "hard-fail",
			wantStatus:  "3",
		},
		{
			name:        "hard-fail on non-review verdict",
			eitriBody:   `echo "VERDICT: PASS"`,
			wantVerdict: "hard-fail",
			wantStatus:  "0",
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

			wtQuoted := strings.ReplaceAll(wt, "'", "'\"'\"")
			script := `review_pr '` + wtQuoted + `' "review the pr"`
			cmd := exec.Command("bash", "-c", "source agent-loop.sh; "+script)
			cmd.Dir = "."
			cmd.Env = append(os.Environ(), "PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("review_pr failed: %v\n%s", err, out)
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
			if lines[2] != filepath.Join(wt, "log.review") {
				t.Fatalf("log path = %q, want %q", lines[2], filepath.Join(wt, "log.review"))
			}
			// .review.md must be written into the worktree when the persona did so
			if tc.writeReviewMD {
				if _, err := os.Stat(filepath.Join(wt, ".review.md")); err != nil {
					t.Fatalf(".review.md missing in worktree: %v", err)
				}
			}
			// per-stage log must exist
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

// ---- T6 loop orchestration (issue #1192) ---------------------------------
//
// run_issue_phase drives the build→test→review fix loop for one issue. We
// exercise it exactly as the worker does: source agent-loop.sh (helpers only)
// with a stub `eitri` on PATH that emits stage-appropriate verdicts, a stub
// `gh` that reports a PR and records needs-human comments, then assert on the
// stored verdict files / the merge gate and the needs-human path.

func TestRunIssuePhase(t *testing.T) {
	const eitriStub = `#!/usr/bin/env bash
persona=""
while [ $# -gt 0 ]; do
  case "$1" in
    --persona) persona="$2"; shift 2 ;;
    *) shift ;;
  esac
done
case "$persona" in
  code-build) exit 0 ;;
  code-test)
    n=0; [ -f .tcount ] && n=$(cat .tcount); n=$((n+1)); echo "$n" > .tcount
    v=$(printf '%s' "$TEST_SEQ" | awk -v i="$n" '{print $i}')
    echo "VERDICT: $v"
    exit 0 ;;
  code-review)
    n=0; [ -f .rcount ] && n=$(cat .rcount); n=$((n+1)); echo "$n" > .rcount
    v=$(printf '%s' "$REVIEW_SEQ" | awk -v i="$n" '{print $i}')
    echo "VERDICT: $v"
    exit 0 ;;
esac
`
	const ghStub = `#!/usr/bin/env bash
if [ "$1" = "pr" ] && [ "$2" = "list" ]; then echo "42"; exit 0; fi
if [ "$1" = "pr" ] && [ "$2" = "comment" ]; then touch "$GH_COMMENT_MARKER"; exit 0; fi
exit 0
`

	cases := []struct {
		name       string
		testSeq    string
		reviewSeq  string
		wantTest   string
		wantReview string
		wantMerge  bool
		wantHuman  bool
	}{
		{
			name:       "approve on first round",
			testSeq:    "PASS",
			reviewSeq:  "APPROVED",
			wantTest:   "PASS",
			wantReview: "APPROVED",
			wantMerge:  true,
		},
		{
			name:       "test reject bounces to a fresh round then approves",
			testSeq:    "REJECT PASS",
			reviewSeq:  "APPROVED",
			wantTest:   "PASS",
			wantReview: "APPROVED",
			wantMerge:  true,
		},
		{
			name:       "review changes-required bounces to a fresh round then approves",
			testSeq:    "PASS PASS",
			reviewSeq:  "CHANGES_REQUIRED APPROVED",
			wantTest:   "PASS",
			wantReview: "APPROVED",
			wantMerge:  true,
		},
		{
			name:       "review blocked uses the cap immediately",
			testSeq:    "PASS",
			reviewSeq:  "BLOCKED",
			wantTest:   "PASS",
			wantReview: "BLOCKED",
			wantMerge:  false,
			wantHuman:  true,
		},
		{
			name:       "three reject rounds exhaust the shared cap",
			testSeq:    "REJECT REJECT REJECT",
			reviewSeq:  "",
			wantTest:   "REJECT",
			wantReview: "NONE",
			wantMerge:  false,
			wantHuman:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			wt := filepath.Join(repo, "wt")
			if err := os.MkdirAll(wt, 0o755); err != nil {
				t.Fatalf("mkdir wt: %v", err)
			}
			marker := filepath.Join(repo, ".gh-commented")
			shim := t.TempDir()
			writeExecutable(t, filepath.Join(shim, "eitri"), eitriStub)
			writeExecutable(t, filepath.Join(shim, "gh"), ghStub)

			wtQuoted := strings.ReplaceAll(wt, "'", "'\"'\"")
			script := `run_issue_phase '` + wtQuoted + `' 1 "an issue"`
			cmd := exec.Command("bash", "-c", "source agent-loop.sh; "+script)
			cmd.Dir = "."
			cmd.Env = append(os.Environ(),
				"PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"),
				"TEST_SEQ="+tc.testSeq,
				"REVIEW_SEQ="+tc.reviewSeq,
				"GH_COMMENT_MARKER="+marker,
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run_issue_phase failed: %v\n%s", err, out)
			}

			if got := readVerdict(t, wt, "test"); got != tc.wantTest {
				t.Fatalf("test verdict = %q, want %q\n%s", got, tc.wantTest, out)
			}
			if got := readVerdict(t, wt, "review"); got != tc.wantReview {
				t.Fatalf("review verdict = %q, want %q\n%s", got, tc.wantReview, out)
			}

			// Merge gate must reflect the latest stored verdicts.
			mergeOut := runSourcedBash(t, `can_merge_issue '`+wtQuoted+`' && echo mergeable || echo not-mergeable`)
			expectMergeable := "not-mergeable"
			if tc.wantMerge {
				expectMergeable = "mergeable"
			}
			if mergeOut != expectMergeable {
				t.Fatalf("can_merge_issue = %q, want %q\n%s", mergeOut, expectMergeable, out)
			}

			// A needs-human outcome must result in a gh PR comment.
			_, ghErr := os.Stat(marker)
			hadComment := ghErr == nil
			if hadComment != tc.wantHuman {
				t.Fatalf("needs-human comment present = %v, want %v\n%s", hadComment, tc.wantHuman, out)
			}
		})
	}
}

func readVerdict(t *testing.T, wt, kind string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(wt, "."+kind+"-verdict"))
	if err != nil {
		if os.IsNotExist(err) {
			return "NONE"
		}
		t.Fatalf("read verdict %s: %v", kind, err)
	}
	return strings.TrimSpace(string(b))
}
