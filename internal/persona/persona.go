// Package persona manages agent personas — named profiles with a system prompt
// and optional injected skills.
//
// Personas are stored as YAML files under either:
//   - <workspace>/.eitri/personas/<name>.yaml (project-scoped, higher precedence)
//   - ~/.eitri/personas/<name>.yaml        (user-level, fallback)
//
// This mirrors the skills discovery pattern (ADR-0002): workspace overrides home.
// Save always writes to the workspace directory; Load and List check both locations.
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
	InjectedSkills []string `yaml:"injected_skills,omitempty" json:"injected_skills,omitempty"`
}

// Dir returns the personas directory for the given workspace.
func Dir(workspace string) string {
	return filepath.Join(workspace, ".eitri", personasDirName)
}

// UserDir returns the user-level personas directory under the home .eitri.
func UserDir(homeDir string) string {
	return filepath.Join(homeDir, ".eitri", personasDirName)
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
// It first checks the workspace-scoped directory, then falls back to the user-level
// home directory (determined via os.UserHomeDir).
// The workspace parameter must be non-empty.
func Load(workspace, name string) (*PersonaDefinition, error) {
	homeDir, _ := os.UserHomeDir()
	return LoadWithHome(workspace, homeDir, name)
}

// LoadWithHome reads a persona definition, checking workspace-scoped first,
// then falling back to the user-level home directory.
func LoadWithHome(workspace, homeDir, name string) (*PersonaDefinition, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("persona name must not be empty")
	}

	// Try workspace first (project-level override)
	workspacePath := filepath.Join(Dir(workspace), sanitizeName(name)+".yaml")
	data, err := os.ReadFile(workspacePath)
	if err == nil {
		var def PersonaDefinition
		if err := yaml.Unmarshal(data, &def); err != nil {
			return nil, fmt.Errorf("unmarshal persona: %w", err)
		}
		return &def, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read persona file: %w", err)
	}

	// Fall back to user-level home directory
	if homeDir != "" {
		homePath := filepath.Join(UserDir(homeDir), sanitizeName(name)+".yaml")
		data, err = os.ReadFile(homePath)
		if err == nil {
			var def PersonaDefinition
			if err := yaml.Unmarshal(data, &def); err != nil {
				return nil, fmt.Errorf("unmarshal persona: %w", err)
			}
			return &def, nil
		}
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read persona file: %w", err)
		}
	}

	return nil, fmt.Errorf("persona %q not found", name)
}

// Delete removes a persona file from disk. It checks both workspace-scoped
// and user-level home directories. It does not prevent deleting generic.
func Delete(workspace, name string) error {
	homeDir, _ := os.UserHomeDir()
	return DeleteWithHome(workspace, homeDir, name)
}

// DeleteWithHome removes a persona file, checking the workspace directory first,
// then the user-level home directory.
func DeleteWithHome(workspace, homeDir, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("persona name must not be empty")
	}

	// Try workspace first
	workspacePath := filepath.Join(Dir(workspace), sanitizeName(name)+".yaml")
	if err := os.Remove(workspacePath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("delete persona file: %w", err)
	}

	// Try user-level home directory
	if homeDir != "" {
		homePath := filepath.Join(UserDir(homeDir), sanitizeName(name)+".yaml")
		if err := os.Remove(homePath); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("delete persona file: %w", err)
		}
	}

	return fmt.Errorf("persona %q not found", name)
}

// List returns persona names from the workspace-scoped directory, with a
// fallback to the user-level home directory. Workspace names override
// home-level names with the same name (de-duplicated, workspace wins).
func List(workspace string) ([]string, error) {
	homeDir, _ := os.UserHomeDir()
	return ListWithHome(workspace, homeDir)
}

// ListWithHome enumerates personas from both workspace-scoped and user-level
// directories. Workspace names take precedence over home names.
func ListWithHome(workspace, homeDir string) ([]string, error) {
	seen := make(map[string]bool)
	var names []string

	addDir := func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("read personas dir %s: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if !strings.HasSuffix(entry.Name(), ".yaml") {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), ".yaml")
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
		return nil
	}

	// Workspace first (higher precedence)
	if err := addDir(Dir(workspace)); err != nil {
		return nil, err
	}

	// Then user-level home
	if homeDir != "" {
		if err := addDir(UserDir(homeDir)); err != nil {
			return nil, err
		}
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
