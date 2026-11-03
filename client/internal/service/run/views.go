package run

import (
	"encoding/json"

	sdk "github.com/memory-co/agentuse/sdk/golang"
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

func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
