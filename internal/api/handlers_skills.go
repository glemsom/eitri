package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/skills"
)

func (s *Server) refreshSkillsRegistry() *skills.Registry {
	if s.config.SkillsService == nil {
		return skills.NewRegistry()
	}
	return s.config.SkillsService.Refresh()
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	registry := s.refreshSkillsRegistry()
	contextWindow := 256000
	if s.config.RunService != nil {
		contextWindow = s.config.RunService.ContextWindowTokens()
	}
	component := templates.SkillsPage(registry, s.config.Workspace, s.chatPathForRequest(r), r.URL.Path, contextWindow)
	component.Render(r.Context(), w)
}

func (s *Server) handleAPISkills(w http.ResponseWriter, r *http.Request) {
	browserID := s.browserIDFromRequest(r)
	_ = browserID

	registry := s.refreshSkillsRegistry()

	// HTMX-aware: return HTML fragment when HX-Request header is present
	if r.Header.Get("HX-Request") == "true" {
		component := templates.SkillsTable(registry)
		component.Render(r.Context(), w)
		return
	}

	// Otherwise return JSON
	effective := registry.Effective()
	type skillJSON struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Scope       string `json:"scope"`
		Path        string `json:"path"`
	}
	result := make([]skillJSON, 0, len(effective))
	for _, s := range effective {
		result = append(result, skillJSON{
			Name:        s.Name,
			Description: s.Description,
			Scope:       string(s.Scope),
			Path:        s.Path,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"skills":      result,
		"diagnostics": registry.Diagnostics(),
	})
}

func (s *Server) handleSkillsRefresh(w http.ResponseWriter, r *http.Request) {
	registry := s.refreshSkillsRegistry()

	if r.Header.Get("HX-Request") == "true" {
		component := templates.SkillsTable(registry)
		component.Render(r.Context(), w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"skills": registry.Summary(),
	})
}

func (s *Server) handleCompleteSkills(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	q := r.URL.Query().Get("q")
	registry := s.refreshSkillsRegistry()
	var effective map[string]*skills.Skill
	if registry != nil {
		effective = registry.Effective()
	}

	type itemJSON struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Scope       string `json:"scope"`
	}
	var items []itemJSON
	for _, skill := range effective {
		if q == "" || strings.HasPrefix(skill.Name, q) {
			items = append(items, itemJSON{
				Name:        skill.Name,
				Description: skill.Description,
				Scope:       string(skill.Scope),
			})
		}
		if len(items) >= 50 {
			break
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"items": items})
}

func (s *Server) handleCompleteFiles(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	q := r.URL.Query().Get("q")

	// Reject path-traversal and absolute paths for safety
	if strings.HasPrefix(q, "..") || strings.Contains(q, "/..") || strings.HasPrefix(q, "/") {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
		return
	}

	workspace := s.config.Workspace
	searchDir := workspace
	prefix := q
	displayPrefix := ""

	// Handle subdirectory prefix: split into dir + file prefix
	if q != "" {
		if strings.HasSuffix(q, "/") {
			searchDir = filepath.Join(workspace, q)
			prefix = ""
			displayPrefix = q
		} else {
			dir := filepath.Dir(q)
			if dir != "." {
				searchDir = filepath.Join(workspace, dir)
				prefix = filepath.Base(q)
				displayPrefix = dir + "/"
			} else {
				prefix = q
			}
		}
	}

	// Skip if searchDir doesn't exist or is outside workspace
	absSearch, err := filepath.Abs(searchDir)
	if err != nil || !strings.HasPrefix(absSearch, workspace) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
		return
	}

	entries, err := os.ReadDir(absSearch)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
		return
	}

	type fileItem struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	}
	var items []fileItem

	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		"dist": true, "build": true, "target": true, ".cache": true,
	}

	for _, entry := range entries {
		name := entry.Name()

		// Skip hidden unless prefix starts with dot
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}

		// Filter by prefix
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}

		itemPath := displayPrefix + name
		if entry.IsDir() {
			if skipDirs[name] {
				continue
			}
			items = append(items, fileItem{Path: itemPath + "/", Kind: "dir"})
		} else {
			items = append(items, fileItem{Path: itemPath, Kind: "file"})
		}

		if len(items) >= 50 {
			break
		}
	}

	// Sort: dirs first, then lexicographic within each group
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind == "dir"
		}
		return items[i].Path < items[j].Path
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"items": items})
}

func (s *Server) handleActivateSessionSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.PathValue("name")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	_ = s.refreshSkillsRegistry()

	// Validate skill exists and is effective
	if s.config.SkillsService.Lookup(name) == nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Header().Set("Content-Type", "text/html")
		component := templates.ErrorToast("Skill \"" + name + "\" not found or not effective")
		component.Render(r.Context(), w)
		return
	}

	// Activate in session
	activated := s.config.SessionManager.ActivateSkill(id, name)

	// Return HTMX fragment with updated chips
	if r.Header.Get("HX-Request") == "true" {
		if activated {
			// Swap the active skills section
			chips := templates.ActiveSkillChips(sess.ActiveSkills)
			chips.Render(r.Context(), w)
			return
		}
		// Already active - return current state
		chips := templates.ActiveSkillChips(sess.ActiveSkills)
		chips.Render(r.Context(), w)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleDisableSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if s.config.SkillsService != nil {
		s.config.SkillsService.SetDisabled(name, true, func(disabled []string) {
			cfg, err := config.Load(s.config.ConfigPath)
			if err != nil {
				s.logger.Error("failed to load config for skill disable", "error", err)
				return
			}
			cfg.DisabledSkills = disabled
			if err := config.Save(s.config.ConfigPath, cfg); err != nil {
				s.logger.Error("failed to save config for skill disable", "error", err)
			}
		})
	}

	registry := s.refreshSkillsRegistry()

	// Auto-deactivate in all sessions
	deactivatedCount := 0
	for _, sess := range s.config.SessionManager.All() {
		for _, activeSkill := range sess.ActiveSkills {
			if activeSkill == name {
				s.config.SessionManager.DeactivateSkill(sess.ID, name)
				deactivatedCount++
				break
			}
		}
	}

	if deactivatedCount > 0 {
		registry.AppendDiagnostic(skills.Diagnostic{
			Severity: skills.SeverityWarn,
			Message:  fmt.Sprintf("Skill %q disabled and deactivated from %d session(s)", name, deactivatedCount),
			Skill:    name,
		})
	}

	if r.Header.Get("HX-Request") == "true" {
		component := templates.SkillsTable(registry)
		component.Render(r.Context(), w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleEnableSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if s.config.SkillsService != nil {
		s.config.SkillsService.SetDisabled(name, false, func(disabled []string) {
			cfg, err := config.Load(s.config.ConfigPath)
			if err != nil {
				s.logger.Error("failed to load config for skill enable", "error", err)
				return
			}
			cfg.DisabledSkills = disabled
			if err := config.Save(s.config.ConfigPath, cfg); err != nil {
				s.logger.Error("failed to save config for skill enable", "error", err)
			}
		})
	}

	registry := s.refreshSkillsRegistry()

	if r.Header.Get("HX-Request") == "true" {
		component := templates.SkillsTable(registry)
		component.Render(r.Context(), w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
