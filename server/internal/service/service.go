// Package service coordinates server-side business operations and notifications.
package service

import (
	"database/sql"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/store"
	"log"
	"sync"
	"time"
)

type Notifier interface {
	KickAll()
	KickUser(string)
	PushRun(string, protocol.Message)
	PushRunSteers([]store.Delivery)
	Disconnect(string)
}

type Service struct {
	attachments  AttachmentConfig
	attachmentMu sync.RWMutex
	log          *log.Logger
	repository   *store.Store
	db           *sql.DB
	now          func() time.Time
	Notifier     Notifier
}

func New(st *store.Store) *Service {
	return &Service{log: log.Default(), repository: st, db: st.Connection(), now: time.Now, Notifier: discardNotifier{}}
}
func Open(path string) (*Service, error) {
	st, e := store.Open(path)
	if e != nil {
		return nil, e
	}
	return New(st), nil
}
func (s *Service) Close() error { return s.repository.Close() }
func (s *Service) unix() int64  { return s.now().Unix() }
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
