package persona

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
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

func TestUserDir(t *testing.T) {
	home := "/home/test-user"
	expected := "/home/test-user/.eitri/personas"
	if d := UserDir(home); d != expected {
		t.Errorf("UserDir(%q) = %q, want %q", home, d, expected)
	}
}

func TestLoad_FallsBackToHomeDir(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()

	// Save persona only in home-level dir
	def := &PersonaDefinition{
		Name:         "home-persona",
		SystemPrompt: "From home dir.",
	}
	if err := os.MkdirAll(UserDir(home), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := yaml.Marshal(def)
	if err := os.WriteFile(filepath.Join(UserDir(home), "home-persona.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}

	// Load should find it via home fallback
	loaded, err := LoadWithHome(workspace, home, "home-persona")
	if err != nil {
		t.Fatalf("LoadWithHome: %v", err)
	}
	if loaded.SystemPrompt != "From home dir." {
		t.Errorf("SystemPrompt = %q, want %q", loaded.SystemPrompt, "From home dir.")
	}
}

func TestLoad_WorkspaceOverridesHome(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()

	// Save same name in both dirs with different content
	workspaceDef := &PersonaDefinition{Name: "shared", SystemPrompt: "Workspace version."}
	if err := Save(workspace, workspaceDef); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(UserDir(home), 0700); err != nil {
		t.Fatal(err)
	}
	homeDef := &PersonaDefinition{Name: "shared", SystemPrompt: "Home version (should be shadowed)."}
	data, _ := yaml.Marshal(homeDef)
	if err := os.WriteFile(filepath.Join(UserDir(home), "shared.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}

	// Load should return workspace version
	loaded, err := LoadWithHome(workspace, home, "shared")
	if err != nil {
		t.Fatalf("LoadWithHome: %v", err)
	}
	if loaded.SystemPrompt != "Workspace version." {
		t.Errorf("expected workspace override, got %q", loaded.SystemPrompt)
	}
}

func TestList_MergesWorkspaceAndHome(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()

	// Create a persona in workspace only
	if err := Save(workspace, &PersonaDefinition{Name: "project-only", SystemPrompt: "Project"}); err != nil {
		t.Fatal(err)
	}

	// Create a persona in home only
	if err := os.MkdirAll(UserDir(home), 0700); err != nil {
		t.Fatal(err)
	}
	homeDef := &PersonaDefinition{Name: "user-only", SystemPrompt: "User"}
	data, _ := yaml.Marshal(homeDef)
	if err := os.WriteFile(filepath.Join(UserDir(home), "user-only.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}

	// Create a persona in both with same name — only workspace should appear
	workspaceDef := &PersonaDefinition{Name: "shared", SystemPrompt: "Project shared"}
	if err := Save(workspace, workspaceDef); err != nil {
		t.Fatal(err)
	}
	homeShared := &PersonaDefinition{Name: "shared", SystemPrompt: "Home shared"}
	homeData, _ := yaml.Marshal(homeShared)
	if err := os.WriteFile(filepath.Join(UserDir(home), "shared.yaml"), homeData, 0600); err != nil {
		t.Fatal(err)
	}

	names, err := ListWithHome(workspace, home)
	if err != nil {
		t.Fatalf("ListWithHome: %v", err)
	}

	// Should have 3 names: project-only, shared, user-only (sorted)
	expected := []string{"project-only", "shared", "user-only"}
	if len(names) != len(expected) {
		t.Fatalf("got %v, want %v", names, expected)
	}
	for i, n := range names {
		if n != expected[i] {
			t.Errorf("names[%d] = %q, want %q", i, n, expected[i])
		}
	}

	// Verify workspace version wins for shared
	loaded, err := LoadWithHome(workspace, home, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SystemPrompt != "Project shared" {
		t.Errorf("expected workspace override for shared, got %q", loaded.SystemPrompt)
	}
}

func TestDelete_DeletesFromWorkspaceFirst(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()

	// Save in workspace
	if err := Save(workspace, &PersonaDefinition{Name: "test", SystemPrompt: "Test"}); err != nil {
		t.Fatal(err)
	}

	// Also save in home (shouldn't matter, workspace wins)
	if err := os.MkdirAll(UserDir(home), 0700); err != nil {
		t.Fatal(err)
	}
	homeDef := &PersonaDefinition{Name: "test", SystemPrompt: "Test home"}
	data, _ := yaml.Marshal(homeDef)
	if err := os.WriteFile(filepath.Join(UserDir(home), "test.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}

	// Delete — should remove workspace one
	if err := DeleteWithHome(workspace, home, "test"); err != nil {
		t.Fatalf("DeleteWithHome: %v", err)
	}

	// Verify workspace one is gone
	if _, err := Load(workspace, "test"); err == nil {
		t.Error("persona still exists after deletion")
	}

	// Home one should still exist
	loaded, err := LoadWithHome(workspace, home, "test")
	if err != nil {
		t.Fatalf("home persona should still exist: %v", err)
	}
	if loaded.SystemPrompt != "Test home" {
		t.Errorf("expected home version, got %q", loaded.SystemPrompt)
	}
}
