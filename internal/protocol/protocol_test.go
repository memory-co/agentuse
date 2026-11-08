package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExecutionMessagesPreservePublicEventsAndOpaqueAnswers(t *testing.T) {
	event := json.RawMessage(`{"type":"result","run_id":"550e8400-e29b-41d4-a716-446655440000","input_ids":["input"],"status":"completed","output":"中文","usage":{"input_tokens":7}}`)
	messages := []Message{
		DeliveryFrame(ConversationMessage{ConversationID: "conversation", MessageID: "message", AgentID: "agent", AgentVersionID: "version", Content: "input", Attachments: nil}),
		EventFrame(ClientEvent{SessionID: "client-session", Seq: 99, Event: event}),
		{Type: TypeAnswer, ID: "a:control", Control: &SessionControl{SessionID: "s1", RunID: "r1", RequestID: "request", Answer: json.RawMessage(`{"q":["one","two"]}`)}},
	}
	for _, message := range messages {
		data, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		for _, foreign := range []string{"attempt_id", "lease_until", "argv", "approvalPolicy", "sandbox", "prompt_id", "task_id", "client_event", "ack_seq", "\"seq\""} {
			if strings.Contains(string(data), foreign) {
				t.Fatal("legacy/native detail leaked", foreign)
			}
		}
		var roundtrip Message
		if err = json.Unmarshal(data, &roundtrip); err != nil {
			t.Fatal(err)
		}
		if roundtrip.Type != message.Type || roundtrip.ID != message.ID {
			t.Fatal(roundtrip)
		}
	}
	raw, _ := json.Marshal(messages[1])
	var decoded Message
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded.ClientEvent.Event) != string(event) {
		t.Fatal("public event was translated")
	}
	if decoded.ClientEvent.Seq != 99 {
		t.Fatal("event identity lost")
	}
}
func TestRejectLegacyAndConflictingMessageIdentities(t *testing.T) {
	for _, raw := range []string{`{"type":"hello","id":"old"}`, `{"type":"message","id":"m:other:a","conversation_id":"c","message_id":"m","agent_id":"a","agent_version_id":"v","content":"hello"}`, `{"type":"event","id":"e:s:0","session_id":"s","event":{}}`, `{"type":"event","id":"e:other:2","session_id":"s","event":{}}`} {
		var m Message
		if json.Unmarshal([]byte(raw), &m) == nil {
			t.Fatal("invalid frame accepted", raw)
		}
	}
}
