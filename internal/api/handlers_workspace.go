package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/fileutil"
)

// handleSessionDirectoryBrowser renders the directory browser overlay for a session.
// GET /api/sessions/{id}/directory-browser?path=...
// If no path is provided, starts at the session's current workspace.
// Returns an HTML fragment for the HTMX overlay.
func (s *Server) handleSessionDirectoryBrowser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || (browserID != "" && sess.BrowserID != browserID) {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	pathParam := r.URL.Query().Get("path")
	if pathParam == "" {
		pathParam = sess.Workspace
	}

	// Validate and list directories
	resp, err := browseDirectory(pathParam)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Convert to template data
	data := templates.DirectoryBrowserData{
		SessionID:   id,
		CurrentPath: resp.CurrentPath,
		ParentPath:  resp.ParentPath,
		Breadcrumbs: convertBreadcrumbs(resp.Breadcrumbs),
		Directories: convertDirectoryEntries(resp.Directories),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	component := templates.DirectoryBrowser(data)
	component.Render(r.Context(), w)
}

// handleUpdateWorkspace updates the workspace directory for a session.
// POST /api/sessions/{id}/workspace
// Body (HTMX hx-vals, URL-encoded): path=/absolute/path/to/workspace
// Also accepts JSON body for API clients.
func (s *Server) handleUpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || (browserID != "" && sess.BrowserID != browserID) {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Accept both URL-encoded form data (from HTMX hx-vals) and JSON body.
	path := r.FormValue("path")
	if path == "" {
		// Fall back to JSON body for API clients
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		path = req.Path
	}

	if path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	// Validate the path
	cleanedPath, err := fileutil.ValidateBrowsePath(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Update session workspace
	s.config.SessionManager.SetWorkspace(id, cleanedPath)

	// Redirect back to the session page to re-render with updated workspace
	// Use HX-Redirect for HTMX requests
	s.hxRedirect(w, r, "/sessions/"+id)
}

// browseDirectory is a helper that calls the browse-directory logic and returns structured data.
func browseDirectory(pathParam string) (*BrowseDirectoryResponse, error) {
	if pathParam == "" {
		pathParam = "/"
	}

	cleanedPath, err := fileutil.ValidateBrowsePath(pathParam)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(cleanedPath)
	if err != nil {
		return nil, err
	}

	parentPath := ""
	if cleanedPath != string(filepath.Separator) {
		parent := filepath.Dir(cleanedPath)
		if info, err := os.Stat(parent); err == nil && info.IsDir() {
			parentPath = parent
		}
	}

	breadcrumbs := buildBreadcrumbs(cleanedPath)

	var dirs []DirectoryEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		dirs = append(dirs, DirectoryEntry{
			Name: name,
			Path: filepath.Join(cleanedPath, name),
		})
	}
	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].Name < dirs[j].Name
	})

	return &BrowseDirectoryResponse{
		CurrentPath: cleanedPath,
		ParentPath:  parentPath,
		Breadcrumbs: breadcrumbs,
		Directories: dirs,
	}, nil
}

// convertBreadcrumbs converts API breadcrumbs to template breadcrumbs.
func convertBreadcrumbs(crumbs []Breadcrumb) []templates.BreadcrumbData {
	result := make([]templates.BreadcrumbData, len(crumbs))
	for i, c := range crumbs {
		result[i] = templates.BreadcrumbData{Label: c.Label, Path: c.Path}
	}
	return result
}

// convertDirectoryEntries converts API directory entries to template entries.
func convertDirectoryEntries(entries []DirectoryEntry) []templates.DirectoryEntryData {
	result := make([]templates.DirectoryEntryData, len(entries))
	for i, e := range entries {
		result[i] = templates.DirectoryEntryData{Name: e.Name, Path: e.Path}
	}
	return result
}
