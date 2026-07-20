package templates

import (
	"html"
	"path/filepath"
	"strings"
)

// nl2br HTML-escapes the input and replaces newlines with <br> tags.
// Used for user messages which are plain text, not Markdown.
// Order matters: handle \r\n before standalone \n to avoid doubling.
func nl2br(s string) string {
	s = html.EscapeString(s)
	s = strings.ReplaceAll(s, "\r\n", "<br>")
	s = strings.ReplaceAll(s, "\n", "<br>")
	s = strings.ReplaceAll(s, "\r", "<br>")
	return s
}

// pathBase returns the last component of a file path.
// Used in templates to show only the basename of the workspace path.
func pathBase(path string) string {
	return filepath.Base(path)
}

// scopeLabel returns a human-readable label for a skill scope.
func scopeLabel(s string) string {
	switch s {
	case "project-eitri", "project-agents":
		return "Project"
	case "user-eitri", "user-agents":
		return "User"
	default:
		return s
	}
}

// scopeIcon returns an SVG element for a skill scope.
func scopeIcon(s string) string {
	switch s {
	case "project-eitri", "project-agents":
		return `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>`
	default:
		return `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>`
	}
}

// statusDot returns a colored dot SVG for a skill status.
func statusDot(status string) string {
	switch status {
	case "effective":
		return `<svg width="10" height="10" viewBox="0 0 10 10" fill="var(--success)"><circle cx="5" cy="5" r="5"/></svg>`
	case "disabled", "shadowed":
		return `<svg width="10" height="10" viewBox="0 0 10 10" fill="var(--text-muted)"><circle cx="5" cy="5" r="5"/></svg>`
	case "invalid":
		return `<svg width="10" height="10" viewBox="0 0 10 10" fill="var(--error)"><circle cx="5" cy="5" r="5"/></svg>`
	default:
		return `<svg width="10" height="10" viewBox="0 0 10 10" fill="var(--text-muted)"><circle cx="5" cy="5" r="5"/></svg>`
	}
}
