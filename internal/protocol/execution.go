package protocol

import (
	"encoding/json"
	"github.com/memory-co/agentuse/internal/attachment"
	"strings"
)

type SkillResource struct {
	SkillID        string `json:"skill_id"`
	SkillVersionID string `json:"skill_version_id"`
	Name           string `json:"name"`
	SHA256         string `json:"sha256"`
	SizeBytes      int64  `json:"size_bytes"`
	DownloadPath   string `json:"download_path"`
}

// ConversationMessage refers directly to one saved message and its target Agent.
// Agent configuration is read separately through the immutable version API.
type ConversationMessage struct {
	ConversationID string                  `json:"conversation_id"`
	MessageID      string                  `json:"message_id"`
	AgentID        string                  `json:"agent_id"`
	AgentVersionID string                  `json:"agent_version_id"`
	Content        string                  `json:"content"`
	Context        string                  `json:"context,omitempty"`
	Attachments    []attachment.Attachment `json:"attachments"`
}

func (m ConversationMessage) Key() string { return "m:" + m.MessageID + ":" + m.AgentID }
func (m ConversationMessage) Valid() bool {
	return m.ConversationID != "" && m.MessageID != "" && m.AgentID != "" && m.AgentVersionID != "" && strings.TrimSpace(m.Content) != ""
}
func (m ConversationMessage) Text() string {
	if m.Context == "" {
		return m.Content
	}
	return m.Context + "\n\n# Current message\n" + m.Content
}

type ClientEvent struct {
	AgentVersionID string          `json:"agent_version_id,omitempty"`
	ConversationID string          `json:"conversation_id,omitempty"`
	AgentID        string          `json:"agent_id,omitempty"`
	SessionID      string          `json:"session_id"`
	Harness        string          `json:"harness,omitempty"`
	Revision       string          `json:"revision,omitempty"`
	Seq            int64           `json:"-"`
	MessageID      string          `json:"message_id,omitempty"`
	Event          json.RawMessage `json:"event"`
}
type SessionControl struct {
	ID             string          `json:"-"`
	RunID          string          `json:"run_id"`
	ConversationID string          `json:"conversation_id,omitempty"`
	SessionID      string          `json:"session_id"`
	RequestID      string          `json:"request_id,omitempty"`
	Answer         json.RawMessage `json:"answer,omitempty"`
}
type StreamRef struct {
	AgentVersionID string `json:"agent_version_id"`
	ConversationID string `json:"conversation_id"`
	AgentID        string `json:"agent_id"`
}
