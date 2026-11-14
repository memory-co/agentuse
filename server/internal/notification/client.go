package notification

import (
	"github.com/memory-co/agentuse/internal/protocol"
)

// Client is the transport contract configured by the service composition root.
// Each business service consumes only the methods it needs.
type Client interface {
	KickAll()
	KickUser(string)
	PushRun(string, protocol.Message)
	Disconnect(string)
}

type DiscardClient struct{}

func (DiscardClient) KickAll()                         {}
func (DiscardClient) KickUser(string)                  {}
func (DiscardClient) PushRun(string, protocol.Message) {}
func (DiscardClient) Disconnect(string)                {}
