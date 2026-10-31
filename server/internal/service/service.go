// Package service coordinates server-side business operations and notifications.
package service

import (
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/store"
)

type Notifier interface {
	KickAll()
	KickUser(string)
	PushRun(string, protocol.Message)
	PushRunSteers([]store.Delivery)
	Disconnect(string)
}

type Service struct {
	*store.Store
	Notifier Notifier
}

func New(st *store.Store) *Service { return &Service{Store: st, Notifier: discardNotifier{}} }
func (s *Service) SetNotifier(n Notifier) {
	if n != nil {
		s.Notifier = n
	}
}

type discardNotifier struct{}

func (discardNotifier) KickAll()                         {}
func (discardNotifier) KickUser(string)                  {}
func (discardNotifier) PushRun(string, protocol.Message) {}
func (discardNotifier) PushRunSteers([]store.Delivery)   {}
func (discardNotifier) Disconnect(string)                {}
