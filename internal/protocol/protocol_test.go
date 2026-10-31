package protocol

import (
	"encoding/json"
	"testing"
)

// 线上格式是和 daemon 的约定，字段名、omitempty 改了旧版本 daemon 就对不上了。
func TestMessageJSON(t *testing.T) {
	for _, c := range []struct {
		msg  Message
		want string
	}{
		{Message{Type: TypeReady}, `{"type":"ready"}`},
		{Message{Type: TypeHeartbeat, MessageID: 7}, `{"type":"heartbeat","message_id":7}`},
		{Message{Type: TypeHello, Version: "v1", Harnesses: []Harness{{Name: "claude", Version: "2"}, {Name: "x"}}},
			`{"type":"hello","harnesses":[{"name":"claude","version":"2"},{"name":"x"}],"version":"v1"}`},
		{Message{Type: TypeEvent, MessageID: 1, Kind: "done", Payload: json.RawMessage(`{"ok":true}`)},
			`{"type":"event","message_id":1,"kind":"done","payload":{"ok":true}}`},
		{Message{Type: TypeRun, Run: &Dispatch{ID: 1, SessionID: "ss_1", Harness: "claude", Input: json.RawMessage(`{"prompt":"hi"}`)}},
			`{"run":{"id":1,"session_id":"ss_1","harness":"claude","input":{"prompt":"hi"}},"type":"run"}`},
	} {
		b, err := json.Marshal(c.msg)
		if err != nil || string(b) != c.want {
			t.Errorf("got %s\nwant %s", b, c.want)
		}
		var back Message
		if err := json.Unmarshal(b, &back); err != nil || back.Type != c.msg.Type {
			t.Errorf("round trip %s: %v", b, err)
		}
	}
}

func TestHarnessInputJSON(t *testing.T) {
	b, _ := json.Marshal(HarnessInput{SessionID: "ss_1", Input: json.RawMessage(`{"prompt":"hi"}`)})
	if string(b) != `{"session_id":"ss_1","input":{"prompt":"hi"}}` {
		t.Fatalf("%s", b)
	}
}
