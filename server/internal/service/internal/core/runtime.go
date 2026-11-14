package core

import (
	"database/sql"
	"log"
	"time"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/store"
)

type Notifier interface {
	KickAll()
	KickUser(string)
	PushRun(string, protocol.Message)
	Disconnect(string)
}

type Runtime struct {
	BrowserChanges BrowserChanges
	Log            *log.Logger
	Repository     *store.Store
	DB             *sql.DB
	Now            func() time.Time
	Notifier       Notifier
}

func (s *Runtime) Unix() int64 { return s.Now().Unix() }

type DiscardNotifier struct{}

func (DiscardNotifier) KickAll()                         {}
func (DiscardNotifier) KickUser(string)                  {}
func (DiscardNotifier) PushRun(string, protocol.Message) {}

func (DiscardNotifier) Disconnect(string) {}

func NewRuntime(st *store.Store) *Runtime {
	return &Runtime{DB: st.Connection(), Repository: st, Now: time.Now, Log: log.Default(), Notifier: DiscardNotifier{}}
}
