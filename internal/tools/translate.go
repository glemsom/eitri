// Package tools holds the agent's tool surface: the shared tool registry, the core tools (bash, open_in_browser), the bwrap sandbox runner, and the single path-namespace translation seam every path-taking tool routes through. Web fetching happens inside the bash tool via curl.
package tools

import "path/filepath"

// GUID identifies one run.
type GUID string

// PathTranslator is the shared seam for path-taking host-side tools. Session temp now lives at the same absolute path inside and outside the sandbox, so translation is identity; Resolve still applies bash's workspace-relative base.
type PathTranslator struct{}

// NewPathTranslator returns a translator for the current path namespace.
func NewPathTranslator() *PathTranslator {
	return &PathTranslator{}
}

// SandboxToHost translates a model-facing sandbox path to its host form.
func (t *PathTranslator) SandboxToHost(p string) (string, bool) {
	return p, false
}

// Resolve translates a model-supplied path to its host form; workspace-relative paths resolve against the workspace root (bash's cwd).
func (t *PathTranslator) Resolve(p, workspace string) string {
	host, _ := t.SandboxToHost(p)
	if !filepath.IsAbs(host) {
		host = filepath.Join(workspace, host)
	}
	return host
}

// HostToSandbox is the reverse of SandboxToHost.
func (t *PathTranslator) HostToSandbox(p string) (string, bool) {
	return p, false
}
