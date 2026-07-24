package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

// ————— handleSkills —————

func TestHandleSkills_RendersSkillsPage(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	resp, err := http.Get(server.URL + "/skills")
	if err != nil {
		t.Fatalf("GET /skills failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	content := string(body)
	if !strings.Contains(content, "skills") && !strings.Contains(content, "Skills") {
		t.Errorf("response missing skills page content: %s", content[:min(len(content), 300)])
	}
}

// ————— handleAPISkills —————

func TestHandleAPISkills_ReturnsJSON(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/skills")
	if err != nil {
		t.Fatalf("GET /api/skills failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	if _, ok := result["skills"]; !ok {
		t.Error("response missing 'skills' key")
	}
	if _, ok := result["diagnostics"]; !ok {
		t.Error("response missing 'diagnostics' key")
	}
}

func TestHandleAPISkills_ReturnsHTMXFragment(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/api/skills", nil)
	req.Header.Set("HX-Request", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/skills with HX-Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// The SkillsTable component may not set Content-Type explicitly;
	// just check for expected HTML content.
	body, _ := io.ReadAll(resp.Body)
	content := string(body)
	if !strings.Contains(content, "Effective Skills") && !strings.Contains(content, "skill") {
		t.Errorf("response missing skills table HTML: %s", content[:min(len(content), 200)])
	}
}

func TestHandleAPISkills_FilterByQuery(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})

	// Create a skill directory so there's at least one skill to filter
	skillDir := filepath.Join(rootDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	skillContent := `---
name: test-skill
description: A test skill
---
# Test Skill
This is a test skill.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Refresh the skills service to pick up the new skill
	skillsSvc.Refresh()

	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	// Filter with query that matches
	resp, err := http.Get(server.URL + "/api/skills?q=test")
	if err != nil {
		t.Fatalf("GET /api/skills?q=test failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	skillsList, ok := result["skills"].([]any)
	if !ok {
		t.Fatal("skills is not an array")
	}
	if len(skillsList) == 0 {
		t.Error("expected at least 1 skill matching 'test', got 0")
	}
}

func TestHandleAPISkills_FilterByQueryNoMatch(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/skills?q=nonexistent-skill-name")
	if err != nil {
		t.Fatalf("GET /api/skills?q=nonexistent-skill-name failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	skillsList, ok := result["skills"].([]any)
	if !ok {
		t.Fatal("skills is not an array")
	}
	if len(skillsList) != 0 {
		t.Errorf("expected 0 skills matching nonexistent query, got %d", len(skillsList))
	}
}

// ————— handleSkillsRefresh —————

func TestHandleSkillsRefresh_ReturnsJSON(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	resp, err := http.Post(server.URL+"/api/skills/refresh", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/skills/refresh failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	if _, ok := result["skills"]; !ok {
		t.Error("response missing 'skills' key")
	}
}

func TestHandleSkillsRefresh_ReturnsHTMXFragment(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/skills/refresh", nil)
	req.Header.Set("HX-Request", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/skills/refresh with HX-Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// The SkillsTable component may not set Content-Type explicitly;
	// just check for expected HTML content.
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Error("expected non-empty response body for HTMX request")
	}
}

// ————— handleCompleteSkills —————

func TestHandleCompleteSkills_ReturnsItems(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})

	// Create a skill so there's something to autocomplete
	skillDir := filepath.Join(rootDir, "complete-test")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	skillContent := `---
name: complete-test
description: A test skill for autocomplete
---
# Complete Test
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatal(err)
	}
	skillsSvc.Refresh()

	server := newTestServerWithOptions(t, workspace, testServerOptions{
		sessionManager: sessionMgr,
		skillsService:  skillsSvc,
	})
	defer server.Close()

	browserID := "test-complete-skills"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("GET", server.URL+"/api/sessions/"+sess.ID+"/complete/skills", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/sessions/{id}/complete/skills failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	items, ok := result["items"].([]any)
	if !ok {
		t.Fatal("items is not an array")
	}
	if len(items) == 0 {
		t.Error("expected at least 1 skill item, got 0")
	}
}

func TestHandleCompleteSkills_SessionNotFound(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/sessions/nonexistent/complete/skills")
	if err != nil {
		t.Fatalf("GET /api/sessions/nonexistent/complete/skills failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

func TestHandleCompleteSkills_FilterByQuery(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})

	// Create skills
	for _, name := range []string{"alpha-skill", "beta-skill", "gamma-skill"} {
		skillDir := filepath.Join(rootDir, name)
		if err := os.MkdirAll(skillDir, 0755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: Skill " + name + "\n---\n"
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	skillsSvc.Refresh()

	server := newTestServerWithOptions(t, workspace, testServerOptions{
		sessionManager: sessionMgr,
		skillsService:  skillsSvc,
	})
	defer server.Close()

	browserID := "test-complete-filter"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("GET", server.URL+"/api/sessions/"+sess.ID+"/complete/skills?q=alpha", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/sessions/{id}/complete/skills?q=alpha failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	items, _ := result["items"].([]any)
	if len(items) != 1 {
		t.Errorf("expected 1 item matching 'alpha', got %d", len(items))
	}
}

// ————— handleCompleteFiles —————

func TestHandleCompleteFiles_ReturnsFiles(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	skillsSvc := skills.NewService()

	// Create some files
	os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(workspace, "README.md"), []byte("# Readme"), 0644)
	os.MkdirAll(filepath.Join(workspace, "cmd"), 0755)

	server := newTestServerWithOptions(t, workspace, testServerOptions{
		sessionManager: sessionMgr,
		skillsService:  skillsSvc,
	})
	defer server.Close()

	browserID := "test-complete-files"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("GET", server.URL+"/api/sessions/"+sess.ID+"/complete/files", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/sessions/{id}/complete/files failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	items, ok := result["items"].([]any)
	if !ok {
		t.Fatal("items is not an array")
	}
	if len(items) == 0 {
		t.Error("expected at least 1 file item, got 0")
	}
}

func TestHandleCompleteFiles_SessionNotFound(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/sessions/nonexistent/complete/files")
	if err != nil {
		t.Fatalf("GET /api/sessions/nonexistent/complete/files failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

func TestHandleCompleteFiles_PathTraversalBlocked(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	skillsSvc := skills.NewService()

	server := newTestServerWithOptions(t, workspace, testServerOptions{
		sessionManager: sessionMgr,
		skillsService:  skillsSvc,
	})
	defer server.Close()

	browserID := "test-complete-traversal"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// Test with path traversal
	req, _ := http.NewRequest("GET", server.URL+"/api/sessions/"+sess.ID+"/complete/files?q=../etc", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/sessions/{id}/complete/files?q=../etc failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	items, _ := result["items"].([]any)
	if len(items) != 0 {
		t.Errorf("expected 0 items for path traversal, got %d", len(items))
	}
}

func TestHandleCompleteFiles_AbsolutePathBlocked(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	skillsSvc := skills.NewService()

	server := newTestServerWithOptions(t, workspace, testServerOptions{
		sessionManager: sessionMgr,
		skillsService:  skillsSvc,
	})
	defer server.Close()

	browserID := "test-complete-absolute"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("GET", server.URL+"/api/sessions/"+sess.ID+"/complete/files?q=/etc", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/sessions/{id}/complete/files?q=/etc failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	items, _ := result["items"].([]any)
	if len(items) != 0 {
		t.Errorf("expected 0 items for absolute path, got %d", len(items))
	}
}

func TestHandleCompleteFiles_PrefixFilter(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	skillsSvc := skills.NewService()

	os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(workspace, "util.go"), []byte("package util"), 0644)
	os.WriteFile(filepath.Join(workspace, "README.md"), []byte("# Readme"), 0644)

	server := newTestServerWithOptions(t, workspace, testServerOptions{
		sessionManager: sessionMgr,
		skillsService:  skillsSvc,
	})
	defer server.Close()

	browserID := "test-complete-prefix"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("GET", server.URL+"/api/sessions/"+sess.ID+"/complete/files?q=main", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/sessions/{id}/complete/files?q=main failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	items, _ := result["items"].([]any)
	if len(items) != 1 {
		t.Errorf("expected 1 item matching 'main', got %d", len(items))
	}
}

// ————— handleActivateSessionSkill —————

func TestHandleActivateSessionSkill_SkillNotFound(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	skillsSvc := skills.NewService()

	server := newTestServerWithOptions(t, workspace, testServerOptions{
		sessionManager: sessionMgr,
		skillsService:  skillsSvc,
	})
	defer server.Close()

	browserID := "test-activate-notfound"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/skills/nonexistent/activate", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/skills/{name}/activate failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected status 422, got %d", resp.StatusCode)
	}
}

func TestHandleActivateSessionSkill_SessionNotFound(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/nonexistent/skills/test/activate", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: "test-browser"})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/skills/{name}/activate failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

func TestHandleActivateSessionSkill_ValidSkill(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})

	// Create a valid skill
	skillDir := filepath.Join(rootDir, "activate-me")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := `---
name: activate-me
description: A skill to activate
---
# Activate Me
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	skillsSvc.Refresh()

	server := newTestServerWithOptions(t, workspace, testServerOptions{
		sessionManager: sessionMgr,
		skillsService:  skillsSvc,
	})
	defer server.Close()

	browserID := "test-activate-valid"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/skills/activate-me/activate", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/skills/activate-me/activate failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Check skill was activated in session
	sessAfter := sessionMgr.Get(sess.ID)
	if sessAfter == nil {
		t.Fatal("session disappeared")
	}
	found := false
	for _, s := range sessAfter.ActiveSkills {
		if s == "activate-me" {
			found = true
			break
		}
	}
	if !found {
		t.Error("skill 'activate-me' was not activated in session")
	}
}

// ————— handleSessionSkillChips —————

func TestHandleSessionSkillChips_ReturnsHTML(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	skillsSvc := skills.NewService()

	server := newTestServerWithOptions(t, workspace, testServerOptions{
		sessionManager: sessionMgr,
		skillsService:  skillsSvc,
	})
	defer server.Close()

	browserID := "test-chips"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("GET", server.URL+"/api/sessions/"+sess.ID+"/skills/chips", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/sessions/{id}/skills/chips failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestHandleSessionSkillChips_SessionNotFound(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/api/sessions/nonexistent/skills/chips", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: "test-browser"})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/sessions/nonexistent/skills/chips failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

// ————— handleDisableSkill —————

func TestHandleDisableSkill_DisablesSkill(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()

	// Create a skill
	skillDir := filepath.Join(workspace, ".eitri", "skills", "disable-me")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := `---
name: disable-me
description: A skill to disable
---
# Disable Me
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	skillsSvc.Refresh()

	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/skills/disable-me/disable", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/skills/disable-me/disable failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", result["status"])
	}
}

func TestHandleDisableSkill_ReturnsHTMXFragment(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/skills/any-skill/disable", nil)
	req.Header.Set("HX-Request", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/skills/any-skill/disable with HX-Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Content-Type may not be set explicitly by templ
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Error("expected non-empty body for HTMX request")
	}
}

// ————— handleEnableSkill —————

func TestHandleEnableSkill_EnablesSkill(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/skills/any-skill/enable", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/skills/any-skill/enable failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", result["status"])
	}
}

func TestHandleEnableSkill_ReturnsHTMXFragment(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/skills/any-skill/enable", nil)
	req.Header.Set("HX-Request", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/skills/any-skill/enable with HX-Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Content-Type may not be set explicitly by templ
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Error("expected non-empty body for HTMX request")
	}
}

// ————— handleDisableAllSkills —————

func TestHandleDisableAllSkills_DisablesAll(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/skills/disable-all", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/skills/disable-all failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", result["status"])
	}
}

func TestHandleDisableAllSkills_ReturnsHTMXFragment(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/skills/disable-all", nil)
	req.Header.Set("HX-Request", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/skills/disable-all with HX-Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Content-Type may not be set explicitly by templ
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Error("expected non-empty body for HTMX request")
	}
}

// ————— handleEnableAllSkills —————

func TestHandleEnableAllSkills_EnablesAll(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/skills/enable-all", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/skills/enable-all failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", result["status"])
	}
}

func TestHandleEnableAllSkills_ReturnsHTMXFragment(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/skills/enable-all", nil)
	req.Header.Set("HX-Request", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/skills/enable-all with HX-Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Content-Type may not be set explicitly by templ
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Error("expected non-empty body for HTMX request")
	}
}
func TestDebugHTMX(t *testing.T) {
	workspace := t.TempDir()
	skillsSvc := skills.NewService()
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	defer server.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/skills/refresh", nil)
	req.Header.Set("HX-Request", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Content-Type may not be set explicitly by templ
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Error("expected non-empty body for HTMX request")
	}
}
