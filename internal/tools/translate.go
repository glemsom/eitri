// Package tools holds the agent's tool surface: the shared tool registry, the four core tools (bash, read, write, edit), the bwrap sandbox runner, and the single path-namespace translation seam every path-taking tool routes through.
package tools

import (
	"path/filepath"
	"strings"
)

// GUID identifies one run's session temp namespace.
type GUID string

// hostTempPrefix returns the host-form temp root for g, e.g. /tmp/eitri-<GUID>.
func hostTempPrefix(g GUID) string {
	return "/tmp/eitri-" + string(g)
}

// PathTranslator is the single, shared seam that maps the two halves of the path namespace: sandbox /tmp <=> host /tmp/eitri-<GUID>.
type PathTranslator struct {
	g GUID
}

// HostTempFor returns the host-form session temp root for g (e.g. /tmp/eitri-<GUID>).
func HostTempFor(g GUID) string {
	return hostTempPrefix(g)
}

// NewPathTranslator returns a translator for the session temp namespace g.
func NewPathTranslator(g GUID) *PathTranslator {
	return &PathTranslator{g: g}
}

// SandboxToHost translates a model-facing sandbox path to its host form.
func (t *PathTranslator) SandboxToHost(p string) (string, bool) {
	ht := hostTempPrefix(t.g)
	if p == ht || strings.HasPrefix(p, ht+"/") {
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

// Resolve translates a model-supplied path to its host form: sandbox /tmp maps to the session temp host root and a workspace-relative path resolves against the workspace root (bash's cwd).
func (t *PathTranslator) Resolve(p, workspace string) string {
	host, _ := t.SandboxToHost(p)
	if !filepath.IsAbs(host) {
		host = filepath.Join(workspace, host)
	}
	return host
}

// HostToSandbox is the reverse of SandboxToHost: it maps a host temp path back to the sandbox /tmp form the model sees.
func (t *PathTranslator) HostToSandbox(p string) (string, bool) {
	ht := hostTempPrefix(t.g)
	if p == "/tmp" {
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
