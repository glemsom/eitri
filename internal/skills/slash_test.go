package skills

import (
	"testing"
)

func TestParseSlashInput_PlainMessage(t *testing.T) {
	lookup := func(name string) *Skill { return nil }

	result, err := ParseSlashInput("hello world", lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	if result.Prompt != "hello world" {
		t.Errorf("Prompt = %q, want %q", result.Prompt, "hello world")
	}
	if len(result.ActivatedSkills) != 0 {
		t.Errorf("expected 0 activated skills, got %d", len(result.ActivatedSkills))
	}
}

func TestParseSlashInput_EmptyInput(t *testing.T) {
	lookup := func(name string) *Skill { return nil }

	result, err := ParseSlashInput("", lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result for empty input")
	}
}

func TestParseSlashInput_SlashOnly(t *testing.T) {
	lookup := func(name string) *Skill {
		if name == "code-review" {
			return &Skill{Name: "code-review"}
		}
		return nil
	}

	result, err := ParseSlashInput("/code-review", lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	if !result.IsSlashOnly {
		t.Error("expected IsSlashOnly = true")
	}
	if len(result.ActivatedSkills) != 1 || result.ActivatedSkills[0] != "code-review" {
		t.Errorf("ActivatedSkills = %v, want [code-review]", result.ActivatedSkills)
	}
}

func TestParseSlashInput_SlashWithPrompt(t *testing.T) {
	lookup := func(name string) *Skill {
		if name == "code-review" {
			return &Skill{Name: "code-review"}
		}
		return nil
	}

	result, err := ParseSlashInput("/code-review review the changes", lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	if result.IsSlashOnly {
		t.Error("expected IsSlashOnly = false")
	}
	if len(result.ActivatedSkills) != 1 || result.ActivatedSkills[0] != "code-review" {
		t.Errorf("ActivatedSkills = %v, want [code-review]", result.ActivatedSkills)
	}
	if result.Prompt != "review the changes" {
		t.Errorf("Prompt = %q, want %q", result.Prompt, "review the changes")
	}
}

func TestParseSlashInput_MultipleSlashWithPrompt(t *testing.T) {
	lookup := func(name string) *Skill {
		valid := map[string]bool{"code-review": true, "debug": true}
		if valid[name] {
			return &Skill{Name: name}
		}
		return nil
	}

	result, err := ParseSlashInput("/code-review /debug find the bug", lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.ActivatedSkills) != 2 {
		t.Errorf("ActivatedSkills length = %d, want 2", len(result.ActivatedSkills))
	}
	if result.Prompt != "find the bug" {
		t.Errorf("Prompt = %q, want %q", result.Prompt, "find the bug")
	}
}

func TestParseSlashInput_UnknownSkill(t *testing.T) {
	lookup := func(name string) *Skill { return nil }

	_, err := ParseSlashInput("/unknown", lookup)
	if err == nil {
		t.Fatal("expected error for unknown skill")
	}
	if _, ok := err.(*UnknownCommandError); !ok {
		t.Errorf("expected UnknownCommandError, got %T: %v", err, err)
	}
}

func TestParseSlashInput_ReservedCommand(t *testing.T) {
	lookup := func(name string) *Skill {
		if name == "help" {
			return &Skill{Name: "help"}
		}
		return nil
	}

	_, err := ParseSlashInput("/help", lookup)
	if err == nil {
		t.Fatal("expected error for reserved command")
	}
}

func TestParseSlashInput_SlashThenPlain(t *testing.T) {
	lookup := func(name string) *Skill {
		if name == "code-review" {
			return &Skill{Name: "code-review"}
		}
		return nil
	}

	result, err := ParseSlashInput("/code-review and then some text", lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.ActivatedSkills) != 1 {
		t.Errorf("ActivatedSkills length = %d, want 1", len(result.ActivatedSkills))
	}
	if result.Prompt != "and then some text" {
		t.Errorf("Prompt = %q, want %q", result.Prompt, "and then some text")
	}
}

func TestIsValidSkillName(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"code-review", true},
		{"my-skill-123", true},
		{"test", true},
		{"a", true},
		{"123", true},
		{"-bad", false},
		{"UPPERCASE", false},
		{"has_underscore", false},
		{"has space", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isValidSkillName(tt.name)
		if got != tt.valid {
			t.Errorf("isValidSkillName(%q) = %v, want %v", tt.name, got, tt.valid)
		}
	}
}

func TestIsSlashCommand(t *testing.T) {
	if !IsSlashCommand("/code-review") {
		t.Error("expected /code-review to be a slash command")
	}
	if !IsSlashCommand(" /code-review") {
		t.Error("expected '/ /code-review' to be a slash command")
	}
	if IsSlashCommand("not a slash") {
		t.Error("expected 'not a slash' to not be a slash command")
	}
	if IsSlashCommand("") {
		t.Error("expected empty string to not be a slash command")
	}
}
