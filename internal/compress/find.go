package compress

import (
	"fmt"
	"sort"
	"strings"
)

// compressFind compresses find/fd output (list of file paths).
func compressFind(output string) *string {
	lines := strings.Split(output, "\n")

	// Filter empty lines and collect paths.
	var paths []string
	for _, line := range lines {
		tr := strings.TrimSpace(line)
		if tr == "" {
			continue
		}
		// Skip noisy directories.
		if shouldSkip(tr) {
			continue
		}
		paths = append(paths, tr)
	}

	if len(paths) < 5 {
		return nil
	}

	// Group by directory.
	byDir := make(map[string][]string)
	for _, p := range paths {
		dir, file := splitPath(p)
		byDir[dir] = append(byDir[dir], file)
	}

	// Sort directories for deterministic output.
	var dirs []string
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%dF %dD:\n", len(paths), len(dirs)))

	for _, dir := range dirs {
		files := byDir[dir]
		sb.WriteString(fmt.Sprintf("\n%s/", dir))

		show := files
		if len(show) > 10 {
			show = show[:10]
		}

		var lineBuf strings.Builder
		for _, f := range show {
			if lineBuf.Len() > 0 && lineBuf.Len()+len(f)+1 > 60 {
				sb.WriteString(fmt.Sprintf("\n  %s", lineBuf.String()))
				lineBuf.Reset()
			}
			if lineBuf.Len() > 0 {
				lineBuf.WriteByte(' ')
			}
			lineBuf.WriteString(f)
		}
		if lineBuf.Len() > 0 {
			sb.WriteString(fmt.Sprintf("\n  %s", lineBuf.String()))
		}

		if len(files) > 10 {
			sb.WriteString(fmt.Sprintf("\n  ... +%d more", len(files)-10))
		}
	}

	result := sb.String()
	return &result
}

// splitPath splits a path into directory and filename components.
// "src/util/helpers.rs" → ("src/util", "helpers.rs")
// "main.go" → (".", "main.go")
func splitPath(path string) (string, string) {
	// Strip leading "./" for cleaner grouping.
	path = strings.TrimPrefix(path, "./")

	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[:idx], path[idx+1:]
	}
	return ".", path
}

// shouldSkip returns true if the path matches a noisy directory pattern.
func shouldSkip(path string) bool {
	skipDirs := []string{
		"node_modules/",
		".git/",
		"target/debug/",
		"target/release/",
		"__pycache__/",
		".svelte-kit/",
		".next/",
		"dist/",
	}

	for _, skip := range skipDirs {
		if strings.Contains(path, skip) {
			return true
		}
	}

	// Skip .DS_Store files.
	if strings.HasSuffix(path, ".DS_Store") {
		return true
	}

	return false
}
