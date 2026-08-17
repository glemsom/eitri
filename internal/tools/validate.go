package tools

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Validator checks a model-supplied path against the writable roots on the
// translated (host) form. It is the write-side seam used
// by write and edit: every target resolves to a host path and is rejected with
// a hard error unless it lands inside the workspace, a configured extra
// writable path, or the session temp.
type Validator struct {
	workspace string
	extra     []string
	tr        *PathTranslator
}

// NewValidator builds the write/edit path guard. workspace is the workspace
// root (host-absolute); extra are configured extra_writable_paths
// (host-absolute); tr is the single shared PathTranslator for the session.
func NewValidator(workspace string, extra []string, tr *PathTranslator) *Validator {
	return &Validator{workspace: filepath.Clean(workspace), extra: cleanAll(extra), tr: tr}
}

// Resolve translates a sandbox-side path p into its host form and validates it
// against the writable roots. It returns the resolved host path on success,
// or a hard error when p lands outside every writable root. Workspace host
// paths are canonical (no rewrite); sandbox /tmp targets resolve to the
// session temp host root, which is itself a writable root.
func (v *Validator) Resolve(p string) (string, error) {
	host := v.tr.Resolve(p, v.workspace)
	if _, ok := v.inside(host); ok {
		return host, nil
	}
	return "", fmt.Errorf("path %q is not inside any writable root (workspace, extra_writable_paths, or session temp)", p)
}

// inside reports whether host lives within any writable root, using exact
// path-element boundaries so a sibling with a shared prefix is not accepted.
func (v *Validator) inside(host string) (string, bool) {
	roots := append([]string{v.workspace, v.tr.SandboxToHostTemp()}, v.extra...)
	for _, r := range roots {
		if within(r, host) {
			return r, true
		}
	}
	return "", false
}

// within reports whether child equals root or descends from it at a path
// element boundary.
func within(root, child string) bool {
	if child == root {
		return true
	}
	if !strings.HasPrefix(child, root) {
		return false
	}
	return len(child) > len(root) && (child[len(root)] == '/')
}

// SandboxToHostTemp exposes the session temp host root (e.g.
// /tmp/eitri-<GUID>); it is a shorthand used by the validator so the temp is
// always a writable root.
func (t *PathTranslator) SandboxToHostTemp() string {
	return hostTempPrefix(t.g)
}

func cleanAll(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Clean(p)
	}
	return out
}
