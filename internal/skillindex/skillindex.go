// Package skillindex defines the model-visible skill index block's wire marker
// and the predicate that recognises it, plus the skill activation block's open
// and close markers and the scan that derives the active set from the
// conversation. The renderer (internal/tools) and its consumers (the engine's
// compaction, history partitioning, and activity scan) share this single source,
// so the blocks' identity is never re-derived from a repeated literal.
package skillindex

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// OpenTag and CloseTag delimit the rendered model-visible skill index block.
// OpenTag's literal is the block's identity: the predicate matches on it, so
// changing the rendered shape means changing this one constant.
const (
	OpenTag  = "<available_skills>"
	CloseTag = "</available_skills>"
)

// ActivationTag and ActivationClose delimit one rendered skill activation block.
// ActivationTag's literal is the block's identity: the partitioner and the
// engine's active-set scan match on it, so the renderer and its recognisers
// cannot drift apart.
const (
	ActivationTag   = "<skill_content"
	ActivationClose = "</skill_content>"
)

// IsBlock reports whether content carries the rendered skill index block.
func IsBlock(content string) bool {
	return strings.Contains(content, OpenTag)
}

// ActivationOpen renders a skill activation block's opening tag for name,
// carrying the absolute skill directory so the model can reach bundled
// resources without guessing a path.
func ActivationOpen(name, directory string) string {
	return fmt.Sprintf(`%s name=%q directory=%q>`, ActivationTag, name, directory)
}

// HasActivation reports whether content carries at least one rendered skill
// activation block.
func HasActivation(content string) bool {
	return strings.Contains(content, ActivationTag)
}

// ActivationNames returns the names of every complete skill activation block in
// content, in order. The opening tag's name attribute is the only field read, so
// the scan is insensitive to the body and to the directory attribute. A block is
// complete only when ActivationClose follows it: the tool-result byte cap
// head-truncates an oversized body, keeping the opening tag while dropping the
// close and the instructions' tail, and an unclosed block must not read as
// active.
func ActivationNames(content string) []string {
	var names []string
	for {
		i := strings.Index(content, ActivationTag)
		if i < 0 {
			return names
		}
		content = content[i+len(ActivationTag):]
		j := strings.Index(content, `name="`)
		if j < 0 {
			return names
		}
		content = content[j+len(`name="`):]
		k := strings.IndexByte(content, '"')
		if k < 0 {
			return names
		}
		name := content[:k]
		content = content[k+1:]
		close := strings.Index(content, ActivationClose)
		if close < 0 {
			return names
		}
		names = append(names, name)
		content = content[close+len(ActivationClose):]
	}
}

type activeSkillsKey struct{}

// ActivationSet is one turn's already-active skills, seeded from the message
// history's activation blocks and shared by that turn's tool calls. A claim
// reserves a name for the turn, so concurrent same-turn calls for one skill
// resolve to exactly one full payload and the rest to the already-active note.
// The set is rebuilt every turn from the history, so compaction-dropped bodies
// re-activate and a fresh session starts empty.
type ActivationSet struct {
	mu    sync.Mutex
	names map[string]bool
}

// NewActivationSet returns a set seeded with the given active skill names.
func NewActivationSet(names []string) *ActivationSet {
	set := &ActivationSet{names: make(map[string]bool, len(names))}
	for _, name := range names {
		set.names[name] = true
	}
	return set
}

// Claim reports whether name was inactive; on true the caller has reserved it
// for this turn. A seeded or already-claimed name returns false.
func (s *ActivationSet) Claim(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.names[name] {
		return false
	}
	s.names[name] = true
	return true
}

// WithActiveSkills returns a context carrying a fresh activation set seeded with
// the skills already present in the conversation. The engine installs it once
// per turn, before dispatching that turn's tool calls.
func WithActiveSkills(ctx context.Context, names []string) context.Context {
	return context.WithValue(ctx, activeSkillsKey{}, NewActivationSet(names))
}

// ClaimActivation claims name against the set carried by ctx, reporting whether
// the skill was inactive. A context without a set (the tool run outside the
// engine) treats every name as inactive.
func ClaimActivation(ctx context.Context, name string) bool {
	set, _ := ctx.Value(activeSkillsKey{}).(*ActivationSet)
	if set == nil {
		return true
	}
	return set.Claim(name)
}
