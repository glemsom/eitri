package skillspack

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Warnf reports a non-fatal materialization problem; the caller surfaces it
// (stderr/log) and boot continues without builtin skills.
type Warnf func(format string, args ...any)

// BuiltinRootName is the data-directory relative path the materialized builtin
// skill packs land under: <dataDir>/skills-builtin. The app feeds this root to
// skill discovery as the "builtin" scope.
const BuiltinRootName = "skills-builtin"

// Materialize writes the builtin skill packs embedded in fsys into
// <dataDir>/skills-builtin, one directory per pack: SKILL.md plus any bundled
// resource files, preserving the embedded layout. The directory is
// binary-owned ROM: on each launch every file is compared byte-for-byte against
// the embedded content and rewritten only when it differs, so binary upgrades
// win and edits to a materialized copy are reverted on the next launch.
// Failures are warn-and-skip: problems are reported through warn and boot
// continues without builtin skills.
func Materialize(dataDir string, fsys fs.FS, warn Warnf) {
	root := filepath.Join(dataDir, BuiltinRootName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		warn("builtin skills: cannot create %s: %v (boot continues without builtin skills)", root, err)
		return
	}

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		warn("builtin skills: cannot scan embedded packs: %v", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if err := materializePack(fsys, name, filepath.Join(root, name)); err != nil {
			warn("builtin skill %q: %v", name, err)
		}
	}
}

// materializePack writes one embedded pack (name) into its target directory.
// The pack directory is created unconditionally (MkdirAll is a no-op when it
// already exists); each file is rewritten only when its bytes differ from the
// embedded content, leaving identical files and their timestamps untouched.
func materializePack(fsys fs.FS, name, target string) error {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	return fs.WalkDir(fsys, name, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == name {
			return nil // the pack root directory, already created
		}
		rel := strings.TrimPrefix(path, name+"/")
		dst := filepath.Join(target, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		embedded, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		existing, readErr := os.ReadFile(dst)
		if readErr == nil && bytes.Equal(existing, embedded) {
			return nil // identical: leave the file (and its timestamp) alone
		}
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		return os.WriteFile(dst, embedded, 0o600)
	})
}