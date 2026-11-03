// Package protocol defines server business input delivery and client event sync.
// Native execution protocols and adapter configuration never cross this boundary.
package protocol

import (
	"github.com/memory-co/agentuse/internal/attachment"
)

const (
	RegisterPath = "/api/daemon/register"
	WSPath       = "/api/daemon/ws"
	MePath       = "/api/daemon/me"
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

const (
	TypeHello     = "hello"
	TypeWelcome   = "welcome"
	TypeClaim     = "claim"
	TypeNoTask    = "no_task"
	TypeReady     = "ready"
	TypeEvent     = "event"
	TypeEventAck  = "event_ack"
	TypeHeartbeat = "heartbeat"
	TypeCancel    = "cancel"
	TypeError     = "error"
)

type Harness struct {
	Name     string `json:"name"`
	Version  string `json:"version,omitempty"`
	Revision string `json:"revision,omitempty"`
}
type Message struct {
	Type          string          `json:"type"`
	Protocol      string          `json:"protocol,omitempty"`
	ClaimID       string          `json:"claim_id,omitempty"`
	FreeSessions  int             `json:"free_sessions,omitempty"`
	ActiveStreams []StreamRef     `json:"active_streams,omitempty"`
	Prompt        *Prompt         `json:"prompt,omitempty"`
	PromptID      int64           `json:"prompt_id,omitempty"`
	ClientEvent   *ClientEvent    `json:"client_event,omitempty"`
	SessionID     string          `json:"session_id,omitempty"`
	AckSeq        int64           `json:"ack_seq,omitempty"`
	Control       *SessionControl `json:"control,omitempty"`
	Harnesses     []Harness       `json:"harnesses,omitempty"`
	Capabilities  []string        `json:"capabilities,omitempty"`
	Version       string          `json:"version,omitempty"`
	DaemonID      string          `json:"daemon_id,omitempty"`
	Name          string          `json:"name,omitempty"`
	Error         string          `json:"error,omitempty"`
}
type PromptInput struct {
	Prompt string `json:"prompt"`
}
type LocalAttachment struct {
	attachment.Attachment
	Path string `json:"path"`
}
