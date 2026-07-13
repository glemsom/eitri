package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxFrontmatterSize is the maximum allowed size for YAML frontmatter (128KB).
const maxFrontmatterSize = 128 * 1024

// maxBodySize is the maximum allowed body size for a skill (200KB per §4.4).
const maxBodySize = 200 * 1024

// ParseSKILLMD reads and parses a SKILL.md file from a skill directory.
// Returns the parsed Skill or nil with diagnostics if parsing fails.
func ParseSKILLMD(skillDir string) (*Skill, Diagnostics) {
	mdPath := filepath.Join(skillDir, "SKILL.md")
	data, err := os.ReadFile(mdPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, Diagnostics{{Severity: SeverityError, Message: "SKILL.md not found", Path: skillDir}}
		}
		return nil, Diagnostics{{Severity: SeverityError, Message: fmt.Sprintf("cannot read SKILL.md: %v", err), Path: skillDir}}
	}

	content := string(data)
	var diags Diagnostics

	// Split frontmatter and body
	body, frontmatter, hasFM := extractFrontmatter(content)
	skill := &Skill{
		Path:   skillDir,
		Status: StatusEffective,
	}

	if !hasFM {
		return nil, Diagnostics{{Severity: SeverityError, Message: "YAML frontmatter not found in SKILL.md", Path: skillDir}}
	}

	// Parse frontmatter
	fm, fmDiags := parseFrontmatter(frontmatter, skillDir)
	diags = append(diags, fmDiags...)
	if fm == nil {
		return nil, diags
	}

	skill.Name = fm.Name
	skill.Description = fm.Description
	skill.License = fm.License
	skill.Compatibility = fm.Compatibility
	skill.Body = strings.TrimSpace(body)

	// Validate required fields
	if skill.Name == "" {
		return nil, append(diags, Diagnostic{Severity: SeverityError, Message: "skill name is required", Path: skillDir})
	}
	if skill.Description == "" {
		return nil, append(diags, Diagnostic{Severity: SeverityError, Message: "skill description is required", Path: skillDir})
	}

	// Enforce maximum body size
	if len(skill.Body) > maxBodySize {
		return nil, append(diags, Diagnostic{Severity: SeverityError, Message: fmt.Sprintf("skill body exceeds %dKB limit", maxBodySize/1024), Path: skillDir, Skill: skill.Name})
	}

	// Warning: name differs from parent directory name
	dirName := filepath.Base(skillDir)
	if skill.Name != dirName {
		diags = append(diags, Diagnostic{Severity: SeverityWarn, Message: fmt.Sprintf("skill name %q differs from directory name %q", skill.Name, dirName), Path: skillDir, Skill: skill.Name})
	}

	return skill, diags
}

// frontmatterData holds the parsed YAML frontmatter fields.
type frontmatterData struct {
	Name          string                 `yaml:"name"`
	Description   string                 `yaml:"description"`
	License       string                 `yaml:"license,omitempty"`
	Compatibility string                 `yaml:"compatibility,omitempty"`
	Metadata      map[string]interface{} `yaml:"metadata,omitempty"`
	AllowedTools  []string               `yaml:"allowed-tools,omitempty"`
}

// parseFrontmatter parses YAML frontmatter content.
func parseFrontmatter(fm string, skillDir string) (*frontmatterData, Diagnostics) {
	if strings.TrimSpace(fm) == "" {
		return nil, Diagnostics{{Severity: SeverityError, Message: "empty YAML frontmatter", Path: skillDir}}
	}

	// Simple key-value parsing for YAML frontmatter without requiring yaml library
	// Parse basic fields
	data := &frontmatterData{}
	diags := parseSimpleYAML(fm, data, skillDir)

	if data.Name == "" {
		return nil, append(diags, Diagnostic{Severity: SeverityError, Message: "name field missing or empty", Path: skillDir})
	}
	if data.Description == "" {
		return nil, append(diags, Diagnostic{Severity: SeverityError, Message: "description field missing or empty", Path: skillDir})
	}

	return data, diags
}

// parseSimpleYAML does a basic line-by-line parse of simple YAML frontmatter.
// Supports: name, description, license, compatibility, and simple string values.
// For complex nested fields, simply records them without error.
func parseSimpleYAML(content string, data *frontmatterData, skillDir string) Diagnostics {
	lines := strings.Split(content, "\n")
	var diags Diagnostics
	inList := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Skip list items and complex nested values
		if strings.HasPrefix(trimmed, "- ") {
			if strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") {
				continue // nested list items
			}
			inList = true
			continue
		}
		if inList && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			continue
		}
		inList = false

		// Skip lines with nested colons or complex structures
		if strings.Contains(trimmed, ": ") || strings.HasSuffix(trimmed, ":") {
			colonIdx := strings.Index(trimmed, ": ")
			if colonIdx < 0 {
				// Line ending with colon (map start) — skip
				continue
			}

			key := strings.TrimSpace(trimmed[:colonIdx])
			value := strings.TrimSpace(trimmed[colonIdx+2:])

			// Remove quotes
			value = strings.Trim(value, "\"'")

			switch key {
			case "name":
				data.Name = value
			case "description":
				data.Description = value
			case "license":
				data.License = value
			case "compatibility":
				data.Compatibility = value
			}
		}
	}

	return diags
}

// extractFrontmatter splits SKILL.md content into frontmatter and body.
// Returns body, frontmatter string, and whether frontmatter was found.
func extractFrontmatter(content string) (body, frontmatter string, found bool) {
	content = strings.TrimSpace(content)

	if !strings.HasPrefix(content, "---") {
		return content, "", false
	}

	// Skip first ---
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return content, "", false
	}

	frontmatter = strings.TrimSpace(rest[:idx])
	body = strings.TrimSpace(rest[idx+4:])
	return body, frontmatter, true
}
