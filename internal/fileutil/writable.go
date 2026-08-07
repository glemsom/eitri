package fileutil

import (
	"path/filepath"
	"strings"
)

// TmpdirFor resolves a session ID to the host path of that session's
// session-scoped sandbox tmpdir, and whether it is currently tracked. It is
// the same callback shape sandbox.Manager.TmpdirFor exposes, so write/edit
// tools can map sandbox /tmp paths back to the host (ADR-0026).
type TmpdirFor func(sessionID string) (string, bool)

// ResolveWritablePath resolves a write/edit tool target to the absolute host
// path it may be written to, applying the shared writable-path rules:
//
//   - A target starting with /tmp/ is rewritten to the session's sandbox
//     tmpdir on the host when that tmpdir is tracked for the session
//     (ADR-0026); otherwise the /tmp/ path passes through unchanged —
//     identical fallback semantics to open_in_browser.
//   - The (possibly rewritten) path is then validated against the workspace
//     root and the given writable roots via ValidatePathWithAllowed. A target
//     outside all roots is a hard error — the caller never shows a
//     confirmation prompt for it.
//
// tmpdirFor may be nil and sessionID may be empty — both disable /tmp
// rewriting, which is safe when no sandbox is in use.
func ResolveWritablePath(path, workspace string, writableRoots []string, sessionID string, tmpdirFor TmpdirFor) (string, error) {
	resolved := rewriteTmp(path, sessionID, tmpdirFor)
	return ValidatePathWithAllowed(resolved, workspace, writableRoots)
}

// rewriteTmp maps a sandbox /tmp/... path to the matching host path for the
// session, but only when that session's tmpdir is currently tracked;
// otherwise the input path is returned unchanged (ADR-0026).
func rewriteTmp(path, sessionID string, tmpdirFor TmpdirFor) string {
	if !strings.HasPrefix(path, "/tmp/") || tmpdirFor == nil || sessionID == "" {
		return path
	}
	hostDir, ok := tmpdirFor(sessionID)
	if !ok || hostDir == "" {
		return path
	}
	return filepath.Join(hostDir, strings.TrimPrefix(path, "/tmp/"))
}
