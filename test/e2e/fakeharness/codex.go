package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// codexAppServer stays in a single active turn until turn/steer is accepted.
func codexAppServer() int {
	dec := json.NewDecoder(os.Stdin)
	thread := fmt.Sprintf("codex-test-%d", time.Now().UnixNano())
	turn := "turn-1"
	for {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				ThreadID       string `json:"threadId"`
				ExpectedTurnID string `json:"expectedTurnId"`
				Input          []struct {
					Text string `json:"text"`
				} `json:"input"`
			} `json:"params"`
		}
		if dec.Decode(&req) != nil {
			return 0
		}
		ack := func(result any) { emit(map[string]any{"id": req.ID, "result": result}) }
		switch req.Method {
		case "initialize":
			ack(map[string]any{})
		case "thread/start":
			ack(map[string]any{"thread": map[string]string{"id": thread}})
		case "turn/start":
			ack(map[string]any{"turn": map[string]string{"id": turn, "status": "inProgress"}})
			emit(map[string]any{"method": "turn/started", "params": map[string]any{"threadId": thread, "turn": map[string]string{"id": turn, "status": "inProgress"}}})
			emit(map[string]any{"method": "item/started", "params": map[string]any{"threadId": thread, "turnId": turn, "item": map[string]string{"type": "commandExecution", "command": "sleep 12"}}})
		case "turn/steer":
			if req.Params.ExpectedTurnID != turn || req.Params.ThreadID != thread {
				emit(map[string]any{"id": req.ID, "error": map[string]any{"code": -32000, "message": "wrong turn"}})
				continue
			}
			ack(map[string]string{"turnId": turn})
			emit(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": thread, "turnId": turn, "item": map[string]string{"type": "agentMessage", "text": req.Params.Input[0].Text}}})
			emit(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": thread, "turn": map[string]string{"id": turn, "status": "completed"}}})
		case "turn/interrupt":
			ack(map[string]any{})
			emit(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": thread, "turn": map[string]string{"id": turn, "status": "interrupted"}}})
		}
	}
}
