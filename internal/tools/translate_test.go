package tools

import (
	"testing"
)

// TestPathTranslatorIsBidirectional verifies the prefix-map translates both
// directions: sandbox /tmp -> host /tmp/eitri-<GUID> and the reverse, while
// leaving workspace host paths untouched.
func TestPathTranslatorIsBidirectional(t *testing.T) {
	g := GUID("abc123")
	tr := NewPathTranslator(g)

	cases := []struct {
		name      string
		sandbox   string
		host      string
		rewritten bool
	}{
		{"session temp file", "/tmp/foo.txt", "/tmp/eitri-abc123/foo.txt", true},
		{"session temp nested", "/tmp/a/b/c", "/tmp/eitri-abc123/a/b/c", true},
		{"session temp root", "/tmp", "/tmp/eitri-abc123", true},
		{"workspace path untouched", "/home/user/proj/main.go", "/home/user/proj/main.go", false},
		{"temp prefix only", "/tmpetc/x", "/tmpetc/x", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotHost, rewritten := tr.SandboxToHost(tc.sandbox)
			if gotHost != tc.host {
				t.Fatalf("SandboxToHost(%q) host = %q, want %q", tc.sandbox, gotHost, tc.host)
			}
			if rewritten != tc.rewritten {
				t.Fatalf("SandboxToHost(%q) rewritten = %v, want %v", tc.sandbox, rewritten, tc.rewritten)
			}
			gotSandbox, rev := tr.HostToSandbox(tc.host)
			if gotSandbox != tc.sandbox {
				t.Fatalf("HostToSandbox(%q) sandbox = %q, want %q", tc.host, gotSandbox, tc.sandbox)
			}
			if rev != tc.rewritten {
				t.Fatalf("HostToSandbox(%q) rewritten = %v, want %v", tc.host, rev, tc.rewritten)
			}
		})
	}
}

// TestPathTranslatorIsIdempotent verifies repeated
// translation never compounds or double-applies the GUID segment.
func TestPathTranslatorIsIdempotent(t *testing.T) {
	g := GUID("xyz99")
	tr := NewPathTranslator(g)

	if host, _ := tr.SandboxToHost("/tmp/a"); host != "/tmp/eitri-xyz99/a" {
		t.Fatalf("first host = %q", host)
	}
	// Re-applying the host path in the sandbox direction must return it as-is
	// (it is already host form), and re-applying must not double the GUID.
	if host, _ := tr.SandboxToHost("/tmp/eitri-xyz99/a"); host != "/tmp/eitri-xyz99/a" {
		t.Fatalf("idempotent host = %q, want unchanged", host)
	}
	// A host path fed through the sandbox direction must not grow a second GUID.
	if h, _ := tr.SandboxToHost("/tmp"); h != "/tmp/eitri-xyz99" {
		t.Fatalf("sandbox->host /tmp = %q, want %q", h, "/tmp/eitri-xyz99")
	}
}

// TestPathTranslatorTempIdentityDefinesGuestRoot verifies the model-facing
// temp identity is always sandbox /tmp.
func TestPathTranslatorTempIdentityDefinesGuestRoot(t *testing.T) {
	tr := NewPathTranslator(GUID("aaa"))
	host, rewritten := tr.SandboxToHost("/tmp")
	if rewritten != true {
		t.Fatalf("/tmp should rewrite, got rewritten=%v", rewritten)
	}
	if host != "/tmp/eitri-aaa" {
		t.Fatalf("host temp root = %q, want %q", host, "/tmp/eitri-aaa")
	}
}
