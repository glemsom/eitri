package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// SkillActivation owns the TUI's slash-command surface: slash-command parsing,
// candidate listing, completion cycling, and skill activation. The Model
// delegates to it and carries no slash state of its own.
type SkillActivation struct {
	skills      []SkillItem
	slashIdx    int
	slashPrefix string
}

// NewSkillActivation captures the detected skills at construction so the
// slash completion has a stable list even if the Dependencies snapshot is nil
// or empty.
func NewSkillActivation(d Dependencies) *SkillActivation {
	if d.Skills != nil {
		return &SkillActivation{skills: d.Skills.Items}
	}
	return &SkillActivation{}
}

// Command reports whether prompt is a `/skillname` activation command for a detected skill.
func (s *SkillActivation) Command(prompt string) (name, args string, ok bool) {
	return slashCommand(prompt, s.skills)
}

// Candidates returns the ordered slash-command completion candidates for the current composer value: the built-in commands first, then every detected skill whose name starts with the `/...` partial.
func (s *SkillActivation) Candidates(value string) []string {
	return slashCandidates(value, s.skills)
}

// Prefix returns the current slash prefix tracked from the composer.
func (s *SkillActivation) Prefix() string { return s.slashPrefix }

// Reset clears the tracked prefix and the completion cycle index, e.g. after a submitted prompt.
func (s *SkillActivation) Reset() {
	s.slashIdx = 0
	s.slashPrefix = ""
}

// TrackComposer records the composer value after a composer update: a leading
// slash becomes the completion prefix with a fresh cycle index; anything else
// clears both.
func (s *SkillActivation) TrackComposer(value string) {
	if strings.HasPrefix(value, "/") {
		s.slashPrefix = value
		s.slashIdx = 0
	} else {
		s.slashPrefix = ""
		s.slashIdx = 0
	}
}

// Complete fills the composer through apply with the next completion candidate,
// cycling deterministicly through the built-in commands and matching detected skills.
func (s *SkillActivation) Complete(apply func(candidate string)) {
	cands := slashCandidates(s.slashPrefix, s.skills)
	if len(cands) == 0 {
		return
	}
	if s.slashIdx < 0 || s.slashIdx >= len(cands) {
		s.slashIdx = 0
	}
	apply(cands[s.slashIdx])
	s.slashIdx = (s.slashIdx + 1) % len(cands)
}

// Activate runs one slash-command activation through the SkillsSurface activation seam on a detached command; it appends the invocation to the transcript and reports a failure note when no activation seam is wired. The resulting payload is injected into the follow-up agent turn's context so the model acts on the skill instructions.
func (s *SkillActivation) Activate(tx *Transcript, surface *SkillsSurface, name, args string) tea.Cmd {
	tx.appendUserMsg("/" + name)
	if surface == nil || surface.Activate == nil {
		tx.appendMsg(failurePrefix() + "no skill activation available")
		return nil
	}
	return skillCmd(surface.Activate, name, args)
}

// CandidateCount returns how many slash-completion rows the composer value renders above the composer.
func (s *SkillActivation) CandidateCount(value string) int {
	return len(slashCandidates(value, s.skills))
}

// RenderCompletion appends the slash-command completion list to the view: the built-in slash commands plus any matching detected skills, with the current cycle selection highlighted. The composer value selects the highlight when it matches a candidate exactly.
func (s *SkillActivation) RenderCompletion(b *strings.Builder, th Theme, value string) {
	cands := slashCandidates(value, s.skills)
	if len(cands) == 0 {
		return
	}
	sel := s.slashIdx
	for i, c := range cands {
		if c == value {
			sel = i
			break
		}
	}
	if sel < 0 || sel >= len(cands) {
		sel = 0
	}
	for i, c := range cands {
		if i == sel {
			b.WriteString(th.slashSelectStyle.Render(g("▸ ", "> ") + c))
		} else {
			b.WriteString(th.statusStyle.Render("  " + c))
		}
		b.WriteString("\n")
	}
}

// TurnPrompt returns the agent-turn prompt for a completed activation: the trailing args when present, otherwise a default apply-skill prompt so a bare `/skillname` still starts a turn.
func (s *SkillActivation) TurnPrompt(name, args string) string {
	if args != "" {
		return args
	}
	return fmt.Sprintf("apply the %s skill", name)
}

// slashCommand reports whether prompt is a `/skillname` activation command for a detected skill.
func slashCommand(prompt string, skills []SkillItem) (name, args string, ok bool) {
	if len(skills) == 0 || !strings.HasPrefix(prompt, "/") {
		return "", "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(prompt, "/"))
	name, args = rest, ""
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		name = rest[:i]
		args = rest[i+1:]
	}
	if name == "" {
		return "", "", false
	}
	for _, it := range skills {
		if it.Name == name {
			return name, strings.TrimSpace(args), true
		}
	}
	return "", "", false
}

// skillCmd runs a skill activation off the main loop and reports its payload. name and args ride along
// on skillDoneMsg so the handler can start the agent turn (args as the prompt, or a default prompt for
// a bare `/skillname`) with the payload injected into context.
func skillCmd(activate func(ctx context.Context, name string) (string, error), name, args string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	return tea.Cmd(func() tea.Msg {
		defer cancel()
		return runSkillActivation(ctx, activate, name, args, 0)
	})
}

func skillCmdWithContext(ctx context.Context, activate func(ctx context.Context, name string) (string, error), name, args string, seq int) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		return runSkillActivation(ctx, activate, name, args, seq)
	})
}

func runSkillActivation(ctx context.Context, activate func(ctx context.Context, name string) (string, error), name, args string, seq int) tea.Msg {
	payload, err := activate(ctx, name)
	if err != nil {
		return skillDoneMsg{name: name, args: args, err: err, seq: seq}
	}
	return skillDoneMsg{name: name, payload: payload, args: args, seq: seq}
}

// slashCandidates returns the ordered slash-command completion candidates for the current composer value: the built-in `/settings`, `/copy`, `/login`, and `/help` commands first, then every detected skill whose name starts with the `/...` partial.
func slashCandidates(value string, skills []SkillItem) []string {
	if !strings.HasPrefix(value, "/") {
		return nil
	}
	partial := strings.TrimSpace(strings.TrimPrefix(value, "/"))
	cands := make([]string, 0, len(skills)+4)
	if partial == "" || strings.HasPrefix("settings", partial) {
		cands = append(cands, "/settings")
	}
	if partial == "" || strings.HasPrefix("copy", partial) {
		cands = append(cands, "/copy")
	}
	if partial == "" || strings.HasPrefix("login", partial) {
		cands = append(cands, "/login")
	}
	if partial == "" || strings.HasPrefix("help", partial) {
		cands = append(cands, "/help")
	}
	for _, it := range skills {
		if it.Name == "settings" {
			continue
		}
		if strings.HasPrefix(it.Name, partial) {
			cands = append(cands, "/"+it.Name)
		}
	}
	return cands
}
