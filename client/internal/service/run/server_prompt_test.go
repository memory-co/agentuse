package run

import (
	"encoding/json"
	"github.com/memory-co/agentuse/internal/protocol"
	"strings"
	"testing"
)

func TestServerCredentialsDoNotChooseInstructions(t *testing.T) {
	input := protocol.Dispatch{AgentID: "agent", Input: json.RawMessage(`{"prompt":"task"}`), Instructions: "server-owned instructions"}
	before, err := serverPrompt(input, "")
	if err != nil {
		t.Fatal(err)
	}
	input.WorkToken = "opaque-credential"
	after, err := serverPrompt(input, "")
	if err != nil || before != after || strings.Contains(after, input.WorkToken) {
		t.Fatal("credential changed instructions", err)
	}
}
