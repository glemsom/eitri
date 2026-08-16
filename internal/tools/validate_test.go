package tools

import "testing"

// TestValidatorAllowsWorkspace enumerates in-workspace targets (the primary
// writable root), resolving them to their unchanged host form.
func TestValidatorAllowsWorkspace(t *testing.T) {
	v := NewValidator("/home/u/proj", nil, NewPathTranslator(GUID("g1")))
	for _, p := range []string{
		"/home/u/proj",
		"/home/u/proj/a.txt",
		"/home/u/proj/src/main.go",
	} {
		host, err := v.Resolve(p)
		if err != nil {
			t.Fatalf("Resolve(%q) error = %v, want nil", p, err)
		}
		if host != p {
			t.Fatalf("Resolve(%q) host = %q, want unchanged %q", p, host, p)
		}
	}
}

// TestValidatorAllowsExtraWritablePaths verifies targets under configured
// extra_writable_paths are accepted.
func TestValidatorAllowsExtraWritablePaths(t *testing.T) {
	v := NewValidator("/home/u/proj", []string{"/srv/data", "/home/u/scratch"}, NewPathTranslator(GUID("g2")))
	for _, p := range []string{
		"/srv/data",
		"/srv/data/file.txt",
		"/home/u/scratch/tmp.log",
	} {
		if _, err := v.Resolve(p); err != nil {
			t.Fatalf("Resolve(%q) error = %v, want nil", p, err)
		}
	}
}

// TestValidatorAllowsSessionTemp verifies a sandbox /tmp target translates to
// the session temp host root and is accepted as a writable root.
func TestValidatorAllowsSessionTemp(t *testing.T) {
	v := NewValidator("/home/u/proj", nil, NewPathTranslator(GUID("abc")))
	host, err := v.Resolve("/tmp/build.sh")
	if err != nil {
		t.Fatalf("Resolve(/tmp/build.sh) error = %v, want nil", err)
	}
	if host != "/tmp/eitri-abc/build.sh" {
		t.Fatalf("host = %q, want %q", host, "/tmp/eitri-abc/build.sh")
	}
}

// TestValidatorRejectsOutsideRoots verifies targets outside every writable
// root are hard errors.
func TestValidatorRejectsOutsideRoots(t *testing.T) {
	v := NewValidator("/home/u/proj", []string{"/srv/data"}, NewPathTranslator(GUID("g3")))
	for _, p := range []string{
		"/etc/passwd",
		"/home/other/secret.txt",
		"/home/u/project/x", // sibling of workspace root, not inside it
	} {
		if _, err := v.Resolve(p); err == nil {
			t.Fatalf("Resolve(%q) error = nil, want hard error", p)
		}
	}
}

// TestValidatorIsFilePathRootWise confirms a prefix like /home/user/proj is
// an exact element-boundary root: the workspace root and its children are
// writable, but a sibling with a shared prefix is not.
func TestValidatorRejectsSiblingPrefixAbuse(t *testing.T) {
	v := NewValidator("/home/u/proj", nil, NewPathTranslator(GUID("g4")))
	if _, err := v.Resolve("/home/u/projectly"); err == nil {
		t.Fatal("Resolve(/home/u/projectly) error = nil, want hard error")
	}
	if host, err := v.Resolve("/home/u/proj/deeply/nested/x"); err != nil || host == "" {
		t.Fatalf("Resolve(nested) error = %v, host = %q, want nil+resolved", err, host)
	}
}
