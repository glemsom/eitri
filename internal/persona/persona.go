// Package persona manages agent personas — named profiles with a system prompt
// and optional required skills.
//
// Personas are stored as YAML files under ~/.eitri/personas/<name>.yaml (user-level).
// Unlike skills (which support workspace-level overrides), personas are always
// user-level — they represent the user's agent behaviour preferences, not
// project-specific capabilities.
package persona

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultPrompt is the built-in system prompt used when no persona is active.
	// It should be kept in sync with history.DefaultSystemPrompt.
	DefaultPrompt = `You are Eitri, an expert AI coding agent. You can help the user by reading/writing/editing files, executing commands - and giving recommendations to the user.

## Core behavior
- Be concise. Prefer the simplest correct solution. Avoid overengineering.
- Prefer small, focused edits over large rewrites. Preserve existing style.
- Remove imports or code left unused by your changes.
- Before reading a file, first use grep to locate relevant code by regex. Then use read with start_line and end_line (populated from grep's output line numbers) to read only the needed section. Avoid reading entire files unless grep confirms the full content is relevant.
- The chat UI renders Markdown (headings, tables, lists, code blocks, blockquotes, math $...$). Use it to structure responses clearly.

## Reasoning budget (important)
- Keep internal reasoning concise. Do not restate the full task or re-explain what you already know.
- Act early: start emitting read/write/grep/bash tool calls once you have a plan, instead of drafting the entire implementation in prose first.
- Do not design every line of code in thinking before touching a file; sketch briefly, then build incrementally via tool calls.
- If you have already read a file or confirmed a fact in this session, do not re-read it.

## Tool usage
- When you need to ask the user a multi-choice question (e.g. which option to proceed with, which fix to apply), use render_quick_replies with the options array instead of asking in plain text — it renders clickable buttons the user can tap.`

	// MaxCustomPersonas is the maximum number of custom personas allowed (not counting generic).
	MaxCustomPersonas = 10

	// GenericName is the name of the built-in generic persona.
	GenericName = "generic"

	// personasDirName is the subdirectory under .eitri where persona files are stored.
	personasDirName = "personas"
)

// SaveToHome saves a persona definition to the user-level home directory (~/.eitri/personas/).
// Unlike Save (which writes to workspace-scoped .eitri/personas/), this always writes to
// the user's home configuration directory regardless of the current workspace.
func SaveToHome(homeDir string, def *PersonaDefinition) error {
	if def == nil {
		return fmt.Errorf("persona definition is nil")
	}
	if strings.TrimSpace(def.Name) == "" {
		return fmt.Errorf("persona name must not be empty")
	}

	personaDir := UserDir(homeDir)
	if err := os.MkdirAll(personaDir, 0700); err != nil {
		return fmt.Errorf("create home personas dir: %w", err)
	}

	data, err := yaml.Marshal(def)
	if err != nil {
		return fmt.Errorf("marshal persona: %w", err)
	}

	path := filepath.Join(personaDir, sanitizeName(def.Name)+".yaml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write home persona file: %w", err)
	}
	return nil
}

// PersonaDefinition represents a single persona.
type PersonaDefinition struct {
	Name           string   `yaml:"name" json:"name"`
	SystemPrompt   string   `yaml:"system_prompt" json:"system_prompt"`
	RequiredSkills []string `yaml:"required_skills,omitempty" json:"required_skills,omitempty"`
}

// UserDir returns the user-level personas directory under the home .eitri.
func UserDir(homeDir string) string {
	return filepath.Join(homeDir, ".eitri", personasDirName)
}

// Save writes a persona definition to the user-level home directory (~/.eitri/personas/).
// Personas are user-level, not workspace-scoped.
func Save(workspace string, def *PersonaDefinition) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve user home dir: %w", err)
	}
	return SaveToHome(homeDir, def)
}

// Load reads a persona definition from the user-level home directory (~/.eitri/personas/).
// The workspace parameter is accepted for API compatibility but is ignored —
// personas are always loaded from the user's home directory.
func Load(workspace, name string) (*PersonaDefinition, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home dir: %w", err)
	}
	return LoadWithHome(workspace, homeDir, name)
}

// LoadWithHome reads a persona definition from the user-level home directory.
// The workspace parameter is accepted for API compatibility but is ignored.
func LoadWithHome(workspace, homeDir, name string) (*PersonaDefinition, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("persona name must not be empty")
	}

	if homeDir == "" {
		return nil, fmt.Errorf("home dir must not be empty")
	}

	path := filepath.Join(UserDir(homeDir), sanitizeName(name)+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("persona %q not found", name)
		}
		return nil, fmt.Errorf("read persona file: %w", err)
	}

	var def PersonaDefinition
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("unmarshal persona: %w", err)
	}
	return &def, nil
}

// Delete removes a persona file from the user-level home directory.
// The workspace parameter is accepted for API compatibility but is ignored.
func Delete(workspace, name string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve user home dir: %w", err)
	}
	return DeleteWithHome(workspace, homeDir, name)
}

// DeleteWithHome removes a persona file from the user-level home directory.
// The workspace parameter is accepted for API compatibility but is ignored.
func DeleteWithHome(workspace, homeDir, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("persona name must not be empty")
	}

	if homeDir == "" {
		return fmt.Errorf("home dir must not be empty")
	}

	path := filepath.Join(UserDir(homeDir), sanitizeName(name)+".yaml")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("persona %q not found", name)
		}
		return fmt.Errorf("delete persona file: %w", err)
	}
	return nil
}

// List returns persona names from the user-level home directory.
// The workspace parameter is accepted for API compatibility but is ignored.
func List(workspace string) ([]string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home dir: %w", err)
	}
	return ListWithHome(workspace, homeDir)
}

// ListWithHome enumerates personas from the user-level home directory.
// The workspace parameter is accepted for API compatibility but is ignored.
func ListWithHome(workspace, homeDir string) ([]string, error) {
	if homeDir == "" {
		return nil, fmt.Errorf("home dir must not be empty")
	}

	dir := UserDir(homeDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("read personas dir %s: %w", dir, err)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".yaml")
		names = append(names, name)
	}

	sort.Strings(names)
	return names, nil
}

// EnsureGeneric creates the generic persona file in the user-level home
// directory (~/.eitri/personas/generic.yaml) if it doesn't already exist.
// The generic persona is the built-in default and is shared across all
// workspaces, so it lives at the user level rather than under any specific
// workspace.
func EnsureGeneric() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve user home dir: %w", err)
	}
	return EnsureGenericWithHome(homeDir)
}

// EnsureGenericWithHome creates the generic persona file under the given
// home directory if it doesn't already exist. Used by EnsureGeneric and by
// tests that need a hermetic home directory.
func EnsureGenericWithHome(homeDir string) error {
	if strings.TrimSpace(homeDir) == "" {
		return fmt.Errorf("home dir must not be empty")
	}

	personaDir := UserDir(homeDir)
	if err := os.MkdirAll(personaDir, 0700); err != nil {
		return fmt.Errorf("create home personas dir: %w", err)
	}

	path := filepath.Join(personaDir, GenericName+".yaml")
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}

	return SaveToHome(homeDir, &PersonaDefinition{
		Name:         GenericName,
		SystemPrompt: DefaultPrompt,
	})
}

// sanitizeName creates a safe filesystem name from a persona name.
func sanitizeName(name string) string {
	// Replace any characters that are problematic in filenames
	name = strings.TrimSpace(name)
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
			return '_'
		}
		return r
	}, name)
	return name
}
