package run

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/session"
	sdk "github.com/memory-co/agentuse/sdk/golang"
)

const ContextInstructions = `# 当前执行的上下文工具

可以直接调用 context，无需提供 Server、conversation 或 session ID：
- context input：读取当前 run 已受理的输入，包括补充消息。
- context attachment list：列出当前输入附件和本次 run 上传的输出文件。
- context attachment read FILE_ID：校验并取得文件的本地绝对路径、名称和 MIME 类型；图片用你的原生看图工具读取该路径。
- context attachment upload FILE：上传输出文件到当前 conversation，自动关联本次 run；在回复中引用返回的 download_path。上传不会自动回复或结束执行。
- context --help：查看当前可用能力。
`

type contextManifest struct {
	Version     int             `json:"version"`
	Root        string          `json:"root"`
	ConfigPath  string          `json:"config_path"`
	Session     session.Session `json:"session"`
	AdapterPath string          `json:"adapter_path"`
}

// ExecutionContext is a view of one adapter-owned run, never another business ID.
// Credentials and storage locations stay private to the command implementation.
type ExecutionContext struct {
	EndpointID     string         `json:"endpoint_id"`
	ConversationID string         `json:"conversation_id"`
	SessionID      string         `json:"session_id"`
	RunID          string         `json:"run_id"`
	AgentID        string         `json:"agent_id"`
	Status         string         `json:"status"`
	Workspace      string         `json:"workspace"`
	Inputs         []ContextInput `json:"inputs"`
	path           string
	manifest       contextManifest
	files          []sdk.Attachment
}

type ContextInput struct {
	MessageID     string   `json:"message_id,omitempty"`
	InputID       string   `json:"input_id"`
	Text          string   `json:"text"`
	Context       string   `json:"context,omitempty"`
	AttachmentIDs []string `json:"attachment_ids"`
}
type ContextAttachment struct {
	ID             string `json:"id"`
	RelativePath   string `json:"relative_path"`
	Direction      string `json:"direction"`
	ConversationID string `json:"conversation_id"`
	RunID          string `json:"run_id,omitempty"`
	DownloadPath   string `json:"download_path"`
	Name           string `json:"name"`
	MIMEType       string `json:"mime_type"`
	SizeBytes      int64  `json:"size_bytes"`
	SHA256         string `json:"sha256"`
	LocalPath      string `json:"local_path,omitempty"`
}

func (s *Service) prepareContext(m session.Session, a *harness.Adapter) error {
	root, err := fileutil.SafePath(s.Sessions.Root)
	if err != nil {
		return err
	}
	path, err := fileutil.SafePath(root, m.EndpointID, m.ConversationID, "sessions", m.ID, "context.json")
	if err != nil {
		return err
	}
	config := s.Path
	if config != "" {
		config, err = filepath.Abs(config)
		if err != nil {
			return err
		}
	}
	manifest := contextManifest{Version: 1, Root: root, ConfigPath: config, Session: m, AdapterPath: a.Path}
	if err = fileutil.WriteJSON(path, manifest); err != nil {
		return err
	}
	bin, err := fileutil.SafePath(filepath.Dir(path), "bin")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(bin, 0700); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	entry := filepath.Join(bin, "context")
	if current, e := os.Readlink(entry); e != nil || current != exe {
		if e = os.Remove(entry); e != nil && !os.IsNotExist(e) {
			return e
		}
		if e = os.Symlink(exe, entry); e != nil {
			return e
		}
	}
	inheritedPath := os.Getenv("PATH")
	for _, value := range a.Env {
		if strings.HasPrefix(value, "PATH=") {
			inheritedPath = strings.TrimPrefix(value, "PATH=")
		}
	}
	a.Env = append(a.Env, "AGENTUSE_CONTEXT="+path, "AGENTUSE_CONTEXT_RUN=", "AGENTUSE_SESSION="+m.ID,
		"PATH="+bin+string(os.PathListSeparator)+inheritedPath)
	return nil
}

// OpenExecutionContext selects a declared run or asks the public harnessctl
// status which run is active. Queued accepted inputs never pick the run.
func OpenExecutionContext(ctx context.Context, path, runID string) (*ExecutionContext, error) {
	if path == "" {
		return nil, fmt.Errorf("未进入执行上下文；使用 agentuse context --path <context.json> --run <run UUID>，或在 Client 提供的运行环境中调用 context")
	}
	path, err := fileutil.SafePath(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest contextManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	m := manifest.Session
	expected, err := fileutil.SafePath(manifest.Root, m.EndpointID, m.ConversationID, "sessions", m.ID, "context.json")
	if err != nil {
		return nil, err
	}
	if manifest.Version != 1 || expected != path || m.AgentID == "" || m.Harness == "" || manifest.AdapterPath == "" {
		return nil, fmt.Errorf("执行上下文归属不匹配")
	}
	metadata := &session.Service{Root: manifest.Root}
	stored, err := metadata.Session(ctx, m.EndpointID, m.ConversationID, m.ID)
	if err != nil {
		return nil, err
	}
	if stored.AgentID != m.AgentID || stored.AgentVersionID != m.AgentVersionID || stored.Harness != m.Harness || stored.HarnessRevision != m.HarnessRevision {
		return nil, fmt.Errorf("执行上下文与 session 不匹配")
	}
	events, err := fileutil.SafePath(filepath.Dir(path), "events.jsonl")
	if err != nil {
		return nil, err
	}
	if runID == "" {
		state, err := (harness.Service{}).Status(ctx, harness.Adapter{Path: manifest.AdapterPath}, events)
		if err != nil {
			return nil, fmt.Errorf("读取当前原生执行: %w", err)
		}
		if state.RunID == nil || *state.RunID == "" {
			return nil, fmt.Errorf("当前没有活跃 run；人类读取历史时请显式指定 --run")
		}
		runID = *state.RunID
	}
	if id, e := uuid.Parse(runID); e != nil || id.String() != runID {
		return nil, fmt.Errorf("无效的 run UUID")
	}
	workspace, err := fileutil.SafePath(manifest.Root, m.EndpointID, m.ConversationID, "workspace")
	if err != nil {
		return nil, err
	}
	out := &ExecutionContext{EndpointID: m.EndpointID, ConversationID: m.ConversationID, SessionID: m.ID, RunID: runID,
		AgentID: m.AgentID, Workspace: workspace, Status: "running",
		Inputs: []ContextInput{}, path: path, manifest: manifest, files: []sdk.Attachment{}}
	// The adapter fsyncs accepted before the Client's event poll saves its receipt.
	// Wait for that exact input/run mapping instead of guessing by prompt text.
	deadline := time.Now().Add(2 * time.Second)
	for {
		err = out.readInputs(ctx, events)
		if !errors.Is(err, errContextReceiptPending) || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

var errContextReceiptPending = errors.New("输入受理关系尚在同步，请稍后重试")

func (c *ExecutionContext) readInputs(ctx context.Context, events string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	receipts := map[string]messageReceipt{}
	if c.EndpointID != endpoint.LocalID {
		dir, err := fileutil.SafePath(c.manifest.Root, c.EndpointID, c.ConversationID, ".server", "messages")
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path, err := fileutil.SafePath(dir, entry.Name())
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var receipt messageReceipt
			if err = json.Unmarshal(data, &receipt); err != nil {
				return err
			}
			if receipt.SessionID == c.SessionID && receipt.RunID == c.RunID && receipt.State == "accepted" {
				if receipt.Message.ConversationID != c.ConversationID || receipt.Message.AgentID != c.AgentID || receipt.Message.AgentVersionID != c.manifest.Session.AgentVersionID {
					return fmt.Errorf("输入上下文归属不匹配")
				}
				receipts[receipt.InputID] = receipt
			}
		}
	}
	file, err := os.Open(events)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	c.Inputs, c.files = []ContextInput{}, []sdk.Attachment{}
	c.Status = "running"
	seenFiles := map[string]sdk.Attachment{}
	pending := map[string]bool{}
	for {
		line, e := reader.ReadBytes('\n')
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		var event harness.Event
		if e = json.Unmarshal(line, &event); e != nil {
			return e
		}
		if event.RunID != c.RunID {
			continue
		}
		switch event.Type {
		case "accepted":
			input := ContextInput{InputID: event.InputID, Text: event.Text, AttachmentIDs: []string{}}
			if c.EndpointID != endpoint.LocalID {
				r, ok := receipts[event.InputID]
				if !ok {
					return errContextReceiptPending
				}
				input.MessageID, input.Text, input.Context = r.Message.MessageID, r.Message.Content, r.Message.Context
				for _, attachment := range r.Message.Attachments {
					a := attachment.Attachment
					if a.SessionID != c.ConversationID {
						return fmt.Errorf("附件不属于当前 conversation")
					}
					input.AttachmentIDs = append(input.AttachmentIDs, a.ID)
					if old, ok := seenFiles[a.ID]; ok {
						if old != a {
							return fmt.Errorf("附件元数据冲突")
						}
					} else {
						seenFiles[a.ID] = a
						c.files = append(c.files, a)
					}
				}
			}
			c.Inputs = append(c.Inputs, input)
		case "result":
			c.Status = event.Status
		case "request":
			if event.State == "pending" {
				pending[event.RequestID] = true
			} else {
				delete(pending, event.RequestID)
			}
		}
	}
	if len(c.Inputs) == 0 {
		return fmt.Errorf("run 不属于该上下文或尚未受理")
	}
	if c.Status == "running" && len(pending) > 0 {
		c.Status = "waiting"
	}
	return nil
}

func (c *ExecutionContext) Path() string    { return c.path }
func (c *ExecutionContext) BinPath() string { return filepath.Join(filepath.Dir(c.path), "bin") }
