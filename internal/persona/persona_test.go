package persona

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoad(t *testing.T) {
	workspace := t.TempDir()
	def := &PersonaDefinition{
		Name:         "test-persona",
		SystemPrompt: "You are a test agent.",
		InjectedSkills: []string{"skill1", "skill2"},
	}

	if err := Save(workspace, def); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(workspace, "test-persona")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Name != "test-persona" {
		t.Errorf("Name = %q, want %q", loaded.Name, "test-persona")
	}
	if loaded.SystemPrompt != "You are a test agent." {
		t.Errorf("SystemPrompt = %q, want %q", loaded.SystemPrompt, "You are a test agent.")
	}
	if len(loaded.InjectedSkills) != 2 || loaded.InjectedSkills[0] != "skill1" {
		t.Errorf("InjectedSkills = %v, want [skill1 skill2]", loaded.InjectedSkills)
	}
}

func TestLoad_NotFound(t *testing.T) {
	workspace := t.TempDir()
	_, err := Load(workspace, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent persona")
	}
}

func TestSave_EmptyName(t *testing.T) {
	workspace := t.TempDir()
	def := &PersonaDefinition{Name: "", SystemPrompt: "test"}
	if err := Save(workspace, def); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestSave_NilDef(t *testing.T) {
	workspace := t.TempDir()
	if err := Save(workspace, nil); err == nil {
		t.Fatal("expected error for nil definition")
	}
}

func TestDelete(t *testing.T) {
	workspace := t.TempDir()
	def := &PersonaDefinition{
		Name:         "delete-me",
		SystemPrompt: "To be deleted.",
	}

	if err := Save(workspace, def); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := Delete(workspace, "delete-me"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify file is gone
	path := filepath.Join(Dir(workspace), "delete-me.yaml")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists after Delete: %v", err)
	}
}

func TestDelete_NotFound(t *testing.T) {
	workspace := t.TempDir()
	if err := Delete(workspace, "nonexistent"); err == nil {
		t.Fatal("expected error for deleting nonexistent persona")
	}
}

func TestList(t *testing.T) {
	workspace := t.TempDir()

	// No personas dir yet
	names, err := List(workspace)
	if err != nil {
		t.Fatalf("List on empty workspace: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected empty list, got %v", names)
	}

	// Add some personas
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := Save(workspace, &PersonaDefinition{Name: name, SystemPrompt: name}); err != nil {
			t.Fatalf("Save %q: %v", name, err)
		}
	}

	names, err = List(workspace)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	expected := []string{"alpha", "beta", "gamma"}
	if len(names) != len(expected) {
		t.Errorf("got %v, want %v", names, expected)
	}
	for i, n := range names {
		if n != expected[i] {
			t.Errorf("names[%d] = %q, want %q", i, n, expected[i])
		}
	}
}

func TestEnsureGeneric(t *testing.T) {
	workspace := t.TempDir()

	// First call should create generic
	if err := EnsureGeneric(workspace); err != nil {
		t.Fatalf("EnsureGeneric (first): %v", err)
	}

	// Load and verify
	def, err := Load(workspace, GenericName)
	if err != nil {
		t.Fatalf("Load generic: %v", err)
	}
	if def.Name != GenericName {
		t.Errorf("Name = %q, want %q", def.Name, GenericName)
	}
	if def.SystemPrompt != DefaultPrompt {
		t.Errorf("SystemPrompt mismatch")
	}

	// Second call should be idempotent
	if err := EnsureGeneric(workspace); err != nil {
		t.Fatalf("EnsureGeneric (second): %v", err)
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"with spaces", "with spaces"},
		{"with/slashes\\and:colons*and?more", "with_slashes_and_colons_and_more"},
		{"<angle>brackets>", "_angle_brackets_"},
		{"  trimmed  ", "trimmed"},
	}
	for _, tt := range tests {
		result := sanitizeName(tt.input)
		if result != tt.expected {
			t.Errorf("sanitizeName(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestDir(t *testing.T) {
	workspace := "/tmp/test-workspace"
	expected := "/tmp/test-workspace/.eitri/personas"
	if d := Dir(workspace); d != expected {
		t.Errorf("Dir(%q) = %q, want %q", workspace, d, expected)
	}
}
