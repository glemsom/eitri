package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillWarner receives lenient-discovery warnings so a caller can surface them (log/stderr) without failing the run.
type SkillWarner interface {
	Warnf(format string, args ...any)
}

// Skill is one discovered, validated skill pack.
type Skill struct {
	Description string
	Body        string
	Resources   []string
	Dir         string
	// ModelInvocable reports whether the model may discover and use this skill
	// via the rendered skill index. Non-model-invocable skills stay reachable
	// through the human slash surface only.
	ModelInvocable bool
}

// Catalog is the filtered, trust-gated set of discoverable skills for a run.
// It backs the human `/skillname` slash surface and, via RenderIndex, supplies
// the model a name/path/description inventory of model-invocable skills. The
// model still has no `skill` tool and loads pack bodies itself via `bash cat`.
type Catalog struct {
	skills map[string]*Skill
	scopes map[string]string // skill name -> install scope ("user" or "project")
	order  []string          // skill names, sorted, project-shadows-user by name
}

func (c *Catalog) Names() []string {
	out := make([]string, len(c.order))
	copy(out, c.order)
	return out
}

func (c *Catalog) Skill(name string) *Skill {
	return c.skills[name]
}

// ModelVisibleSkills returns the model-visible skill names in sorted order:
// non-model-invocable skills filtered out, project scope shadowing user scope on
// name collision (already resolved at discovery).
func (c *Catalog) ModelVisibleSkills() []string {
	var out []string
	for _, name := range c.order {
		if c.skills[name] != nil && c.skills[name].ModelInvocable {
			out = append(out, name)
		}
	}
	return out
}

// RenderIndex renders the model-visible skill inventory as an XML block of the
// form <available_skills><skill><name/><path/><description/></skill>...</available_skills>.
// Each <path> is the absolute path to the pack's SKILL.md. Paths are escaped so
// multi-line descriptions and &/<> characters stay well-formed. When no skill is
// model-visible the result is empty; callers treat that as "omit the block".
func (c *Catalog) RenderIndex() string {
	names := c.ModelVisibleSkills()
	if len(names) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<available_skills>")
	for _, name := range names {
		sk := c.skills[name]
		b.WriteString("<skill>")
		b.WriteString("<name>" + xmlEscape(name) + "</name>")
		b.WriteString("<path>" + xmlEscape(filepath.Join(sk.Dir, "SKILL.md")) + "</path>")
		b.WriteString("<description>" + xmlEscape(sk.Description) + "</description>")
		b.WriteString("</skill>")
	}
	b.WriteString("</available_skills>")
	return b.String()
}

// xmlEscape escapes the five XML entities so text forms stay well-formed.
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

func (c *Catalog) Scope(name string) string {
	if c == nil {
		return ""
	}
	return c.scopes[name]
}

// Discover scans the user-global root (~/.agents/skills) and the project root (.agents/skills) for skill packs (a subdir containing a parseable SKILL.md).
func Discover(userRoot, projectRoot string, w SkillWarner) (*Catalog, error) {
	c := &Catalog{
		skills: map[string]*Skill{},
		scopes: map[string]string{},
	}

	if err := discoverScope(userRoot, c, "user", w); err != nil {
		return nil, err
	}
	if err := discoverScope(projectRoot, c, "project", w); err != nil {
		return nil, err
	}

	c.order = make([]string, 0, len(c.skills))
	for name := range c.skills {
		c.order = append(c.order, name)
	}
	sort.Strings(c.order)
	return c, nil
}

// skillParseStatus describes why a discovered pack was or wasn't cataloged.
type skillParseStatus int

const (
	skillCataloged skillParseStatus = iota
	skillUnparseable
)

// discoverScope walks root for skill pack directories and folds them into c. root is the scope's <scope>/skills parent (may not exist).
func discoverScope(root string, c *Catalog, scope string, w SkillWarner) error {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !validSkillName(name) {
			continue
		}
		packDir := filepath.Join(root, name)
		skill, status := parseSkill(packDir)
		if status == skillUnparseable {
			if w != nil {
				w.Warnf("skill %q: skipping unparseable SKILL.md in scope %s", name, scope)
			}
			continue
		}
		c.skills[name] = skill
		c.scopes[name] = scope
	}
	return nil
}

// parseSkill reads a pack's SKILL.md, strips its frontmatter leniently, and collects the packaged resources.
func parseSkill(packDir string) (*Skill, skillParseStatus) {
	md := filepath.Join(packDir, "SKILL.md")
	data, err := os.ReadFile(md)
	if err != nil {
		return nil, skillUnparseable
	}
	body, front, ok := splitFrontmatter(string(data))
	if !ok {
		return nil, skillUnparseable
	}
	meta := parseFrontmatter(front)
	name, ok := meta["name"]
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return nil, skillUnparseable
	}
	desc := meta["description"]
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return nil, skillUnparseable
	}

	res := bundledResources(packDir, md)
	s := &Skill{
		Description:    desc,
		Body:           strings.TrimPrefix(body, "\n"),
		Resources:      res,
		Dir:            packDir,
		ModelInvocable: true,
	}
	if v, ok := meta["model-invocable"]; ok {
		s.ModelInvocable = strings.EqualFold(strings.TrimSpace(v), "true")
	} else if v, ok := meta["disable-model-invocation"]; ok {
		s.ModelInvocable = !strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return s, skillCataloged
}

// bundledResources lists the pack's files (relative paths) excluding SKILL.md, deterministically sorted.
func bundledResources(packDir, exclude string) []string {
	var out []string
	_ = filepath.WalkDir(packDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == exclude {
			return nil
		}
		out = append(out, strings.TrimPrefix(filepath.ToSlash(p), filepath.ToSlash(packDir)+"/"))
		return nil
	})
	sort.Strings(out)
	return out
}

// validSkillName enforces the Agent Skills name rule (lowercase alphanumeric plus hyphens, 1..64 chars) cheaply at discovery so malformed dir names are excluded without being parsed.
func validSkillName(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-':
		default:
			return false
		}
	}
	return true
}

func splitFrontmatter(s string) (body, front string, ok bool) {
	if !strings.HasPrefix(s, "---") {
		return "", "", false
	}
	rest := s[3:]
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return "", "", false
	}
	rest = rest[nl+1:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", false
	}
	front = rest[:end]
	body = rest[end+4:]
	return body, front, true
}

// parseFrontmatter leniently extracts `key: value` fields from a frontmatter block.
func parseFrontmatter(s string) map[string]string {
	out := map[string]string{}
	var curKey string
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if curKey != "" && (line[0] == ' ' || line[0] == '\t') {
			out[curKey] += " " + strings.TrimSpace(trimmed)
			continue
		}
		idx := strings.IndexByte(trimmed, ':')
		if idx <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(trimmed[:idx]))
		val := strings.TrimSpace(trimmed[idx+1:])
		val = strings.Trim(val, `"'`)
		out[key] = val
		curKey = key
	}
	return out
}

// renderSkillPayload builds the structured agentskills-io payload: the body wrapped in <skill_content name="..."> plus a <skill_resources> listing of the bundled files.
func renderSkillPayload(name string, sk *Skill) string {
	var b strings.Builder
	b.WriteString("skill content for active session; tags: [\"skill\"]\n\n")
	fmt.Fprintf(&b, `<skill_content name="%s">`+"\n", name)
	b.WriteString(sk.Body)
	b.WriteString("\n</skill_content>\n")
	if len(sk.Resources) > 0 {
		fmt.Fprintf(&b, `<skill_resources name="%s">`+"\n", name)
		for _, r := range sk.Resources {
			fmt.Fprintf(&b, "- %s\n", r)
		}
		b.WriteString("</skill_resources>\n")
	}
	return b.String()
}
