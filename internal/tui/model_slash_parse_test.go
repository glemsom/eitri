package tui

import (
	"testing"
)

// TestModel_parseSkillNameAndArgs is the acceptance boundary for issue
// #238: slashCommand must recognise both a bare `/skillname` and a
// `/skillname <args>` line, splitting the trailing args on the first
// whitespace after the detected skill name. It must leave every other `/...`
// line untouched (real paths and unknown skills fall through as a normal
// prompt), and never return args when the line is skill-only.
func TestModel_parseSkillNameAndArgs(t *testing.T) {
	t.Parallel()
	skills := []SkillItem{{Name: "review"}, {Name: "plan"}}

	tests := []struct {
		label    string
		prompt   string
		wantName string
		wantArgs string
		wantOK   bool
	}{
		{
			label:    "exact bare skill name",
			prompt:   "/review",
			wantName: "review",
			wantArgs: "",
			wantOK:   true,
		},
		{
			label:    "bare skill name with single trailing space resolves to no args",
			prompt:   "/review ",
			wantName: "review",
			wantArgs: "",
			wantOK:   true,
		},
		{
			label:    "bare skill name with trailing tab resolves to no args",
			prompt:   "/plan\t",
			wantName: "plan",
			wantArgs: "",
			wantOK:   true,
		},
		{
			label:    "skill name with single space args",
			prompt:   "/review fix the bug",
			wantName: "review",
			wantArgs: "fix the bug",
			wantOK:   true,
		},
		{
			label:    "skill name with tab-separated args",
			prompt:   "/plan\tnow please",
			wantName: "plan",
			wantArgs: "now please",
			wantOK:   true,
		},
		{
			label:    "skill name followed by spaces before args trims them",
			prompt:   "/review   a b",
			wantName: "review",
			wantArgs: "a b",
			wantOK:   true,
		},
		{
			label:    "args keep trailing whitespace trimmed",
			prompt:   "/review a b  ",
			wantName: "review",
			wantArgs: "a b",
			wantOK:   true,
		},
		{
			label:    "unknown skill with args falls through",
			prompt:   "/unknown call me",
			wantName: "",
			wantArgs: "",
			wantOK:   false,
		},
		{
			label:    "real path is a normal prompt",
			prompt:   "/usr/bin/env",
			wantName: "",
			wantArgs: "",
			wantOK:   false,
		},
		{
			label:    "non-slash line is not a command",
			prompt:   "review the code",
			wantName: "",
			wantArgs: "",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			name, args, ok := slashCommand(tt.prompt, skills)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v for prompt %q", ok, tt.wantOK, tt.prompt)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q for prompt %q", name, tt.wantName, tt.prompt)
			}
			if args != tt.wantArgs {
				t.Errorf("args = %q, want %q for prompt %q", args, tt.wantArgs, tt.prompt)
			}
		})
	}
}
