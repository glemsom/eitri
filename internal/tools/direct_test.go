package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectRunnerExecutesBashDirectly(t *testing.T) {
	t.Parallel()
	rr := &recordingRunner{out: &Output{Stdout: "ok"}}
	ws := "/home/u/proj"
	temp := filepath.Join("/tmp", "session")
	dr := &directRunner{workspace: ws, tempHost: temp, run: rr}
	if _, err := dr.Run(context.Background(), "echo hi"); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(rr.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(rr.calls))
	}
	spec := rr.calls[0]
	if spec.Name != "/bin/bash" {
		t.Fatalf("exec name = %q, want /bin/bash (no bwrap)", spec.Name)
	}
	if strings.Contains(spec.Name, "bwrap") {
		t.Fatalf("direct backend still names bwrap: %q", spec.Name)
	}
	if len(spec.Args) != 2 || spec.Args[0] != "-c" || spec.Args[1] != "echo hi" {
		t.Fatalf("argv = %v, want [-c echo hi]", spec.Args)
	}
	if spec.Dir != ws {
		t.Fatalf("working directory = %q, want workspace %q", spec.Dir, ws)
	}
	env := map[string]string{}
	for _, kv := range spec.Env {
		parts := strings.SplitN(kv, "=", 2)
		env[parts[0]] = parts[1]
	}
	for _, k := range []string{"TMPDIR", "TEMP", "TMP"} {
		if env[k] != temp {
			t.Fatalf("env[%s] = %q, want session temp %q (env=%v)", k, env[k], temp, spec.Env)
		}
	}
}

func TestDirectRunnerCreatesSessionTemp(t *testing.T) {
	t.Parallel()
	rr := &recordingRunner{out: &Output{Stdout: "ok"}}
	temp := filepath.Join(t.TempDir(), "nested", "session")
	dr := &directRunner{workspace: t.TempDir(), tempHost: temp, run: rr}
	if _, err := dr.Run(context.Background(), "true"); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	fi, err := os.Stat(temp)
	if err != nil {
		t.Fatalf("session temp %s not created: %v", temp, err)
	}
	if !fi.IsDir() {
		t.Fatalf("session temp %s is not a directory", temp)
	}
}

func TestDirectRunnerRealIntegration(t *testing.T) {
	t.Parallel()
	ws := newNonRemappedWorkspace(t)
	temp := filepath.Join(t.TempDir(), "session")
	dr := &directRunner{workspace: ws, tempHost: temp, run: defaultRunner{}}
	o, err := dr.Run(context.Background(), "echo cwd=$PWD; echo tmp=$TMPDIR; echo t=$TEMP; echo u=$TMP")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if !strings.Contains(o.Stdout, "cwd="+ws) {
		t.Fatalf("stdout %q does not run with workspace as cwd %q", o.Stdout, ws)
	}
	if !strings.Contains(o.Stdout, "tmp="+temp) || !strings.Contains(o.Stdout, "t="+temp) || !strings.Contains(o.Stdout, "u="+temp) {
		t.Fatalf("stdout %q does not set session temp env to %q", o.Stdout, temp)
	}
	// The workspace must be writable directly (no cage): a write should land host-side.
	if _, err := dr.Run(context.Background(), "echo probe > probe.txt"); err != nil {
		t.Fatalf("workspace write error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "probe.txt")); err != nil {
		t.Fatalf("workspace write did not land host-side: %v", err)
	}
}
