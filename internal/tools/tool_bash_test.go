package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/compress"
)

func TestBashDescriptionGuidance(t *testing.T) {
	t.Parallel()
	desc := (&bashTool{}).Description()
	folded := strings.ToLower(desc)
	for _, want := range []string{
		"stdout",        // combined stream keeps stdout
		"stderr",        // ... and stderr
		"compress",      // output passes through a compressor
		"ansi",          // ANSI escape sequences stripped
		"collapsed",     // repeated consecutive lines / redraw frames collapse
		"re-run",        // re-running the command is the recovery path
		"recovery",      // ... recovery path for truncated listings
		"truncated",     // heavy listings are truncated
		"deterministic", // same command yields the same compressed form
	} {
		if !strings.Contains(folded, want) {
			t.Fatalf("bash description missing %q: %s", want, desc)
		}
	}
	if !strings.Contains(desc, "+N more") { // marker is case-sensitive in the spec
		t.Fatalf("bash description missing %q: %s", "+N more", desc)
	}
	if !strings.Contains(folded, "never silent") && !strings.Contains(folded, "not silent") {
		t.Fatalf("bash description must state truncation is never silent: %s", desc)
	}
}

func TestDefaultBashDescriptionClaimsSandbox(t *testing.T) {
	t.Parallel()
	desc := (&bashTool{}).Description()
	folded := strings.ToLower(desc)
	if !strings.Contains(folded, "sandbox") {
		t.Fatalf("default bash description must claim a sandbox: %s", desc)
	}
}

func TestYoloBashDescriptionOmitsSandboxClaim(t *testing.T) {
	t.Parallel()
	desc := (&bashTool{unsandboxed: true}).Description()
	folded := strings.ToLower(desc)
	// The yolo description must not claim execution inside a sandbox/cage, and
	// must be honest that the command runs directly with the user's host
	// permissions.
	if strings.Contains(folded, "in a sandbox") || strings.Contains(folded, "inside a sandbox") {
		t.Fatalf("yolo bash description must not claim execution in a sandbox: %s", desc)
	}
	if !strings.Contains(folded, "host") && !strings.Contains(folded, "direct") {
		t.Fatalf("yolo bash description must state it runs directly on the host: %s", desc)
	}
}

// fakeBackend stubs the sandbox boundary so a bash tool run is testable without bwrap.
type fakeBackend struct {
	out *Output
	err error
}

func (f fakeBackend) Run(_ context.Context, _ string) (*Output, error) { return f.out, f.err }
func (fakeBackend) setTempHost(string)                                 {}

func TestBashToolReportsSandboxDroppedBytes(t *testing.T) {
	t.Parallel()
	const upstream = 8<<20 - compress.DefaultByteCap
	b := &bashTool{backend: fakeBackend{out: &Output{
		Stdout:  strings.Repeat("payload line\n", 200),
		Dropped: upstream,
	}}}
	res, err := b.Run(context.Background(), map[string]any{"command": "true"})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if res.BytesDropped != upstream {
		t.Fatalf("ToolResult.BytesDropped = %d, want %d (the sandbox's rejected bytes must ride with the result so the byte cap can fold them into one count)", res.BytesDropped, upstream)
	}
}

func TestYoloBashDescriptionKeepsOutputContract(t *testing.T) {
	t.Parallel()
	desc := (&bashTool{unsandboxed: true}).Description()
	folded := strings.ToLower(desc)
	for _, want := range []string{"stdout", "stderr", "compress", "ansi", "collapsed", "truncated", "deterministic"} {
		if !strings.Contains(folded, want) {
			t.Fatalf("yolo bash description lost output-contract guidance %q: %s", want, desc)
		}
	}
	if !strings.Contains(desc, "+N more") {
		t.Fatalf("yolo bash description missing %q: %s", "+N more", desc)
	}
}
