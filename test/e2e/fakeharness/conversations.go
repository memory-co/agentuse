package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
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
	return contextCommand("conversation", "create", "--title", "Child execution", "--mode", "single", "--agents", agent, prompt)
}
func contextConversationReviewNotification(instructions string) ([]byte, error) {
	match := regexp.MustCompile(`context conversation read ([a-z0-9-]+) --round ([0-9]+)`).FindStringSubmatch(instructions)
	if len(match) != 3 {
		return nil, fmt.Errorf("missing child round reference")
	}
	body, err := contextCommand("conversation", "read", match[1], "--round", match[2])
	if err != nil {
		return nil, err
	}
	var detail agentuse.ConversationDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return nil, err
	}
	if len(detail.Runs) == 1 && detail.Runs[0].Status == "completed" && detail.Runs[0].Output == "Child execution complete." {
		if _, err := contextCommand("conversation", "resolve", match[1], "--round", match[2]); err != nil {
			return nil, err
		}
	}
	return body, nil
}
