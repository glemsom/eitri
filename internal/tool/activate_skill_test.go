package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

func TestActivateSkill_Schema(t *testing.T) {
	tool := NewActivateSkill(nil, nil)
	if tool.Name() != "activate_skill" {
		t.Errorf("Name = %q, want 'activate_skill'", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description should not be empty")
	}
	schema := tool.JSONSchema()
	if schema == nil {
		t.Fatal("JSONSchema is nil")
	}
	if !json.Valid(schema) {
		t.Error("JSONSchema is not valid JSON")
	}
}

func TestActivateSkill_InvalidArgs(t *testing.T) {
	tool := NewActivateSkill(nil, nil)
	_, err, _ := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestActivateSkill_EmptyName(t *testing.T) {
	tool := NewActivateSkill(nil, nil)
	_, err, isError := tool.Call(context.Background(), json.RawMessage(`{"name":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
}

func TestActivateSkill_NilSkillsService(t *testing.T) {
	tool := NewActivateSkill(nil, nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"name":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
	if len(blocks) > 0 {
		result, ok := blocks[0].(litellm.TextBlock)
		if ok && result.Text == "" {
			t.Error("expected error text")
		}
	}
}

func writeSkillFile(t *testing.T, dir, name string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", skillDir, err)
	}
	content := "---\nname: " + name + "\ndescription: Test skill " + name + "\n---\n# " + name + "\n\nInstructions for " + name + "."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

func TestActivateSkill_ValidSkill(t *testing.T) {
	rootDir := t.TempDir()
	skillName := "test-skill"
	writeSkillFile(t, rootDir, skillName)

	svc := skills.NewServiceWithRoots([]skills.Root{
		{Path: rootDir, Scope: skills.ScopeProjectEitri},
	})
	svc.Refresh()

	uiMgr := session.NewManager(10)

	tool := NewActivateSkill(svc, uiMgr)

	// Call without session ID in context (no activation recorded)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"name":"test-skill"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	textBlock, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	if len(textBlock.Text) == 0 {
		t.Error("expected skill content")
	}
}

func TestActivateSkill_WithSessionContext(t *testing.T) {
	rootDir := t.TempDir()
	skillName := "test-skill-2"
	writeSkillFile(t, rootDir, skillName)

	svc := skills.NewServiceWithRoots([]skills.Root{
		{Path: rootDir, Scope: skills.ScopeProjectEitri},
	})
	svc.Refresh()

	uiMgr := session.NewManager(10)
	sess, err := uiMgr.Create("browser1")
	if err != nil {
		t.Fatal(err)
	}

	tool := NewActivateSkill(svc, uiMgr)

	// Call with session ID in context
	sessCtx := context.WithValue(context.Background(), sessionIDKey, sess.ID)
	blocks, err, isError := tool.Call(sessCtx, json.RawMessage(`{"name":"test-skill-2"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}

	// Verify skill was recorded
	activeSkills := uiMgr.ActiveSkills(sess.ID)
	if len(activeSkills) != 1 || activeSkills[0] != "test-skill-2" {
		t.Errorf("active skills = %v, want ['test-skill-2']", activeSkills)
	}
}

func TestActivateSkill_UnknownSkill(t *testing.T) {
	svc := skills.NewService()
	tool := NewActivateSkill(svc, nil)

	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"name":"unknown-skill"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
	if len(blocks) > 0 {
		result, ok := blocks[0].(litellm.TextBlock)
		if ok && result.Text == "" {
			t.Error("expected error text")
		}
	}
}

func TestActivateSkill_ArgsUnmarshal(t *testing.T) {
	args := json.RawMessage(`{"name":"code-review"}`)
	var parsed activateSkillArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Name != "code-review" {
		t.Errorf("Name = %q, want 'code-review'", parsed.Name)
	}
}
