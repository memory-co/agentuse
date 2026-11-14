package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

// Execute the bare context command from the actual native run environment.
func contextCommand(args ...string) ([]byte, error) {
	b, err := exec.Command("context", args...).CombinedOutput()
	if err != nil {
		return b, fmt.Errorf("context %v: %w: %s", args, err, b)
	}
	return b, nil
}
func contextConversationSpawn(agent, prompt string) ([]byte, error) {
	if b, err := contextCommand("conversation", "agents"); err != nil {
		return b, err
	}
	b, err := contextCommand("conversation", "create", "--title", "Child execution", "--mode", "single", "--agents", agent, prompt)
	if err != nil {
		return b, err
	}
	var created struct {
		Conversation struct {
			ID string `json:"id"`
		} `json:"conversation"`
		Round struct {
			Seq int64 `json:"seq"`
		} `json:"round"`
	}
	if err = json.Unmarshal(b, &created); err != nil {
		return b, err
	}
	if b, err = contextCommand("conversation", "list"); err != nil {
		return b, err
	}
	if b, err = contextCommand("conversation", "wait", created.Conversation.ID, "--round", strconv.FormatInt(created.Round.Seq, 10), "--timeout", "10s"); err != nil {
		return b, err
	}
	return contextCommand("conversation", "read", created.Conversation.ID, "--round", strconv.FormatInt(created.Round.Seq, 10))
}
