package persona

import (
	"os"
	"path/filepath"
	"testing"
)

// withTempHome sets HOME to a temp directory for the duration of the test.
// This gives each test its own isolated persona store.
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", home)
	t.Cleanup(func() { os.Setenv("HOME", oldHome) })
	return home
}

func TestSaveAndLoad(t *testing.T) {
	withTempHome(t)
	workspace := t.TempDir()
	def := &PersonaDefinition{
		Name:           "test-persona",
		SystemPrompt:   "You are a test agent.",
		RequiredSkills: []string{"skill1", "skill2"},
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
	if len(loaded.RequiredSkills) != 2 || loaded.RequiredSkills[0] != "skill1" {
		t.Errorf("RequiredSkills = %v, want [skill1 skill2]", loaded.RequiredSkills)
	}
}

func TestLoad_NotFound(t *testing.T) {
	withTempHome(t)
	workspace := t.TempDir()
	_, err := Load(workspace, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent persona")
	}
}

func TestSave_EmptyName(t *testing.T) {
	withTempHome(t)
	workspace := t.TempDir()
	def := &PersonaDefinition{Name: "", SystemPrompt: "test"}
	if err := Save(workspace, def); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestSave_NilDef(t *testing.T) {
	withTempHome(t)
	workspace := t.TempDir()
	if err := Save(workspace, nil); err == nil {
		t.Fatal("expected error for nil definition")
	}
}

func TestDelete(t *testing.T) {
	home := withTempHome(t)
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

	// Verify file is gone from the home-level personas dir
	path := filepath.Join(UserDir(home), "delete-me.yaml")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists after Delete: %v", err)
	}
}

func TestDelete_NotFound(t *testing.T) {
	withTempHome(t)
	workspace := t.TempDir()
	if err := Delete(workspace, "nonexistent"); err == nil {
		t.Fatal("expected error for deleting nonexistent persona")
	}
}

func TestList(t *testing.T) {
	withTempHome(t)
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
	home := t.TempDir()

	// First call should create generic
	if err := EnsureGenericWithHome(home); err != nil {
		t.Fatalf("EnsureGenericWithHome (first): %v", err)
	}

	// Generic must live under the user-level home dir, not under any workspace.
	homePath := filepath.Join(UserDir(home), GenericName+".yaml")
	if _, err := os.Stat(homePath); err != nil {
		t.Fatalf("generic persona not created in home dir %s: %v", homePath, err)
	}

	// Load it back from the home dir.
	def, err := LoadWithHome("", home, GenericName)
	if err != nil {
		t.Fatalf("LoadWithHome generic: %v", err)
	}
	if def.Name != GenericName {
		t.Errorf("Name = %q, want %q", def.Name, GenericName)
	}
	if def.SystemPrompt != DefaultPrompt {
		t.Errorf("SystemPrompt mismatch")
	}

	// Second call should be idempotent
	if err := EnsureGenericWithHome(home); err != nil {
		t.Fatalf("EnsureGenericWithHome (second): %v", err)
	}
}

// TestEnsureGeneric_DoesNotWriteToWorkspace is the regression test for the
// bug where the generic persona was being created in <workspace>/.eitri/personas/.
// It must live in ~/.eitri/personas/ (the user-level home dir) so it is shared
// across all workspaces and not duplicated in each project.
func TestEnsureGeneric_DoesNotWriteToWorkspace(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()

	if err := EnsureGenericWithHome(home); err != nil {
		t.Fatalf("EnsureGenericWithHome: %v", err)
	}

	// The generic persona file must NOT exist in the workspace.
	workspacePath := filepath.Join(workspace, ".eitri", personasDirName, GenericName+".yaml")
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Errorf("generic persona must not be created in workspace at %s, but it exists (err=%v)", workspacePath, err)
	}
}

// TestEnsureGeneric_RejectsEmptyHome guards EnsureGenericWithHome against
// silent fallback to a real user home dir.
func TestEnsureGeneric_RejectsEmptyHome(t *testing.T) {
	if err := EnsureGenericWithHome(""); err == nil {
		t.Fatal("expected error for empty home dir, got nil")
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

func TestUserDir(t *testing.T) {
	home := "/home/test-user"
	expected := "/home/test-user/.eitri/personas"
	if d := UserDir(home); d != expected {
		t.Errorf("UserDir(%q) = %q, want %q", home, d, expected)
	}
}

func TestLoad_FromHomeDir(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()

	// Save persona only in home-level dir
	def := &PersonaDefinition{
		Name:         "home-persona",
		SystemPrompt: "From home dir.",
	}
	if err := SaveToHome(home, def); err != nil {
		t.Fatal(err)
	}

	// Load should find it
	loaded, err := LoadWithHome(workspace, home, "home-persona")
	if err != nil {
		t.Fatalf("LoadWithHome: %v", err)
	}
	if loaded.SystemPrompt != "From home dir." {
		t.Errorf("SystemPrompt = %q, want %q", loaded.SystemPrompt, "From home dir.")
	}
}

func TestDelete_OnlyFromHomeDir(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()

	// Save in home dir
	if err := SaveToHome(home, &PersonaDefinition{Name: "test", SystemPrompt: "Test"}); err != nil {
		t.Fatal(err)
	}

	// Delete — should remove from home
	if err := DeleteWithHome(workspace, home, "test"); err != nil {
		t.Fatalf("DeleteWithHome: %v", err)
	}

	// Verify it's gone
	if _, err := LoadWithHome(workspace, home, "test"); err == nil {
		t.Error("persona still exists after deletion")
	}
}
