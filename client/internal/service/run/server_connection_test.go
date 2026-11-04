package run

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/internal/protocol"
)

func TestRemoteOutboxWaitsForCommitAckAndReplaysSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	files, err := openFileServices(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	metadata, _, err := files.Sessions.Acquire(ctx, "https---example.com", "conversation", "agent", "custom", "v1", "agent-version")
	if err != nil {
		t.Fatal(err)
	}
	connection, err := newConnection(Config{Server: "http://example.com", Key: "test", EndpointID: metadata.EndpointID, RunService: files}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := connection.sessionDir(metadata)
	line := []byte(`{"type":"accepted","run_id":"550e8400-e29b-41d4-a716-446655440000","input_id":"input","text":"hello"}` + "\n")
	if err = os.WriteFile(filepath.Join(dir, "events.jsonl"), append(line, []byte(`{"type":`)...), 0600); err != nil {
		t.Fatal(err)
	}
	if err = fileutil.WriteJSON(filepath.Join(dir, ".server", "stream.json"), streamReceipt{}); err != nil {
		t.Fatal(err)
	}
	receipt := promptReceipt{Prompt: protocol.Prompt{ID: 7, ConversationID: metadata.ConversationID}, State: "accepted", SessionID: metadata.ID, RunID: "550e8400-e29b-41d4-a716-446655440000", InputID: "input"}
	if err = fileutil.WriteJSON(connection.receiptPath(metadata, 7), receipt); err != nil {
		t.Fatal(err)
	}
	received := make(chan protocol.Message, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := websocket.Accept(w, r, nil)
		if e != nil {
			return
		}
		defer c.CloseNow()
		for {
			var m protocol.Message
			if wsjson.Read(ctx, c, &m) != nil {
				return
			}
			received <- m
		}
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	connection.conn = conn
	if err = connection.flushEvents(ctx); err != nil {
		t.Fatal(err)
	}
	first := <-received
	if first.ClientEvent == nil || first.ClientEvent.PromptID != 7 || first.ClientEvent.Seq != int64(len(line)) {
		t.Fatalf("bad envelope %+v", first)
	}
	if string(first.ClientEvent.Event) != strings.TrimSpace(string(line)) {
		t.Fatal("source event translated")
	}
	if err = connection.ackEvent(metadata.ID, int64(len(line))+1); err == nil {
		t.Fatal("foreign ACK accepted")
	}
	// Lost ACK/reconnection: only the in-memory send window is discarded.
	connection.streamPending = map[string]int64{}
	if err = connection.flushEvents(ctx); err != nil {
		t.Fatal(err)
	}
	second := <-received
	if string(second.ClientEvent.Event) != string(first.ClientEvent.Event) || second.ClientEvent.Seq != first.ClientEvent.Seq {
		t.Fatal("replay changed identity")
	}
	if err = connection.ackEvent(metadata.ID, first.ClientEvent.Seq); err != nil {
		t.Fatal(err)
	}
	if err = connection.flushEvents(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
		t.Fatal("partial tail or ACKed event sent")
	default:
	}
	var cursor streamReceipt
	data, _ := os.ReadFile(filepath.Join(dir, ".server", "stream.json"))
	if json.Unmarshal(data, &cursor) != nil || cursor.Offset != int64(len(line)) {
		t.Fatal("ACK not durable")
	}
}

func TestRecoverRemoteReceiptUsesOnlyCompleteAcceptedRecords(t *testing.T) {
	for _, tc := range []struct {
		name, source, state string
		ambiguous           bool
	}{
		{"accepted", `{"type":"accepted","run_id":"550e8400-e29b-41d4-a716-446655440000","input_id":"one","text":"hello"}` + "\n" + `{"type":`, "accepted", false},
		{"unaccepted", `{"type":`, "failed", false},
		{"ambiguous", strings.Repeat(`{"type":"accepted","run_id":"550e8400-e29b-41d4-a716-446655440000","input_id":"one","text":"hello"}`+"\n", 2), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, _ := openFileServices(t.TempDir())
			metadata, _, err := files.Sessions.Acquire(context.Background(), "https---example.com", "conversation", "agent", "custom", "v", "agent-version")
			if err != nil {
				t.Fatal(err)
			}
			d, _ := newConnection(Config{Server: "http://example.com", Key: "test", EndpointID: metadata.EndpointID, RunService: files}, nil)
			dir := d.sessionDir(metadata)
			path := d.receiptPath(metadata, 1)
			if err = fileutil.WriteJSON(path, promptReceipt{Prompt: protocol.Prompt{ID: 1, Content: "hello"}, SessionID: metadata.ID, State: "submitting"}); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			err = files.recoverRemoteReceipts(context.Background())
			if tc.ambiguous {
				if err == nil {
					t.Fatal("ambiguous input guessed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			var receipt promptReceipt
			if json.Unmarshal(data, &receipt) != nil || receipt.State != tc.state {
				t.Fatalf("bad recovery %s", data)
			}
			if receipt.State == "failed" {
				pinned, e := files.pendingConversation(context.Background(), metadata.EndpointID, metadata.ConversationID)
				if e != nil || !pinned {
					t.Fatal("unconfirmed input failure can be reclaimed", e)
				}
				receipt.Reported = true
				if e = fileutil.WriteJSON(path, receipt); e != nil {
					t.Fatal(e)
				}
				pinned, e = files.pendingConversation(context.Background(), metadata.EndpointID, metadata.ConversationID)
				if e != nil || pinned {
					t.Fatal("confirmed input failure still pins directory", e)
				}
			}
			after, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
			if string(after) != tc.source {
				t.Fatal("recovery rewrote execution facts")
			}
		})
	}
}
