package runner

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/glemsom/eitri/internal/session"
)

// markdownLinkRe matches Markdown links: [text](path)
var markdownLinkRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)

// ScanContextFiles scans the workspace for additional context files loaded into
// the agent prompt. It checks for AGENTS.md and, if present, parses it for
// Markdown links pointing to local files (which are also included as context).
//
// Returns a slice of ContextFile sorted by depth then path, or nil if none found.
func ScanContextFiles(workspace string) []session.ContextFile {
	agentsPath := filepath.Join(workspace, "AGENTS.md")
	if _, err := os.Stat(agentsPath); os.IsNotExist(err) {
		return nil
	}

	files := []session.ContextFile{
		{Path: "AGENTS.md", Depth: 0},
	}

	referenced := scanReferencedFiles(agentsPath, workspace)
	files = append(files, referenced...)

	return files
}

// scanReferencedFiles parses a Markdown file for links to local files
// and returns them as context files with Depth=1.
func scanReferencedFiles(mdPath, workspace string) []session.ContextFile {
	data, err := os.ReadFile(mdPath)
	if err != nil {
		slog.Debug("cannot read AGENTS.md for reference scanning", "error", err)
		return nil
	}

	content := string(data)
	matches := markdownLinkRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var refs []session.ContextFile

	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		linkPath := strings.TrimSpace(m[2])

		// Skip external URLs and absolute paths
		if strings.HasPrefix(linkPath, "http://") || strings.HasPrefix(linkPath, "https://") ||
			strings.HasPrefix(linkPath, "/") || strings.HasPrefix(linkPath, "#") {
			continue
		}

		// Resolve relative to AGENTS.md's directory (the workspace root)
		resolved := filepath.Join(workspace, linkPath)
		cleaned, err := filepath.Abs(resolved)
		if err != nil {
			continue
		}

		// Ensure the file is within the workspace
		wsAbs, err := filepath.Abs(workspace)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(cleaned, wsAbs) {
			continue
		}

		// Check the file exists
		info, err := os.Stat(cleaned)
		if err != nil || info.IsDir() {
			continue
		}

		rel, err := filepath.Rel(workspace, cleaned)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)

		if seen[rel] {
			continue
		}
		seen[rel] = true

		refs = append(refs, session.ContextFile{
			Path:  rel,
			Depth: 1,
		})
	}

	return refs
}
