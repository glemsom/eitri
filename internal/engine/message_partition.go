package engine

import (
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/skillindex"
)

type messagePartition struct {
	StableHead []provider.Message
	persisted  []provider.Message
}

// isSystemPromptHead reports whether content is the byte-stable persona head,
// so prompt-head detection strips the head before per-run directives.
func isSystemPromptHead(content string) bool {
	return content == SystemPromptContent()
}

func partitionMessages(messages []provider.Message) messagePartition {
	start := 0
	if len(messages) > 0 && messages[0].Role == provider.RoleSystem && isSystemPromptHead(messages[0].Content) {
		start++
	}
	// The order mirrors RunAgent's assembly: persona head, skill index, repo
	// instructions, then the per-run workspace directive.
	for start < len(messages) && isSkillIndexMessage(messages[start]) {
		start++
	}
	for start < len(messages) && isRepoInstructionMessage(messages[start]) {
		start++
	}
	for start < len(messages) && isWorkspaceMessage(messages[start]) {
		start++
	}

	return messagePartition{StableHead: messages[:start], persisted: messages[start:]}
}

func isTransientMessage(message provider.Message) bool {
	return message.Role == provider.RoleUser && skillindex.HasActivation(message.Content)
}

// activeSkillNames returns the names of skills whose activation blocks appear in
// messages, in order. Activity is derived from the conversation, not stored:
// compaction drops a body and the skill becomes activatable again, and a new
// session starts with none.
func activeSkillNames(messages []provider.Message) []string {
	var names []string
	for _, m := range messages {
		if skillindex.HasActivation(m.Content) {
			names = append(names, skillindex.ActivationNames(m.Content)...)
		}
	}
	return names
}

func (p messagePartition) PersistedHistory() []provider.Message {
	return append([]provider.Message(nil), p.persisted...)
}
