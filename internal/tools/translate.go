// Package tools holds the agent's tool surface: the shared tool registry,
// the four core tools (bash, read, write, edit), the bwrap sandbox runner,
// and the single path-namespace translation seam every path-taking tool
// routes through (ADR-0002).
package tools

import "strings"

// GUID identifies one run's session temp namespace. It is the internal host
// detail that must never surface to the model (ADR-0002 decision 5).
type GUID string

// hostTempPrefix returns the host-form temp root for g, e.g. /tmp/eitri-<GUID>.
func hostTempPrefix(g GUID) string {
	return "/tmp/eitri-" + string(g)
}

// PathTranslator is the single, shared seam that maps the two halves of the
// path namespace: sandbox /tmp <=> host /tmp/eitri-<GUID>. Workspace host
// paths are canonical and need no translation (ADR-0002). All path-taking
// tools (bash, write, edit, and later open_in_browser) and their validation
// share one translator so every component resolves the same /tmp namespace.
type PathTranslator struct {
	g GUID
}

// HostTempFor returns the host-form session temp root for g (e.g.
// /tmp/eitri-<GUID>). It is the single source of truth for the temp path; both
// the sandbox mount and the PathTranslator must agree on it, so wiring uses this
// everywhere instead of hand-deriving the path.
func HostTempFor(g GUID) string {
	return hostTempPrefix(g)
}

// NewPathTranslator returns a translator for the session temp namespace g.
// Mirrors the single shared seam requirement: build one per session and route
// every path through it.
func NewPathTranslator(g GUID) *PathTranslator {
	return &PathTranslator{g: g}
}

// SandboxToHost translates a model-facing sandbox path to its host form.
// Only the /tmp root is remapped; workspace host paths and the root temp
// entry point return unchanged. It is idempotent: an already-host-form path
// (one carrying this session's /tmp/eitri-<GUID>) passes through untouched,
// and a host temp path in sandbox form never gets a second GUID segment.
// The returned bool reports whether the path was rewritten.
func (t *PathTranslator) SandboxToHost(p string) (string, bool) {
	ht := hostTempPrefix(t.g)
	if p == ht || strings.HasPrefix(p, ht+"/") {
		// Already host temp form: never double-apply the GUID segment.
		return p, false
	}
	if p == "/tmp" {
		return ht, true
	}
	if strings.HasPrefix(p, "/tmp/") {
		return ht + p[len("/tmp"):], true
	}
	return p, false
}

// HostToSandbox is the reverse of SandboxToHost: it maps a host temp path back
// to the sandbox /tmp form the model sees. Paths not under the session temp
// return unchanged. It is idempotent and reversible with SandboxToHost.
func (t *PathTranslator) HostToSandbox(p string) (string, bool) {
	ht := hostTempPrefix(t.g)
	if p == "/tmp" {
		// Already sandbox temp form (model sees /tmp); never re-apply.
		return p, false
	}
	if p == ht {
		return "/tmp", true
	}
	if strings.HasPrefix(p, ht+"/") {
		return "/tmp" + p[len(ht):], true
	}
	return p, false
}
