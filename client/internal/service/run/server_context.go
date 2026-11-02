package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/store"
	"github.com/memory-co/agentuse/internal/protocol"
	"gopkg.in/yaml.v3"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func writeSessionHistory(m protocol.Dispatch, dir string) error {
	if m.RunID == "" {
		return nil
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	managed, err := openAttachmentDir(root, ".agentuse")
	if err != nil {
		return err
	}
	defer managed.Close()
	name := "session-history.json"
	if info, e := managed.Lstat(name); e == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("session history 必须是普通文件")
	}
	tmp := ".session-history-" + m.AttemptID
	f, err := managed.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer managed.Remove(tmp)
	defer f.Close()
	if _, err = f.Write(m.SessionMessages); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return renameAttachment(managed, tmp, name)
}

func (d *endpointConnection) loadRunHistory(ctx context.Context, m *protocol.Dispatch) error {
	if m.RunID == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(d.cfg.Server, "/")+"/api/sessions/"+url.PathEscape(m.SessionID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+d.cfg.Key)
	req.Header.Set("X-Agentuse-Run", m.RunID)
	req.Header.Set("X-Agentuse-Attempt", m.AttemptID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("session read: HTTP %d", resp.StatusCode)
	}
	var detail struct {
		Messages json.RawMessage `json:"messages"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return err
	}
	var ids []struct {
		ID int64 `json:"id"`
	}
	if err = json.Unmarshal(detail.Messages, &ids); err != nil {
		return err
	}
	m.SessionMessages = detail.Messages
	if len(ids) > 0 {
		m.ContextSeq = ids[len(ids)-1].ID
	}
	return nil
}

func (d *endpointConnection) prepareHistory(m *protocol.Dispatch, s store.Session, la *harness.Adapter, dir string) error {
	if s.ID == "" {
		la.HistoryPath = filepath.Join(dir, ".agentuse", "session-history.json")
		la.EventsPath = filepath.Join(filepath.Dir(dir), "sessions", la.Name, "events.jsonl")
		return writeSessionHistory(*m, dir)
	}
	var err error
	cursor, err := d.serverCursor(s)
	if err != nil {
		return err
	}
	var messages []map[string]json.RawMessage
	if err = json.Unmarshal(m.SessionMessages, &messages); err != nil {
		return err
	}
	filtered := []map[string]json.RawMessage{}
	for _, msg := range messages {
		var id int64
		_ = json.Unmarshal(msg["id"], &id)
		if cursor > 0 && id <= cursor {
			continue
		}
		task := false
		for _, taskID := range m.TaskMessageIDs {
			if id == taskID {
				task = true
			}
		}
		if task {
			continue
		}
		var author string
		_ = json.Unmarshal(msg["agent_key"], &author)
		if cursor > 0 && author == m.AgentID {
			continue
		}
		filtered = append(filtered, msg)
	}
	if len(filtered) == 0 {
		return nil
	}
	b, err := json.Marshal(filtered)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(b)
	path, err := store.SafePath(d.workdir, d.cfg.EndpointID, m.SessionID, "sessions", s.ID, "resources", hex.EncodeToString(digest[:])+".history.json")
	if err != nil {
		return err
	}
	if err = store.Atomic(path, b); err != nil {
		return err
	}
	la.HistoryPath = path
	// Path is outside the shared workspace; native tools receive it explicitly.
	return nil
}

func (d *endpointConnection) cursorPath(s store.Session) (string, error) {
	return store.SafePath(d.workdir, s.EndpointID, s.ConversationID, "sessions", s.ID, ".server", "context.json")
}
func (d *endpointConnection) serverCursor(s store.Session) (int64, error) {
	path, err := d.cursorPath(s)
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		legacy, err := store.SafePath(d.workdir, s.EndpointID, s.ConversationID, "sessions", s.ID, "session.yaml")
		if err != nil {
			return 0, err
		}
		data, err = os.ReadFile(legacy)
		if err != nil {
			return 0, err
		}
		var old struct {
			Seq int64 `yaml:"last_context_seq"`
		}
		err = yaml.Unmarshal(data, &old)
		return old.Seq, err
	}
	if err != nil {
		return 0, err
	}
	var cursor struct {
		Seq int64 `json:"message_seq"`
	}
	err = json.Unmarshal(data, &cursor)
	return cursor.Seq, err
}
func (d *endpointConnection) saveServerCursor(s store.Session, seq int64) error {
	path, err := d.cursorPath(s)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]int64{"message_seq": seq})
	return store.Atomic(path, data)
}
