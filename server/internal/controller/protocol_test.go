package controller

import (
	"context"
	"encoding/json"
	"github.com/memory-co/agentuse/server/internal/hub"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func TestMessageProtocolPushAcceptanceAndReplay(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	transport := hub.New(f.e.st, nil)
	f.e.st.SetNotifier(transport)
	endpoint := httptest.NewServer(transport)
	defer endpoint.Close()
	url := "ws" + strings.TrimPrefix(endpoint.URL, "http") + protocol.WSPath
	// Authentication and incompatible protocols are rejected before upgrading.
	_, response, err := websocket.Dial(ctx, url, nil)
	if err == nil || response == nil || response.StatusCode != 401 {
		t.Fatal("unauthenticated upgrade accepted", response, err)
	}
	_, response, err = websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.key}}})
	if err == nil || response == nil || response.StatusCode != 426 {
		t.Fatal("old protocol accepted", response, err)
	}
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.key}, protocol.ProtocolHeader: []string{protocol.ExecutionProtocol}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	send := func(m protocol.Message) {
		t.Helper()
		if e := wsjson.Write(ctx, conn, m); e != nil {
			t.Fatal(e)
		}
	}
	read := func() protocol.Message {
		t.Helper()
		var m protocol.Message
		if e := wsjson.Read(ctx, conn, &m); e != nil {
			t.Fatal(e)
		}
		return m
	}
	send(protocol.Message{Type: protocol.TypeHeartbeat, ID: "h:first", FreeSessions: 1, Harnesses: []protocol.Harness{{Name: "claude", Revision: "revision"}}})
	ack := read()
	if ack.Type != protocol.TypeAck || ack.ID != "h:first" || ack.Status != protocol.AckOK {
		t.Fatal(ack)
	}
	client := agentuse.New(f.e.ts.URL, f.token)
	first, e := client.SendMessage(ctx, "", f.session.ID, agentuse.MessageRequest{Prompt: "first", ToAgentIDs: []string{f.agent}})
	if e != nil {
		t.Fatal(e)
	}
	second, e := client.SendMessage(ctx, "", f.session.ID, agentuse.MessageRequest{Prompt: "second", ToAgentIDs: []string{f.agent}})
	if e != nil {
		t.Fatal(e)
	}
	delivery := read()
	if delivery.Type != protocol.TypeMessage || delivery.Delivery.MessageID != first.Message.ID || delivery.Delivery.Content != "first" || delivery.ID != delivery.Delivery.Key() {
		t.Fatal("no direct message delivery", delivery)
	}
	frame, _ := json.Marshal(delivery)
	for _, field := range []string{"prompt_id", "task_id", "\"skills\"", "\"config\"", "\"model\"", "\"prompt\":"} {
		if strings.Contains(string(frame), field) {
			t.Fatal("extra task/config abstraction", field, string(frame))
		}
	}
	// Configuration is authorized before any accepted/run exists.
	device := agentuse.New(f.e.ts.URL, f.key)
	version, e := device.GetAgentVersion(ctx, "", f.agent, delivery.Delivery.AgentVersionID)
	if e != nil || version.AgentID != f.agent {
		t.Fatal(version, e)
	}
	detail, e := client.GetSession(ctx, "", f.session.ID)
	if e != nil || len(detail.Runs) != 0 {
		t.Fatal("run created before acceptance", detail, e)
	}
	run := uuid.NewString()
	public, _ := json.Marshal(map[string]any{"type": "accepted", "run_id": run, "input_id": "in-1", "text": delivery.Delivery.Text()})
	accepted := protocol.EventFrame(protocol.ClientEvent{SessionID: "native-session", Seq: 10, MessageID: first.Message.ID, AgentID: f.agent, Event: public})
	send(accepted)
	// The next message can be pushed once acceptance is committed, even though
	// no additional session capacity or claim request was sent.
	ack = read()
	if ack.ID != accepted.ID || ack.Status != protocol.AckOK {
		t.Fatal(ack)
	}
	next := read()
	if next.Type != protocol.TypeMessage || next.Delivery.MessageID != second.Message.ID {
		t.Fatal(next)
	}
	// Replaying the same source under another conversation message must fail.
	conflict := accepted
	copyEvent := *accepted.ClientEvent
	copyEvent.MessageID = second.Message.ID
	conflict.ClientEvent = &copyEvent
	send(conflict)
	ack = read()
	if ack.ID != accepted.ID || ack.Status != protocol.AckError {
		t.Fatal("source rebound", ack)
	}
	send(accepted)
	ack = read()
	if ack.ID != accepted.ID || ack.Status != protocol.AckOK {
		t.Fatal("valid replay rejected", ack)
	}
	result, _ := json.Marshal(map[string]any{"type": "result", "run_id": run, "status": "completed", "input_ids": []string{"in-1"}, "output": "done"})
	done := protocol.EventFrame(protocol.ClientEvent{SessionID: "native-session", Seq: 20, Event: result})
	for i := 0; i < 2; i++ {
		send(done)
		ack = read()
		if ack.ID != done.ID || ack.Status != protocol.AckOK {
			t.Fatal(ack)
		}
	}
	send(protocol.Message{Type: protocol.TypeAck, ID: next.ID, Status: protocol.AckUnknown, Error: "send may have been accepted"})
	third, e := client.SendMessage(ctx, "", f.session.ID, agentuse.MessageRequest{Prompt: "third", ToAgentIDs: []string{f.agent}})
	if e != nil {
		t.Fatal(e)
	}
	detail, e = client.GetSession(ctx, "", f.session.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].ID != run || len(detail.Messages) != 4 {
		t.Fatal("replay duplicated execution or reply", detail)
	}
	for _, target := range detail.Deliveries {
		if target.MessageID == third.Message.ID && target.Status != "pending" {
			t.Fatal("unknown input did not block later send", target)
		}
	}
}
