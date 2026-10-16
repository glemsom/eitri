// Package skillindex defines the model-visible skill index block's wire marker
// and the predicate that recognises it. The renderer (internal/tools) and its
// consumers (the engine's compaction and history partitioning) share this single
// source, so the block's identity is never re-derived from a repeated literal.
package skillindex

import "strings"

// OpenTag and CloseTag delimit the rendered model-visible skill index block.
// OpenTag's literal is the block's identity: the predicate matches on it, so
// changing the rendered shape means changing this one constant.
const (
	OpenTag  = "<available_skills>"
	CloseTag = "</available_skills>"
)

// IsBlock reports whether content carries the rendered skill index block.
func IsBlock(content string) bool {
	return strings.Contains(content, OpenTag)
}
