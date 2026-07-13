package skills

import (
	"strings"
)

// reservedCommands are built-in slash commands that shadow skill names.
var reservedCommands = map[string]bool{
	"/help": true, "/settings": true, "/skills": true, "/clear": true, "/new": true,
}

// SlashParseResult holds the parsed result of a slash command input.
type SlashParseResult struct {
	ActivatedSkills []string // skills activated before the prompt
	Prompt          string   // remaining prompt text
	IsSlashOnly     bool     // true if input was only slashes (no prompt after skills)
}

// ParseSlashInput parses chat input for skill slash commands.
// Format:
//   /skill-name                          → activate skill, no LLM run
//   /skill-name prompt text...           → activate skill + send prompt
//   /skill-a /skill-b prompt text...     → activate multiple + send prompt
//   /unknown                             → returns error
//
// Returns the parsed result or an error for unknown commands.
func ParseSlashInput(input string, lookupFn func(name string) *Skill) (*SlashParseResult, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, nil
	}

	// Not a slash command
	if !strings.HasPrefix(input, "/") {
		return &SlashParseResult{Prompt: input}, nil
	}

	// Parse leading slash tokens
	tokens := strings.Fields(input)
	var activatedSkills []string
	var remainingTokens []string
	allSlash := true

	for i, token := range tokens {
		if !strings.HasPrefix(token, "/") {
			remainingTokens = tokens[i:]
			allSlash = false
			break
		}

		// Check for reserved command
		if reservedCommands[token] {
			return nil, &UnknownCommandError{Command: token}
		}

		// Validate skill name pattern: /[a-z0-9][a-z0-9-]*
		skillName := token[1:] // strip leading /
		if !isValidSkillName(skillName) {
			return nil, &UnknownCommandError{Command: token}
		}

		// Lookup skill
		skill := lookupFn(skillName)
		if skill == nil {
			return nil, &UnknownCommandError{Command: token}
		}

		activatedSkills = append(activatedSkills, skillName)
	}

	if allSlash {
		// Only slash tokens, no prompt
		return &SlashParseResult{
			ActivatedSkills: activatedSkills,
			IsSlashOnly:     true,
		}, nil
	}

	prompt := strings.TrimSpace(strings.Join(remainingTokens, " "))
	return &SlashParseResult{
		ActivatedSkills: activatedSkills,
		Prompt:          prompt,
	}, nil
}

// isValidSkillName checks that a skill name matches the pattern [a-z0-9][a-z0-9-]*.
func isValidSkillName(name string) bool {
	if len(name) == 0 {
		return false
	}
	for i, c := range name {
		if c >= 'a' && c <= 'z' {
			continue
		}
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '-' && i > 0 {
			continue
		}
		return false
	}
	return true
}

// UnknownCommandError is returned when a slash command doesn't match any skill or reserved command.
type UnknownCommandError struct {
	Command string
}

func (e *UnknownCommandError) Error() string {
	return "unknown skill or command: " + e.Command
}

// IsSlashCommand returns true if the input starts with /.
func IsSlashCommand(input string) bool {
	return strings.HasPrefix(strings.TrimSpace(input), "/")
}
