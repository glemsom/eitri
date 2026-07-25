// Package persona manages agent personas — named profiles with a system prompt
// and optional injected skills. Personas are stored as YAML files under
// <workspace>/.eitri/personas/<name>.yaml.
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

## Tool usage
- When you need to ask the user a multi-choice question (e.g. which option to proceed with, which fix to apply), use render_quick_replies with the options array instead of asking in plain text — it renders clickable buttons the user can tap.`

	// MaxCustomPersonas is the maximum number of custom personas allowed (not counting generic).
	MaxCustomPersonas = 10

	// GenericName is the name of the built-in generic persona.
	GenericName = "generic"

	// personasDirName is the subdirectory under .eitri where persona files are stored.
	personasDirName = "personas"
)

// PersonaDefinition represents a single persona.
type PersonaDefinition struct {
	Name           string   `yaml:"name" json:"name"`
	SystemPrompt   string   `yaml:"system_prompt" json:"system_prompt"`
	InjectedSkills []string `yaml:"injected_skills,omitempty" json:"injected_skills,omitempty"`
}

// Dir returns the personas directory for the given workspace.
func Dir(workspace string) string {
	return filepath.Join(workspace, ".eitri", personasDirName)
}

// Save writes a persona definition to disk as <name>.yaml.
// It creates the personas directory if it doesn't exist.
func Save(workspace string, def *PersonaDefinition) error {
	if def == nil {
		return fmt.Errorf("persona definition is nil")
	}
	if strings.TrimSpace(def.Name) == "" {
		return fmt.Errorf("persona name must not be empty")
	}

	personaDir := Dir(workspace)
	if err := os.MkdirAll(personaDir, 0700); err != nil {
		return fmt.Errorf("create personas dir: %w", err)
	}

	data, err := yaml.Marshal(def)
	if err != nil {
		return fmt.Errorf("marshal persona: %w", err)
	}

	path := filepath.Join(personaDir, sanitizeName(def.Name)+".yaml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write persona file: %w", err)
	}
	return nil
}

// Load reads a persona definition from disk.
func Load(workspace, name string) (*PersonaDefinition, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("persona name must not be empty")
	}

	path := filepath.Join(Dir(workspace), sanitizeName(name)+".yaml")
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

// Delete removes a persona file from disk. It does not prevent deleting generic.
func Delete(workspace, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("persona name must not be empty")
	}

	path := filepath.Join(Dir(workspace), sanitizeName(name)+".yaml")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("persona %q not found", name)
		}
		return fmt.Errorf("delete persona file: %w", err)
	}
	return nil
}

// List enumerates persona YAML files in the personas directory and returns their names.
func List(workspace string) ([]string, error) {
	personaDir := Dir(workspace)
	entries, err := os.ReadDir(personaDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read personas dir: %w", err)
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

// EnsureGeneric creates the generic persona file if it doesn't exist.
func EnsureGeneric(workspace string) error {
	personaDir := Dir(workspace)
	if err := os.MkdirAll(personaDir, 0700); err != nil {
		return fmt.Errorf("create personas dir: %w", err)
	}

	path := filepath.Join(personaDir, GenericName+".yaml")
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}

	def := &PersonaDefinition{
		Name:         GenericName,
		SystemPrompt: DefaultPrompt,
	}
	return Save(workspace, def)
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
