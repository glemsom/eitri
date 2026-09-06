package skillspack_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/glemsom/eitri/internal/engine/skillspack"
)

// collectWarnings drains a Materialize run's non-fatal warnings into *out so
// tests can assert both the absence and the content of warnings.
func collectWarnings(out *[]string) skillspack.Warnf {
	return func(format string, args ...any) {
		*out = append(*out, fmt.Sprintf(format, args...))
	}
}

func TestMaterializeWritesPacksOnFirstRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var warns []string
	skillspack.Materialize(dir, skillspack.FS, collectWarnings(&warns))

	embedded, err := skillspack.FS.ReadFile(filepath.Join("subagents", "SKILL.md"))
	if err != nil {
		t.Fatalf("read embedded subagents/SKILL.md: %v", err)
	}
	target := filepath.Join(dir, "skills-builtin", "subagents", "SKILL.md")
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("materialized SKILL.md not present: %v", err)
	}
	if !bytes.Equal(got, embedded) {
		t.Fatal("materialized content differs from the embedded bytes")
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings = %v, want none", warns)
	}
}

func TestMaterializeLeavesIdenticalFilesUntouched(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var warns []string
	skillspack.Materialize(dir, skillspack.FS, collectWarnings(&warns))
	target := filepath.Join(dir, "skills-builtin", "subagents", "SKILL.md")

	// Age the file on disk so an accidental rewrite would be observable in mtime.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(target, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	skillspack.Materialize(dir, skillspack.FS, collectWarnings(&warns))
	after, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat after second materialize: %v", err)
	}
	if !after.ModTime().Equal(old) {
		t.Fatalf("second materialize rewrote an identical file: mtime = %v, want stale %v", after.ModTime(), old)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings = %v, want none", warns)
	}
}

func TestMaterializeRewritesTamperedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var warns []string
	skillspack.Materialize(dir, skillspack.FS, collectWarnings(&warns))
	target := filepath.Join(dir, "skills-builtin", "subagents", "SKILL.md")

	if err := os.WriteFile(target, []byte("tampered"), 0o600); err != nil {
		t.Fatalf("tamper SKILL.md: %v", err)
	}
	skillspack.Materialize(dir, skillspack.FS, collectWarnings(&warns))

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after re-materialize: %v", err)
	}
	embedded, err := skillspack.FS.ReadFile(filepath.Join("subagents", "SKILL.md"))
	if err != nil {
		t.Fatalf("read embedded subagents/SKILL.md: %v", err)
	}
	if !bytes.Equal(got, embedded) {
		t.Fatal("tampered file was not restored to the embedded bytes")
	}
}

func TestMaterializeSkipsUnwritableRootWithWarning(t *testing.T) {
	t.Parallel()
	// A data directory nested under a regular file fails every mkdir regardless
	// of privileges, simulating a read-only/unwritable $EITRI_DIR.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup blocker file: %v", err)
	}
	dataDir := filepath.Join(blocker, "eitri")

	var warns []string
	skillspack.Materialize(dataDir, skillspack.FS, collectWarnings(&warns)) // must return without failing boot
	if len(warns) == 0 {
		t.Fatal("warnings = none, want a warn-and-skip warning for an unwritable data directory")
	}
}

func TestMaterializeIncludesBundledResources(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"demo/SKILL.md":       {Data: []byte("---\nname: demo\ndescription: demo\n---\n\nbody")},
		"demo/scripts/run.sh": {Data: []byte("#!/bin/sh\necho hi\n")},
	}
	dir := t.TempDir()
	var warns []string
	skillspack.Materialize(dir, fsys, collectWarnings(&warns))
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings = %v, want none", warns)
	}
	for rel, want := range map[string]string{
		"skills-builtin/demo/SKILL.md":       "---\nname: demo\ndescription: demo\n---\n\nbody",
		"skills-builtin/demo/scripts/run.sh": "#!/bin/sh\necho hi\n",
	} {
		got, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if string(got) != want {
			t.Fatalf("%s content = %q, want %q", rel, got, want)
		}
	}
}
