package run

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/session"
)

const ContextInstructions = `# Execution context tools

Call context directly; no Server, conversation or session ID is needed:
- context status: query execution identity and status; messages are delivered through the native harness input channel.
- context attachment list: fetch the current attachment list for the entire conversation from Server, including files from earlier messages and runs. Listing does not download files.
- context attachment download PATH: download a file by its conversation-relative path, for example user_input/screenshot.png. Use relative_path from the attachment list or message; no file ID is needed. The file is verified and saved at <workspace>/<relative_path>. Reuse that path in later turns. The command returns the local absolute path, name and MIME type; use your native image or file tools to read it.
- Create output files inside the workspace, for example reports/report.txt. user_input/ is reserved for user uploads. context attachment upload FILE publishes a file to the conversation at the same workspace-relative path and associates it with this run. If Server assigns a different path to avoid a conflict, use the returned relative_path and local_path.
- Before telling the user that an output file is available, upload it and cite the returned download_path. A local workspace path is not a browser download link. Uploading does not send a reply or finish execution.
- context --help: show available commands.
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
	EndpointID     string `json:"endpoint_id"`
	ConversationID string `json:"conversation_id"`
	SessionID      string `json:"session_id"`
	RunID          string `json:"run_id"`
	AgentID        string `json:"agent_id"`
	Status         string `json:"status"`
	Workspace      string `json:"workspace"`
	path           string
	manifest       contextManifest
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

// OpenConversationContext binds file access to the declared conversation only.
func OpenConversationContext(ctx context.Context, path string) (*ExecutionContext, error) {
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
	workspace, err := fileutil.SafePath(manifest.Root, m.EndpointID, m.ConversationID, "workspace")
	if err != nil {
		return nil, err
	}
	out := &ExecutionContext{EndpointID: m.EndpointID, ConversationID: m.ConversationID, SessionID: m.ID, RunID: "",
		AgentID: m.AgentID, Workspace: workspace, Status: "", path: path, manifest: manifest}
	return out, nil
}

// OpenExecutionContext selects the actual run for status, upload and shell access.
func OpenExecutionContext(ctx context.Context, path, runID string) (*ExecutionContext, error) {
	out, err := OpenConversationContext(ctx, path)
	if err != nil {
		return nil, err
	}
	manifest := out.manifest
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
	out.RunID = runID
	if err = out.readRunStatus(ctx, events); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *ExecutionContext) readRunStatus(ctx context.Context, events string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.Open(events)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	accepted := false
	c.Status = "running"
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
			accepted = true
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
	if !accepted {
		return fmt.Errorf("run 不属于该上下文或尚未受理")
	}
	if c.Status == "running" && len(pending) > 0 {
		c.Status = "waiting"
	}
	return nil
}

func (c *ExecutionContext) Path() string    { return c.path }
func (c *ExecutionContext) BinPath() string { return filepath.Join(filepath.Dir(c.path), "bin") }
