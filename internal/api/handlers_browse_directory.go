package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/glemsom/eitri/internal/fileutil"
)

// BrowseDirectoryResponse is the JSON response for the browse-directory endpoint.
type BrowseDirectoryResponse struct {
	CurrentPath string           `json:"current_path"`
	ParentPath  string           `json:"parent_path"`
	Breadcrumbs []Breadcrumb     `json:"breadcrumbs"`
	Directories []DirectoryEntry `json:"directories"`
}

// Breadcrumb represents a single segment in the breadcrumb navigation.
type Breadcrumb struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// DirectoryEntry represents a subdirectory entry in the listing.
type DirectoryEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// handleBrowseDirectory handles GET /api/browse-directory?path=...
// It lists subdirectories at the given path, returning JSON.
func (s *Server) handleBrowseDirectory(w http.ResponseWriter, r *http.Request) {
	pathParam := r.URL.Query().Get("path")

	// Default to $HOME if no path provided
	if pathParam == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			writeBrowseError(w, "cannot determine home directory", http.StatusInternalServerError)
			return
		}
		pathParam = home
	}

	// Validate the path
	cleanedPath, err := fileutil.ValidateBrowsePath(pathParam)
	if err != nil {
		writeBrowseError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Read directory entries
	entries, err := os.ReadDir(cleanedPath)
	if err != nil {
		writeBrowseError(w, "cannot read directory: permission denied", http.StatusForbidden)
		return
	}

	// Build parent path
	parentPath := ""
	if cleanedPath != string(filepath.Separator) {
		parent := filepath.Dir(cleanedPath)
		// Only set parent if it's a real directory (skip for virtual roots)
		if info, err := os.Stat(parent); err == nil && info.IsDir() {
			parentPath = parent
		}
	}

	// Build breadcrumbs
	breadcrumbs := buildBreadcrumbs(cleanedPath)

	// Collect only non-hidden directories, sorted
	var dirs []DirectoryEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip hidden directories (dot-prefixed)
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

	resp := BrowseDirectoryResponse{
		CurrentPath: cleanedPath,
		ParentPath:  parentPath,
		Breadcrumbs: breadcrumbs,
		Directories: dirs,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// buildBreadcrumbs splits an absolute path into breadcrumb segments.
func buildBreadcrumbs(absPath string) []Breadcrumb {
	if absPath == "" {
		return nil
	}

	// Normalize to clean path
	absPath = filepath.Clean(absPath)

	// For root, return a single breadcrumb
	if absPath == string(filepath.Separator) {
		return []Breadcrumb{{Label: "/", Path: "/"}}
	}

	// Split path into segments
	var crumbs []Breadcrumb

	// Handle Windows volume names or Unix root
	if filepath.VolumeName(absPath) != "" {
		vol := filepath.VolumeName(absPath)
		crumbs = append(crumbs, Breadcrumb{Label: vol, Path: vol + string(filepath.Separator)})
		absPath = absPath[len(vol)+1:]
	} else {
		crumbs = append(crumbs, Breadcrumb{Label: "/", Path: "/"})
	}

	parts := strings.Split(absPath, string(filepath.Separator))
	accum := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		accum = filepath.Join(accum, part)
		fullPath := string(filepath.Separator) + accum
		crumbs = append(crumbs, Breadcrumb{
			Label: part,
			Path:  fullPath,
		})
	}

	return crumbs
}

// writeBrowseError writes a JSON error response for the browse-directory endpoint.
func writeBrowseError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
