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

// Skipped records a skill pack whose SKILL.md failed to parse, so a lenient
// discovery drop is never silent: callers surface it in the TUI skill listing
// and the batch notice.
type Skipped struct {
	Name   string
	Scope  string
	Reason string
}

// Catalog is the filtered, trust-gated set of discoverable skills for a run.
// It backs the human `/skillname` slash surface and, via RenderIndex, supplies
// the model a name/path/description inventory of model-invocable skills. The
// model still has no `skill` tool and loads pack bodies itself via `bash cat`.
type Catalog struct {
	skills  map[string]*Skill
	scopes  map[string]string // skill name -> install scope ("builtin", "user", or "project")
	order   []string          // skill names, sorted, project-shadows-user-by-name
	skipped []Skipped         // packs skipped for an unparseable SKILL.md, in discovery order
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

// SkippedSkills returns the packs skipped during discovery (in discovery order),
// so lenient rejection can be surfaced rather than silently dropping them.
func (c *Catalog) SkippedSkills() []Skipped {
	if c == nil {
		return nil
	}
	return c.skipped
}

// Discover scans the user-global root (~/.agents/skills), the project root (.agents/skills), and the builtin root (the materialized $EITRI_DIR/skills-builtin) for skill packs (a subdir containing a parseable SKILL.md). On exact-name collision the strongest claim wins: project shadows user, and user shadows builtin; builtin is the weakest claim and inherits trust from being part of the binary.
func Discover(userRoot, projectRoot, builtinRoot string, w SkillWarner) (*Catalog, error) {
	c := &Catalog{
		skills: map[string]*Skill{},
		scopes: map[string]string{},
	}

	// Builtin is discovered first so the higher scopes overwrite it: discovery
	// order is the shadowing order (project > user > builtin).
	if err := discoverScope(builtinRoot, c, "builtin", w); err != nil {
		return nil, err
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
		skill, parseErr := parseSkill(packDir)
		if parseErr != nil {
			if w != nil {
				w.Warnf("skill %q: skipping unparseable SKILL.md in scope %s: %v", name, scope, parseErr)
			}
			c.skipped = append(c.skipped, Skipped{Name: name, Scope: scope, Reason: parseErr.Error()})
			continue
		}
		c.skills[name] = skill
		c.scopes[name] = scope
	}
	return nil
}

func parseSkill(packDir string) (*Skill, error) {
	md := filepath.Join(packDir, "SKILL.md")
	data, err := os.ReadFile(md)
	if err != nil {
		return nil, err
	}
	body, front, ok := splitFrontmatter(string(data))
	if !ok {
		return nil, fmt.Errorf("frontmatter must be delimited by exact --- lines")
	}
	meta, err := parseFrontmatter(front)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(meta["name"])
	if name == "" {
		return nil, fmt.Errorf("name must be a non-empty scalar")
	}
	desc := strings.TrimSpace(meta["description"])
	if desc == "" {
		return nil, fmt.Errorf("description must be a non-empty scalar")
	}

	modelInvocable := true
	modelValue, hasModel := meta["model-invocable"]
	disableValue, hasDisable := meta["disable-model-invocation"]
	if hasModel {
		modelInvocable, err = parseFrontmatterBool("model-invocable", modelValue)
		if err != nil {
			return nil, err
		}
	}
	if hasDisable {
		disabled, boolErr := parseFrontmatterBool("disable-model-invocation", disableValue)
		if boolErr != nil {
			return nil, boolErr
		}
		if hasModel && modelInvocable == disabled {
			return nil, fmt.Errorf("conflicting model-invocable and disable-model-invocation values")
		}
		modelInvocable = !disabled
	}

	return &Skill{
		Description:    desc,
		Body:           strings.TrimPrefix(body, "\n"),
		Resources:      bundledResources(packDir, md),
		Dir:            packDir,
		ModelInvocable: modelInvocable,
	}, nil
}

func parseFrontmatterBool(key, value string) (bool, error) {
	switch {
	case strings.EqualFold(value, "true"):
		return true, nil
	case strings.EqualFold(value, "false"):
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", key)
	}
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
	lines := strings.Split(s, "\n")
	if len(lines) < 3 || lines[0] != "---" {
		return "", "", false
	}
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			return strings.Join(lines[i+1:], "\n"), strings.Join(lines[1:i], "\n"), true
		}
	}
	return "", "", false
}

// parseFrontmatter accepts flat key/scalar fields, indented plain-text
// continuations for description, and YAML literal (`|`) and folded (`>`) block
// scalars for scalar fields (with `-`/`+` chomping). Duplicate fields and
// unsupported YAML collection syntax are rejected rather than interpreted
// partially, while ancillary metadata subtrees of unknown keys are ignored so
// real-world SKILL.md frontmatter stays discoverable.
func parseFrontmatter(s string) (map[string]string, error) {
	out := map[string]string{}
	lines := strings.Split(s, "\n")
	var curKey string
	for i := 0; i < len(lines); {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			i++
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			continuation := strings.TrimSpace(line)
			if curKey == "description" && !strings.Contains(continuation, ":") {
				out[curKey] += " " + continuation
				i++
				continue
			}
			if curKey != "name" && curKey != "description" && curKey != "model-invocable" && curKey != "disable-model-invocation" {
				if strings.HasPrefix(continuation, "- ") || strings.Contains(continuation, ":") {
					i++
					continue
				}
			}
			return nil, fmt.Errorf("frontmatter line %d: unsupported nested or continuation syntax", i+1)
		}
		idx := strings.IndexByte(line, ':')
		if idx <= 0 {
			return nil, fmt.Errorf("frontmatter line %d: expected key: value", i+1)
		}
		key := strings.ToLower(strings.TrimSpace(line[:idx]))
		if key == "" || strings.ContainsAny(key, " []{}#,\t") {
			return nil, fmt.Errorf("frontmatter line %d: invalid key", i+1)
		}
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("frontmatter line %d: duplicate key %q", i+1, key)
		}
		value := strings.TrimSpace(line[idx+1:])
		if ind, chomp, ok := parseBlockIndicator(value); ok {
			// A block scalar's content is the run of lines indented past the
			// key's column; consume them and advance past the block.
			j := i + 1
			var block []string
			for j < len(lines) && (lines[j] == "" || lines[j][0] == ' ' || lines[j][0] == '\t') {
				block = append(block, lines[j])
				j++
			}
			v, err := decodeBlockScalar(ind, chomp, block)
			if err != nil {
				return nil, fmt.Errorf("frontmatter line %d: %v", i+1, err)
			}
			out[key] = v
			curKey = key
			i = j
			continue
		}
		if strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{") || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">") {
			return nil, fmt.Errorf("frontmatter line %d: unsupported value syntax", i+1)
		}
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		} else if strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'") {
			return nil, fmt.Errorf("frontmatter line %d: unterminated quoted scalar", i+1)
		}
		out[key] = value
		curKey = key
		i++
	}
	return out, nil
}

// parseBlockIndicator recognizes a YAML literal (`|`) or folded (`>`) block
// scalar indicator with optional chomping (`-` strip, `+` keep) and an optional
// explicit indentation digit. It reports false for any value that is not a bare
// block indicator so plain scalars and flow collections fall through to normal
// handling.
func parseBlockIndicator(v string) (ind byte, chomp byte, ok bool) {
	if v == "" || (v[0] != '|' && v[0] != '>') {
		return 0, 0, false
	}
	ind = v[0]
	rest := v[1:]
	chomp = 0
	if rest != "" {
		c := rest[0]
		switch {
		case c == '-' || c == '+':
			chomp = c
			rest = rest[1:]
		case c >= '0' && c <= '9':
			rest = rest[1:]
			if rest != "" && (rest[0] == '-' || rest[0] == '+') {
				chomp = rest[0]
				rest = rest[1:]
			}
		}
	}
	if strings.TrimSpace(rest) != "" {
		return 0, 0, false
	}
	return ind, chomp, true
}

// decodeBlockScalar assembles the collected lines of a YAML block scalar into
// its string value: literal `|` keeps line breaks verbatim, folded `>` joins
// consecutive non-empty lines with a single space and preserves blank lines as
// newlines. chomp applies the trailing-newline policy (0 = clip to one,
// '-' = strip, '+' = keep).
func decodeBlockScalar(ind byte, chomp byte, block []string) (string, error) {
	lines := make([]string, len(block))
	for i, l := range block {
		lines[i] = strings.TrimSpace(l)
	}
	var body string
	if ind == '|' {
		body = strings.Join(lines, "\n")
	} else {
		var b strings.Builder
		prevBlank := true
		for _, l := range lines {
			if l == "" {
				b.WriteByte('\n')
				prevBlank = true
				continue
			}
			if !prevBlank {
				b.WriteByte(' ')
			}
			b.WriteString(l)
			prevBlank = false
		}
		body = b.String()
	}
	switch chomp {
	case '-':
		return strings.TrimRight(body, "\n"), nil
	case '+':
		return body, nil
	default: // clip: at most one trailing newline
		body = strings.TrimRight(body, "\n")
		if body != "" {
			body += "\n"
		}
		return body, nil
	}
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
