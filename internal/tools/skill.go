package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillWarner receives lenient-discovery warnings so a caller can surface them
// (log/stderr) without failing the run. Fail-closed: unparseable skills are
// omitted, and the warning is the only trace.
type SkillWarner interface {
	Warnf(format string, args ...any)
}

// Skill is one discovered, validated skill pack. Body is the SKILL.md content
// with any YAML frontmatter stripped; Resources lists the relative paths of
// the pack's bundled files (excluding SKILL.md) so the model can resolve them
// against Dir via the read/list tools.
type Skill struct {
	Description string
	Body        string
	Resources   []string
	Dir         string
}

// Catalog is the filtered, trust-gated set of discoverable skills for a run.
// It owns the discovery-order name list (for the strict enum), the per-name
// install scope (user/project) and the per-run activation set (for dedupe).
type Catalog struct {
	skills    map[string]*Skill
	scopes    map[string]string // skill name -> install scope ("user" or "project")
	hidden    map[string]bool   // skill name -> hide-not-block (disable-model-invocation)
	order     []string
	activated map[string]bool
}

// Names returns the discovered skill names in stable (scope, then sorted)
// order. Zero skills yields an empty slice — the caller then omits the skill
// tool entirely.
func (c *Catalog) Names() []string {
	out := make([]string, len(c.order))
	copy(out, c.order)
	return out
}

// Skill returns the named skill, or nil when it is not in the catalog.
func (c *Catalog) Skill(name string) *Skill {
	return c.skills[name]
}

// Scope returns the install scope ("user" or "project") for the named skill,
// or "" when the name is not in the catalog.
func (c *Catalog) Scope(name string) string {
	if c == nil {
		return ""
	}
	return c.scopes[name]
}

// Enum returns the strict-schema enum values: only the names the model may
// invoke. Hidden (disable-model-invocation) skills are human-invoked via the
// slash surface and so are excluded from the model-facing enum (hide-not-
// block), even though Names() still surfaces them for the `/` completion.
func (c *Catalog) Enum() []any {
	out := make([]any, 0, len(c.order))
	for _, n := range c.order {
		if c.hidden[n] {
			continue
		}
		out = append(out, n)
	}
	return out
}

// IsActive reports whether name has already been injected into this session's
// context (used to skip re-injection on re-activation).
func (c *Catalog) IsActive(name string) bool {
	return c.activated[name]
}

// MarkActive records that name has been injected, so a later activation
// dedupes. Unknown names are ignored.
func (c *Catalog) MarkActive(name string) {
	if _, ok := c.skills[name]; ok {
		c.activated[name] = true
	}
}

// Discover scans the user-global root (~/.agents/skills) and the project root
// (.agents/skills) for skill packs (a subdir containing a parseable SKILL.md).
// A project pack shadows a user pack of the exact same name. Only valid,
// filtered packs are retained; a pack whose frontmatter cannot be parsed
// leniently is omitted with a warning (fail-closed). It never returns a
// partially-populated catalog on error.
func Discover(userRoot, projectRoot string, w SkillWarner) (*Catalog, error) {
	c := &Catalog{
		skills:    map[string]*Skill{},
		scopes:    map[string]string{},
		hidden:    map[string]bool{},
		activated: map[string]bool{},
	}

	// User scope first; project scope then overwrites on exact-name collision.
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
	// skillCataloged: the pack parsed and is cataloged (hidden-or-invocable; the
	// disable-model-invocation distinction rides on the hidden bool).
	skillCataloged skillParseStatus = iota
	// skillUnparseable: the frontmatter is invalid; omit fail-closed with a warn.
	skillUnparseable
)

// discoverScope walks root for skill pack directories and folds them into c.
// root is the scope's <scope>/skills parent (may not exist). Unparseable packs
// are omitted fail-closed with a warn; disable-model-invocation packs are kept
// (human slash-invocable) but flagged hidden so Enum() excludes them from the
// model-facing surface.
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
		skill, hidden, status := parseSkill(packDir)
		if status == skillUnparseable {
			if w != nil {
				w.Warnf("skill %q: skipping unparseable SKILL.md in scope %s", name, scope)
			}
			continue
		}
		// Project shadows user (and a later project entry wins over an earlier
		// one) by simple overwrite of the same keyed name.
		c.skills[name] = skill
		c.scopes[name] = scope
		// Hide-not-block: a disable-model-invocation pack is still human-
		// invocable via the slash `/` surface (it lists+activates it), but is
		// excluded from the model-facing enum by Enum(). Zero value stays false
		// for invocable packs; shadowing overwrites it per winning scope.
		c.hidden[name] = hidden
	}
	return nil
}

// parseSkill reads a pack's SKILL.md, strips its frontmatter leniently, and
// collects the packaged resources. It returns the parsed skill, a hidden bool
// (disable-model-invocation: true, aliased to human-only invocation), and
// skillUnparseable (fail-closed) when the frontmatter cannot be parsed.
func parseSkill(packDir string) (*Skill, bool, skillParseStatus) {
	md := filepath.Join(packDir, "SKILL.md")
	data, err := os.ReadFile(md)
	if err != nil {
		return nil, false, skillUnparseable
	}
	body, front, ok := splitFrontmatter(string(data))
	if !ok {
		return nil, false, skillUnparseable
	}
	meta := parseFrontmatter(front)
	name, ok := meta["name"]
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return nil, false, skillUnparseable
	}
	desc := meta["description"]
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return nil, false, skillUnparseable
	}
	hidden := disableModelInvocation(meta["disable-model-invocation"])

	res := bundledResources(packDir, md)
	return &Skill{
		Description: desc,
		Body:        strings.TrimPrefix(body, "\n"),
		Resources:   res,
		Dir:         packDir,
	}, hidden, skillCataloged
}

// disableModelInvocation reports whether the disable-model-invocation
// frontmatter field is truthy (true/1/yes), so the pack is hidden from the
// model (hide-not-block).
func disableModelInvocation(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

// bundledResources lists the pack's files (relative paths) excluding SKILL.md,
// deterministically sorted. Resources are advertised, never read eagerly.
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

// validSkillName enforces the Agent Skills name rule (lowercase alphanumeric
// plus hyphens, 1..64 chars) cheaply at discovery so malformed dir names are
// excluded without being parsed.
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

// splitFrontmatter splits SKILL.md content into a frontmatter string and the
// body. It returns ok=false when no `---`-delimited frontmatter block exists at
// the top of the file.
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
	// Find the closing `---` on its own line.
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", false
	}
	front = rest[:end]
	body = rest[end+4:]
	return body, front, true
}

// parseFrontmatter leniently extracts `key: value` fields from a frontmatter
// block. It folds continuations after the first `key:` into the value and
// handles quoted values by trimming surrounding quotes. Fields are lowercased
// keys for case-insensitive lookup.
func parseFrontmatter(s string) map[string]string {
	out := map[string]string{}
	var curKey string
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// A value continuation line (starts with whitespace) appends to the
		// current key, matching YAML block-scalar style for description.
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

// skillTool is the dedicated skill activation tool. Its name is constrained to
// an enum of the catalog's names and it returns the pack body wrapped per the
// agentskills-io payload shape. It omits disabled/filtered skills (already
// filtered from the catalog) and dedupes re-activation of an in-context skill.
type skillTool struct {
	c *Catalog
}

func (s *skillTool) Name() string { return "skill" }

func (s *skillTool) Description() string {
	return "Activate an Agent Skill pack, injecting its instructions into context as a tool result. Choose the matching skill by name from the provided enum; the pack's markdown and bundled resources are returned for you to follow. Skill instructions are tool results and never elevated to a system message."
}

func (s *skillTool) Schema() map[string]any {
	return strictSchema(map[string]any{
		"name": map[string]any{
			"type":        "string",
			"description": "The name of the skill to activate, chosen from the enum of available skills.",
			"enum":        s.c.Enum(),
		},
	}, []string{"name"})
}

func (s *skillTool) Run(ctx context.Context, args map[string]any) (ToolResult, error) {
	name, err := strArg(args, "name")
	if err != nil {
		return ToolResult{}, err
	}
	if s.c.IsActive(name) {
		return ToolResult{Text: fmt.Sprintf("skill %q is already active in this context; no re-injection performed.", name)}, nil
	}
	sk := s.c.Skill(name)
	if sk == nil {
		return ToolResult{}, fmt.Errorf("unknown skill %q", name)
	}
	s.c.MarkActive(name)
	return ToolResult{Text: renderSkillPayload(name, sk)}, nil
}

// renderSkillPayload builds the structured agentskills-io payload: the body
// wrapped in <skill_content name="..."> plus a <skill_resources> listing of the
// bundled files. The wrapping tags double as the compaction ring-fence marker.
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
