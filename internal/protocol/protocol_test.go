package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExecutionMessagesPreservePublicEventsAndOpaqueAnswers(t *testing.T) {
	event := json.RawMessage(`{"type":"result","run_id":"550e8400-e29b-41d4-a716-446655440000","input_ids":["input"],"status":"completed","output":"中文","usage":{"input_tokens":7}}`)
	messages := []Message{{Type: TypePrompt, Prompt: &Prompt{ID: 1, ConversationID: "conversation", AgentID: "agent", Harness: "client-label", Content: "input"}}, {Type: TypeEvent, ClientEvent: &ClientEvent{SessionID: "client-session", Seq: 99, Event: event}}, {Type: TypeReply, Control: &SessionControl{ID: "control", RequestID: "request", Answer: json.RawMessage(`{"q":["one","two"]}`)}}}
	for _, message := range messages {
		data, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		for _, foreign := range []string{"attempt_id", "lease_until", "argv", "approvalPolicy", "sandbox"} {
			if strings.Contains(string(data), foreign) {
				t.Fatal("native execution detail leaked", foreign)
			}
		}
		var roundtrip Message
		if err = json.Unmarshal(data, &roundtrip); err != nil {
			t.Fatal(err)
		}
		if roundtrip.Type != message.Type {
			t.Fatal(roundtrip)
		}
	}
	var decoded Message
	raw, _ := json.Marshal(messages[1])
	json.Unmarshal(raw, &decoded)
	var a, b any
	json.Unmarshal(event, &a)
	json.Unmarshal(decoded.ClientEvent.Event, &b)
	if strings.TrimSpace(string(decoded.ClientEvent.Event)) != string(event) {
		t.Fatal("public event was translated")
	}
}
