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
// It owns the discovery-order name list (for the strict enum) and the per-run
// activation set (for dedupe).
type Catalog struct {
	skills    map[string]*Skill
	order     []string
	activated map[string]bool
}

// Names returns the discovered skill names in stable (scope, then sorted)
// order. Zero skills yields an empty slice — the caller then omits the skill
// tool entirely (docs/spec.md §3, ticket #14).
func (c *Catalog) Names() []string {
	out := make([]string, len(c.order))
	copy(out, c.order)
	return out
}

// Skill returns the named skill, or nil when it is not in the catalog.
func (c *Catalog) Skill(name string) *Skill {
	return c.skills[name]
}

// Enum returns the strict-schema enum values: the valid, filtered skill names.
func (c *Catalog) Enum() []any {
	out := make([]any, len(c.order))
	for i, n := range c.order {
		out[i] = n
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
	// skillCataloged: the pack parsed and is model-invocable; add it.
	skillCataloged skillParseStatus = iota
	// skillHidden: the pack parsed but declares disable-model-invocation: true,
	// so it is hidden from the catalog and the tool enum (hide-not-block).
	skillHidden
	// skillUnparseable: the frontmatter is invalid; omit fail-closed with a warn.
	skillUnparseable
)

// discoverScope walks root for skill pack directories and folds the model-
// invocable ones into c. root is the scope's <scope>/skills parent (may not
// exist). Hidden (disable-model-invocation) and unparseable packs are omitted
// per hide-not-block; only unparseable ones warn.
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
		switch status {
		case skillHidden:
			// Hide-not-block: a disable-model-invocation pack is not listed for
			// the model, so it is never returned to be blocked at call time.
			continue
		case skillUnparseable:
			if w != nil {
				w.Warnf("skill %q: skipping unparseable SKILL.md in scope %s", name, scope)
			}
			continue
		}
		// Project shadows user (and a later project entry wins over an earlier
		// one) by simple overwrite of the same keyed name.
		c.skills[name] = skill
	}
	return nil
}

// parseSkill reads a pack's SKILL.md, strips its frontmatter leniently, and
// collects the packaged resources. It reports skillUnparseable (fail-closed)
// when the frontmatter cannot be parsed validly, and skillHidden when the pack
// declares disable-model-invocation: true (hidden from the model, hide-not-
// block).
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
	desc, _ := meta["description"]
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return nil, skillUnparseable
	}
	if disableModelInvocation(meta["disable-model-invocation"]) {
		return nil, skillHidden
	}

	res := bundledResources(packDir, md)
	return &Skill{
		Description: desc,
		Body:        strings.TrimPrefix(body, "\n"),
		Resources:   res,
		Dir:         packDir,
	}, skillCataloged
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

func (s *skillTool) Run(ctx context.Context, args map[string]any) (string, error) {
	name, err := strArg(args, "name")
	if err != nil {
		return "", err
	}
	if s.c.IsActive(name) {
		return fmt.Sprintf("skill %q is already active in this context; no re-injection performed.", name), nil
	}
	sk := s.c.Skill(name)
	if sk == nil {
		return "", fmt.Errorf("unknown skill %q", name)
	}
	s.c.MarkActive(name)
	return renderSkillPayload(name, sk), nil
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
