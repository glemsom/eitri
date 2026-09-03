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

func partitionMessages(messages []provider.Message) messagePartition {
	start := 0
	if len(messages) > 0 && messages[0].Role == provider.RoleSystem && messages[0].Content == SystemPromptContent() {
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
