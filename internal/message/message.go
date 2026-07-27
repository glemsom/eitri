// Package message defines the EitriMessage type and conversion helpers.
// EitriMessage wraps litellm.Message with UI-only metadata (timestamps,
// components, quick replies). The session store uses this type internally
// while the public HistoryManager interface returns []llm.Message.
package message

import (
	"time"

	"github.com/voocel/litellm"
)

// ComponentData holds a rendered UI component attached to a message.
type ComponentData struct {
	Name string         `json:"name"`
	Data map[string]any `json:"data"`
}

// EitriMessage wraps litellm.Message with UI-only metadata.
type EitriMessage struct {
	litellm.Message
	CreatedAt    time.Time       `json:"created_at"`
	Components   []ComponentData `json:"components,omitempty"`
	QuickReplies []string        `json:"quick_replies,omitempty"`
}

// ToLitellm returns the embedded litellm.Message for transport.
func (m EitriMessage) ToLitellm() litellm.Message {
	return m.Message
}

// FromLitellm wraps a litellm.Message with zero-value UI fields.
func FromLitellm(msg litellm.Message) EitriMessage {
	return EitriMessage{Message: msg}
}
