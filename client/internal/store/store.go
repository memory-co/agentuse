// Package store keeps the client filesystem and parent-scoped native views.
package store

import (
	"context"
	"encoding/json"
	sdk "github.com/memory-co/agentuse/sdk/golang"
	"os"
	"sync"
)

type Event struct {
	Kind    string          `json:"kind"`
	At      int64           `json:"at"`
	Payload json.RawMessage `json:"payload"`
}
type Filter struct {
	Endpoint, Conversation, Session string
	Agent, Status                   string
	After, Before                   int64
	Limit, Offset                   int
}
type Page struct {
	Runs       []Run  `json:"items"`
	Total      int    `json:"total"`
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
	NextCursor string `json:"next_cursor,omitempty"`
}
type Statistic struct {
	Key       string    `json:"key"`
	Runs      int       `json:"runs"`
	UsageRuns int       `json:"usage_runs"`
	Usage     sdk.Usage `json:"usage"`
	CostUSD   float64   `json:"cost_usd"`
}
type Reader interface {
	List(context.Context, Filter) (Page, error)
	Stats(context.Context, string, Filter) ([]Statistic, error)
	Conversations(context.Context, string) ([]Conversation, error)
	Sessions(context.Context, string, string) ([]Session, error)
	Session(context.Context, string, string, string) (Session, error)
	Detail(context.Context, string, string, string, string) (Run, error)
}
type Ledger struct {
	reserved    map[string]int
	Root        string
	mu          sync.Mutex
	serverPaths map[string]string
}

func Open(root string) (*Ledger, error) {
	p, e := SafePath(root)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(p, 0700); e != nil {
		return nil, e
	}
	return &Ledger{Root: p, serverPaths: map[string]string{}}, nil
}
func (l *Ledger) Reader() Reader { return &readStore{l} }
func (l *Ledger) Close() error   { return nil }

type readStore struct{ l *Ledger }

func (l *Ledger) Reconcile(ctx context.Context) error {
	if err := (&writeStore{l: l}).Reconcile(ctx); err != nil {
		return err
	}
	return l.rebuildNativeIndexes(ctx)
}
