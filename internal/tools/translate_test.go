package tools

import "testing"

func TestPathTranslatorPreservesAbsolutePaths(t *testing.T) {
	t.Parallel()
	tr := NewPathTranslator()

	cases := []string{
		"/tmp/kubeconfig",
		"/home/user/.eitri/sessions/abc/tmp/report.html",
		"/home/user/proj/main.go",
	}
	for _, p := range cases {
		gotHost, rewritten := tr.SandboxToHost(p)
		if gotHost != p || rewritten {
			t.Fatalf("SandboxToHost(%q) = %q, %v; want identity", p, gotHost, rewritten)
		}
		gotSandbox, rev := tr.HostToSandbox(p)
		if gotSandbox != p || rev {
			t.Fatalf("HostToSandbox(%q) = %q, %v; want identity", p, gotSandbox, rev)
		}
	}
}

func TestPathTranslatorResolveUsesWorkspaceForRelativePaths(t *testing.T) {
	t.Parallel()
	tr := NewPathTranslator()
	if got := tr.Resolve("docs/readme.md", "/home/user/ws"); got != "/home/user/ws/docs/readme.md" {
		t.Fatalf("Resolve(relative) = %q", got)
	}
	if got := tr.Resolve("/tmp/kubeconfig", "/home/user/ws"); got != "/tmp/kubeconfig" {
		t.Fatalf("Resolve(absolute) = %q", got)
	}
}
