package templates

import (
	"strings"
	"testing"
)

// ─── pathBase ──────────────────────────────────────────────────────────

func TestPathBase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "."},
		{"/", "/"},
		{"/home/user", "user"},
		{"relative/path/file.txt", "file.txt"},
		{"singlefile", "singlefile"},
		{"/root/", "root"},
		{"trailing/slash/", "slash"},
	}
	for _, tt := range tests {
		got := pathBase(tt.input)
		if got != tt.want {
			t.Errorf("pathBase(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ─── scopeLabel ────────────────────────────────────────────────────────

func TestScopeLabel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"project-eitri", "Project"},
		{"project-agents", "Project"},
		{"user-eitri", "User"},
		{"user-agents", "User"},
		{"unknown", "unknown"},
		{"", ""},
		{"project", "project"},
	}
	for _, tt := range tests {
		got := scopeLabel(tt.input)
		if got != tt.want {
			t.Errorf("scopeLabel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ─── scopeIcon ─────────────────────────────────────────────────────────

func TestScopeIcon(t *testing.T) {
	tests := []struct {
		input string
		want  string // substring we expect in the output
	}{
		{"project-eitri", `<path d="M22 19a2`},
		{"project-agents", `<path d="M22 19a2`},
		{"user-eitri", `<circle cx="12" cy="12" r="10"/>`},
		{"user-agents", `<circle cx="12" cy="12" r="10"/>`},
		{"unknown", `<circle cx="12" cy="12" r="10"/>`},
		{"", `<circle cx="12" cy="12" r="10"/>`},
	}
	for _, tt := range tests {
		got := scopeIcon(tt.input)
		if !strings.Contains(got, tt.want) {
			t.Errorf("scopeIcon(%q) = %q, missing substring %q", tt.input, got, tt.want)
		}
	}
}

// ─── statusDot ─────────────────────────────────────────────────────────

func TestStatusDot(t *testing.T) {
	tests := []struct {
		input string
		want  string // substring expected in the output
	}{
		{"effective", "var(--success)"},
		{"disabled", "var(--text-muted)"},
		{"shadowed", "var(--text-muted)"},
		{"invalid", "var(--error)"},
		{"unknown-status", "var(--text-muted)"},
		{"", "var(--text-muted)"},
	}
	for _, tt := range tests {
		got := statusDot(tt.input)
		if !strings.Contains(got, tt.want) {
			t.Errorf("statusDot(%q) = %q, missing substring %q", tt.input, got, tt.want)
		}
	}
}

// ─── gravatarURL ──────────────────────────────────────────────────────

func TestGravatarURL_Empty(t *testing.T) {
	if got := gravatarURL(""); got != "" {
		t.Errorf("gravatarURL('') = %q, want empty", got)
	}
}

func TestGravatarURL_OnlySpaces(t *testing.T) {
	if got := gravatarURL("   "); got != "" {
		t.Errorf("gravatarURL('   ') = %q, want empty", got)
	}
}

func TestGravatarURL_KnownHash(t *testing.T) {
	// MD5 of "test@example.com" (lowercased, trimmed) = 55502f40dc8b7c769880b10874abc9d0
	want := "https://www.gravatar.com/avatar/55502f40dc8b7c769880b10874abc9d0?s=32&d=mp"
	if got := gravatarURL("test@example.com"); got != want {
		t.Errorf("gravatarURL('test@example.com') = %q, want %q", got, want)
	}
}

func TestGravatarURL_CaseInsensitive(t *testing.T) {
	want := "https://www.gravatar.com/avatar/55502f40dc8b7c769880b10874abc9d0?s=32&d=mp"
	if got := gravatarURL("TEST@EXAMPLE.COM"); got != want {
		t.Errorf("gravatarURL('TEST@EXAMPLE.COM') = %q, want %q", got, want)
	}
}

func TestGravatarURL_UsesDMPFallback(t *testing.T) {
	got := gravatarURL("someone@example.com")
	if !strings.HasSuffix(got, "&d=mp") {
		t.Errorf("gravatarURL('someone@example.com') = %q, does not end with &d=mp", got)
	}
}

func TestGravatarURL_IncludesSize32(t *testing.T) {
	got := gravatarURL("someone@example.com")
	if !strings.Contains(got, "s=32") {
		t.Errorf("gravatarURL('someone@example.com') = %q, does not contain s=32", got)
	}
}

// ─── sandboxBadge ─────────────────────────────────────────────────────

func TestSandboxBadge_Active(t *testing.T) {
	got := sandboxBadge("default", true)
	if !strings.Contains(got, "sandbox-badge--ok") {
		t.Errorf("active badge missing --ok class: %q", got)
	}
	if !strings.Contains(got, "Sandbox active") {
		t.Errorf("active badge missing text: %q", got)
	}
}

func TestSandboxBadge_DisabledByProfile(t *testing.T) {
	got := sandboxBadge("none", true)
	if !strings.Contains(got, "sandbox-badge--warn") {
		t.Errorf("disabled badge missing --warn class: %q", got)
	}
	if !strings.Contains(got, "Sandbox disabled") {
		t.Errorf("disabled badge missing text: %q", got)
	}
}

func TestSandboxBadge_BwrapNotFound(t *testing.T) {
	got := sandboxBadge("default", false)
	if !strings.Contains(got, "sandbox-badge--warn") {
		t.Errorf("unavailable badge missing --warn class: %q", got)
	}
	if !strings.Contains(got, "bwrap not found") {
		t.Errorf("unavailable badge missing text: %q", got)
	}
}

func TestSandboxBadge_DisabledProfileNoBwrap(t *testing.T) {
	got := sandboxBadge("none", false)
	if !strings.Contains(got, "sandbox-badge--warn") {
		t.Errorf("disabled badge missing --warn class: %q", got)
	}
	if !strings.Contains(got, "Sandbox disabled") {
		t.Errorf("disabled badge text expected first: %q", got)
	}
}
