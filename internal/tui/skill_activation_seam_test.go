package tui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func plainTestTheme() Theme {
	var plain lipgloss.Style // zero-value style: Render passes text through
	return Theme{slashSelectStyle: plain, statusStyle: plain}
}

func TestSkillActivation_commandViaSeam(t *testing.T) {
	t.Parallel()
	s := NewSkillActivation(Dependencies{Skills: &SkillsSurface{Items: []SkillItem{{Name: "review"}, {Name: "plan"}}}})

	name, args, ok := s.Command("/review fix the bug")
	if !ok || name != "review" || args != "fix the bug" {
		t.Fatalf("Command(/review fix the bug) = %q, %q, %v", name, args, ok)
	}
	if _, _, ok := s.Command("/usr/bin/env"); ok {
		t.Fatal("real path must not be a command")
	}
}

func TestSkillActivation_renderCompletionListsCandidates(t *testing.T) {
	t.Parallel()
	s := NewSkillActivation(Dependencies{Skills: &SkillsSurface{Items: []SkillItem{{Name: "review"}, {Name: "plan"}}}})

	s.TrackComposer("/")
	var b strings.Builder
	s.RenderCompletion(&b, plainTestTheme())
	out := b.String()
	for _, want := range []string{"/settings", "/copy", "/login", "/help", "/review", "/plan"} {
		if !strings.Contains(out, want) {
			t.Errorf("completion output %q missing %q", out, want)
		}
	}
	if n := s.CandidateCount(); n != 6 {
		t.Errorf("CandidateCount() = %d, want 6", n)
	}
	s.TrackComposer("hello")
	if n := s.CandidateCount(); n != 0 {
		t.Errorf("CandidateCount() after plain text = %d, want 0", n)
	}
}

func TestSkillActivation_renderCompletionHighlightsSelected(t *testing.T) {
	t.Parallel()
	s := NewSkillActivation(Dependencies{Skills: &SkillsSurface{Items: []SkillItem{{Name: "review"}}}})
	s.TrackComposer("/")
	s.Move(1)

	var b strings.Builder
	s.RenderCompletion(&b, plainTestTheme())
	if !strings.Contains(b.String(), "▸ /copy") {
		t.Errorf("selected candidate not highlighted: %q", b.String())
	}
	var accepted string
	if !s.Complete(func(candidate string) { accepted = candidate }) || accepted != "/copy" {
		t.Errorf("accepted candidate = %q, want /copy", accepted)
	}
	if s.isOpen() {
		t.Error("accepted completion should close menu")
	}
}
