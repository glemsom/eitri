package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxResourceFiles is the maximum number of resource files to list.
const maxResourceFiles = 200

// maxResourceDepth is the maximum directory depth for resource discovery (relative to skill dir).
const maxResourceDepth = 4

// resourceDirs lists the directories inside a skill that are considered resources.
var resourceDirs = []string{"scripts", "references", "assets"}

// ListResources scans a skill directory for resource files in scripts/,
// references/, and assets/ subdirectories.
// Results are capped at maxResourceFiles files at max depth maxResourceDepth.
func ListResources(skillDir string) []string {
	var resources []string

	for _, subdir := range resourceDirs {
		dir := filepath.Join(skillDir, subdir)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}

		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip unreadable entries
			}

			if info.IsDir() {
				// Check depth
				rel, _ := filepath.Rel(skillDir, path)
				depth := len(strings.Split(rel, string(filepath.Separator)))
				if depth > maxResourceDepth {
					return filepath.SkipDir
				}
				return nil
			}

			if len(resources) >= maxResourceFiles {
				return filepath.SkipDir
			}

			rel, _ := filepath.Rel(skillDir, path)
			resources = append(resources, rel)
			return nil
		})
	}

	sort.Strings(resources)
	return resources
}
