// Package skillindex defines the model-visible skill index block's wire marker
// and the predicate that recognises it, plus the skill activation block's marker
// and the scan that derives the active set from the conversation. The renderer
// (internal/tools) and its consumers (the engine's compaction, history
// partitioning, and activity scan) share this single source, so the blocks'
// identity is never re-derived from a repeated literal.
package skillindex

import (
	"context"
	"fmt"
	"strings"
)

// OpenTag and CloseTag delimit the rendered model-visible skill index block.
// OpenTag's literal is the block's identity: the predicate matches on it, so
// changing the rendered shape means changing this one constant.
const (
	OpenTag  = "<available_skills>"
	CloseTag = "</available_skills>"
)

// ContentTag opens one rendered skill activation block. Like OpenTag it is the
// block's identity: the partitioner and the engine's active-set scan match on it,
// so the renderer and its recognisers cannot drift apart.
const ContentTag = "<skill_content"

// IsBlock reports whether content carries the rendered skill index block.
func IsBlock(content string) bool {
	return strings.Contains(content, OpenTag)
}

// ContentOpen renders a skill activation block's opening tag for name, carrying
// the absolute skill directory so the model can reach bundled resources without
// guessing a path.
func ContentOpen(name, directory string) string {
	return fmt.Sprintf(`%s name=%q directory=%q>`, ContentTag, name, directory)
}

// IsContentBlock reports whether content carries at least one rendered skill
// activation block.
func IsContentBlock(content string) bool {
	return strings.Contains(content, ContentTag)
}

// ContentNames returns the names of every skill activation block in content, in
// order. The opening tag's name attribute is the only field read, so the scan is
// insensitive to the body and to the directory attribute.
func ContentNames(content string) []string {
	var names []string
	for {
		i := strings.Index(content, ContentTag)
		if i < 0 {
			return names
		}
		content = content[i+len(ContentTag):]
		j := strings.Index(content, `name="`)
		if j < 0 {
			return names
		}
		content = content[j+len(`name="`):]
		k := strings.IndexByte(content, '"')
		if k < 0 {
			return names
		}
		names = append(names, content[:k])
		content = content[k+1:]
	}
}

type activeSkillsKey struct{}

// WithActiveSkills returns a context carrying the set of skills already present
// in the conversation, so the skill tool can short-circuit a repeat activation.
// An empty set returns ctx unchanged.
func WithActiveSkills(ctx context.Context, names []string) context.Context {
	if len(names) == 0 {
		return ctx
	}
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return context.WithValue(ctx, activeSkillsKey{}, set)
}

// ActiveSkills reports the active-skill set carried by ctx, or nil when none was
// supplied.
func ActiveSkills(ctx context.Context) map[string]bool {
	set, _ := ctx.Value(activeSkillsKey{}).(map[string]bool)
	return set
}
