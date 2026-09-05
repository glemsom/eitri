package engine

import (
	"strings"

	"github.com/glemsom/eitri/internal/provider"
)

type messagePartition struct {
	StableHead []provider.Message
	Transient  []provider.Message
	History    []provider.Message
	persisted  []provider.Message
}

// isSystemPromptHead reports whether content is the byte-stable persona head
// in either mode (default or unsandboxed), so prompt-head detection strips the
// right head in a yolo session.
func isSystemPromptHead(content string) bool {
	return content == SystemPromptContent() || content == SystemPromptYoloContent()
}

func partitionMessages(messages []provider.Message) messagePartition {
	start := 0
	if len(messages) > 0 && messages[0].Role == provider.RoleSystem && isSystemPromptHead(messages[0].Content) {
		start++
	}
	for start < len(messages) && isWorkspaceMessage(messages[start]) {
		start++
	}
	for start < len(messages) && isSkillIndexMessage(messages[start]) {
		start++
	}
	for start < len(messages) && isRepoInstructionMessage(messages[start]) {
		start++
	}

	p := messagePartition{StableHead: messages[:start], persisted: messages[start:]}
	for _, message := range p.persisted {
		if p.IsTransient(message) {
			p.Transient = append(p.Transient, message)
		} else {
			p.History = append(p.History, message)
		}
	}
	return p
}

func (messagePartition) IsTransient(message provider.Message) bool {
	return message.Role == provider.RoleUser && strings.Contains(message.Content, "<skill_content")
}

func (p messagePartition) PersistedHistory() []provider.Message {
	return append([]provider.Message(nil), p.persisted...)
}
