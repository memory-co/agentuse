// Package protocol transports conversation messages and unchanged harnessctl events.
package protocol

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/attachment"
)

const (
	RegisterPath      = "/api/daemon/register"
	WSPath            = "/api/client/ws"
	MePath            = "/api/daemon/me"
	ProtocolHeader    = "AgentUse-Protocol"
	ExecutionProtocol = "agentuse-client/2"
	TypeHeartbeat     = "heartbeat"
	TypeMessage       = "message"
	TypeEvent         = "event"
	TypeAnswer        = "answer"
	TypeCancel        = "cancel"
	TypeAck           = "ack"
	AckOK             = "ok"
	AckError          = "error"
	AckUnknown        = "unknown"
)

type RegisterRequest struct {
	Token    string `json:"token"`
	Hostname string `json:"hostname"`
}
type RegisterResponse struct {
	DaemonID string `json:"daemon_id"`
	Name     string `json:"name"`
	Key      string `json:"key"`
}
type MeResponse struct {
	DaemonID string `json:"daemon_id"`
	Name     string `json:"name"`
	UserID   string `json:"user_id"`
	Status   string `json:"status"`
}
type Harness struct {
	Name     string `json:"name"`
	Version  string `json:"version,omitempty"`
	Revision string `json:"revision,omitempty"`
}

// Message is a flat WebSocket envelope. The pointers are typed in-memory views,
// not additional wire envelopes or independent delivery identities.
type Message struct {
	Type          string               `json:"type"`
	ID            string               `json:"id"`
	Status        string               `json:"status,omitempty"`
	Error         string               `json:"error,omitempty"`
	FreeSessions  int                  `json:"free_sessions,omitempty"`
	ActiveStreams []StreamRef          `json:"active,omitempty"`
	Harnesses     []Harness            `json:"harnesses,omitempty"`
	Delivery      *ConversationMessage `json:"-"`
	ClientEvent   *ClientEvent         `json:"-"`
	Control       *SessionControl      `json:"-"`
}
type messageFields Message

func (m Message) MarshalJSON() ([]byte, error) {
	base, err := json.Marshal(messageFields(m))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(base, &fields); err != nil {
		return nil, err
	}
	var payload any
	switch m.Type {
	case TypeMessage:
		payload = m.Delivery
	case TypeEvent:
		payload = m.ClientEvent
	case TypeAnswer, TypeCancel:
		payload = m.Control
	}
	if payload != nil {
		raw, e := json.Marshal(payload)
		if e != nil {
			return nil, e
		}
		var extra map[string]json.RawMessage
		if e = json.Unmarshal(raw, &extra); e != nil {
			return nil, e
		}
		for k, v := range extra {
			fields[k] = v
		}
	}
	return json.Marshal(fields)
}
func (m *Message) UnmarshalJSON(data []byte) error {
	var fields messageFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*m = Message(fields)
	if m.ID == "" {
		return fmt.Errorf("message id required")
	}
	switch m.Type {
	case TypeHeartbeat:
		if m.FreeSessions < 0 {
			return fmt.Errorf("negative capacity")
		}
	case TypeAck:
		if m.Status != AckOK && m.Status != AckError && m.Status != AckUnknown {
			return fmt.Errorf("invalid ACK status")
		}
	case TypeMessage:
		m.Delivery = &ConversationMessage{}
		if err := json.Unmarshal(data, m.Delivery); err != nil {
			return err
		}
		if !m.Delivery.Valid() || m.ID != m.Delivery.Key() {
			return fmt.Errorf("invalid conversation message")
		}
	case TypeEvent:
		m.ClientEvent = &ClientEvent{}
		if err := json.Unmarshal(data, m.ClientEvent); err != nil {
			return err
		}
		session, pos, err := ParseEventID(m.ID)
		if err != nil || session != m.ClientEvent.SessionID {
			return fmt.Errorf("invalid event id")
		}
		m.ClientEvent.Seq = pos
	case TypeAnswer, TypeCancel:
		m.Control = &SessionControl{}
		if err := json.Unmarshal(data, m.Control); err != nil {
			return err
		}
		m.Control.ID = m.ID
		prefix := "c:"
		if m.Type == TypeAnswer {
			prefix = "a:"
		}
		if !strings.HasPrefix(m.ID, prefix) || m.Control.SessionID == "" || m.Control.RunID == "" {
			return fmt.Errorf("invalid control")
		}
	default:
		return fmt.Errorf("unknown protocol message %q", m.Type)
	}
	return nil
}
func DeliveryFrame(m ConversationMessage) Message {
	return Message{Type: TypeMessage, ID: m.Key(), Delivery: &m}
}
func EventFrame(e ClientEvent) Message {
	return Message{Type: TypeEvent, ID: EventID(e.SessionID, e.Seq), ClientEvent: &e}
}
func EventID(session string, pos int64) string {
	return "e:" + session + ":" + strconv.FormatInt(pos, 10)
}
func ParseEventID(id string) (string, int64, error) {
	if !strings.HasPrefix(id, "e:") {
		return "", 0, fmt.Errorf("invalid event id")
	}
	split := strings.LastIndexByte(id, ':')
	if split <= 2 {
		return "", 0, fmt.Errorf("invalid event id")
	}
	pos, err := strconv.ParseInt(id[split+1:], 10, 64)
	if err != nil || pos <= 0 || strconv.FormatInt(pos, 10) != id[split+1:] {
		return "", 0, fmt.Errorf("invalid event position")
	}
	return id[2:split], pos, nil
}

// LegacyMessageID provides a stable UUID when migrating the old integer IDs.
func LegacyMessageID(conversation string, id int64) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(conversation+"#message:"+strconv.FormatInt(id, 10))).String()
}

type PromptInput struct {
	Prompt string `json:"prompt"`
}
type LocalAttachment struct {
	attachment.Attachment
	Path string `json:"path"`
}

func ParseMessageID(id string) (string, string, error) {
	if !strings.HasPrefix(id, "m:") {
		return "", "", fmt.Errorf("invalid message id")
	}
	message, agent, ok := strings.Cut(strings.TrimPrefix(id, "m:"), ":")
	if !ok || message == "" || agent == "" {
		return "", "", fmt.Errorf("invalid message target")
	}
	return message, agent, nil
}
