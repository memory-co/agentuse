package main

import (
	"fmt"
	"os/exec"
	"regexp"
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
func contextConversationReadNotification(instructions string) ([]byte, error) {
	match := regexp.MustCompile(`context conversation read ([a-z0-9-]+) --round ([0-9]+)`).FindStringSubmatch(instructions)
	if len(match) != 3 {
		return nil, fmt.Errorf("missing child round reference")
	}
	return contextCommand("conversation", "read", match[1], "--round", match[2])
}
