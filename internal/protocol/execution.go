package protocol

import "encoding/json"

// ExecutionProtocol transports business inputs and unchanged harnessctl events.
// A prompt identifies a server message delivery; only the client assigns run_id.
const ExecutionProtocol = "harnessctl-events-v1"
const TypePrompt = "prompt"
const TypePromptError = "prompt_error"
const TypePromptErrorAck = "prompt_error_ack"
const TypeReply = "reply"
const TypeControlAck = "control_ack"

type Prompt struct {
	Attachments    []LocalAttachment `json:"attachments,omitempty"`
	ID             int64             `json:"id"`
	ConversationID string            `json:"conversation_id"`
	MessageID      int64             `json:"message_id"`
	AgentID        string            `json:"agent_id"`
	Harness        string            `json:"harness"`
	Content        string            `json:"content"`
	Instructions   string            `json:"instructions,omitempty"`
	Context        json.RawMessage   `json:"context,omitempty"`
	ContextSeq     int64             `json:"context_seq,omitempty"`
	WorkID         string            `json:"work_id,omitempty"`
	WorkToken      string            `json:"work_token,omitempty"`
}

type ClientEvent struct {
	ConversationID string `json:"conversation_id"`
	AgentID        string `json:"agent_id"`
	SessionID      string `json:"session_id"`
	Harness        string `json:"harness"`
	Revision       string `json:"revision"`
	Seq            int64  `json:"seq"`
	PromptID       int64  `json:"prompt_id,omitempty"`
	// Event is the exact public JSONL object, without native adapter translation.
	Event json.RawMessage `json:"event"`
}

type SessionControl struct {
	RunID          string          `json:"run_id"`
	ID             string          `json:"id"`
	ConversationID string          `json:"conversation_id"`
	SessionID      string          `json:"session_id"`
	RequestID      string          `json:"request_id,omitempty"`
	Answer         json.RawMessage `json:"answer,omitempty"`
}

type StreamRef struct {
	ConversationID string `json:"conversation_id"`
	AgentID        string `json:"agent_id"`
}
