package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/test/e2e/e2eutil"
)

type imageContextProof struct {
	Context struct {
		RunID          string `json:"run_id"`
		SessionID      string `json:"session_id"`
		ConversationID string `json:"conversation_id"`
		Status         string `json:"status"`
		Inputs         []struct {
			MessageID string `json:"message_id"`
			Text      string `json:"text"`
		} `json:"inputs"`
	} `json:"context"`
	Attachments struct {
		Attachments []struct {
			ID string `json:"id"`
		} `json:"attachments"`
	} `json:"attachments"`
	File struct {
		LocalPath string `json:"local_path"`
		MIMEType  string `json:"mime_type"`
	} `json:"file"`
	Uploaded struct {
		ID             string `json:"id"`
		RunID          string `json:"run_id"`
		ConversationID string `json:"conversation_id"`
		Direction      string `json:"direction"`
		DownloadPath   string `json:"download_path"`
	} `json:"uploaded"`
	Outputs struct {
		Attachments []struct {
			ID        string `json:"id"`
			Direction string `json:"direction"`
		} `json:"attachments"`
	} `json:"outputs"`

	SHA256 string `json:"sha256"`
}

func TestRunContextImagesAndHumanEntry(t *testing.T) {
	e2eutil.Skip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := e2eutil.StartServer(t, e2eutil.Binary(t, "agentuse-server"), t.TempDir())
	cli, fake := e2eutil.Binary(t, "agentuse"), e2eutil.Binary(t, "fakeharness")
	c := sdk.New(srv.URL, srv.AdminToken)
	home := t.TempDir()
	config := filepath.Join(home, ".agentuse", "endpoints.yaml")
	os.MkdirAll(filepath.Dir(config), 0700)
	writeAdapterConfig(t, config, map[string]any{"harnesses": map[string]any{"fake": map[string]any{"command": fake}, "codex": map[string]any{"command": fake}, "claude": map[string]any{"command": fake}}})
	user := "context-user"
	device, err := c.CreateDaemon(ctx, user, "context machine")
	if err != nil {
		t.Fatal(err)
	}
	register := exec.Command(cli, "register", "--server", srv.URL, "--token", device.SetupToken, "--config", config)
	register.Env = e2eutil.Env(home)
	e2eutil.MustRun(t, register)
	daemon := exec.Command(cli, "daemon", "--config", config)
	daemon.Env = e2eutil.Env(home, "AGENTUSE_UI_ADDR=")
	e2eutil.Start(t, daemon)
	waitOnline(t, c, user, device.ID, 3)
	for _, name := range []string{"fake", "codex", "claude"} {
		if _, err = c.SetDaemonHarness(ctx, user, device.ID, name, true); err != nil {
			t.Fatal(err)
		}
		if _, err = c.CreateAgent(ctx, user, sdk.CreateAgentRequest{ID: "image-" + name, Name: "Image " + name, Harness: name, DaemonID: device.ID}); err != nil {
			t.Fatal(err)
		}
	}
	image, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl6FZkAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(image)
	parsed, _ := url.Parse(srv.URL)
	ep := "http---" + strings.ReplaceAll(parsed.Host, ":", "%3A")
	for _, h := range []string{"fake", "codex", "claude"} {
		t.Run(h, func(t *testing.T) {
			created, err := c.CreateSession(ctx, user, sdk.CreateSessionRequest{})
			if err != nil {
				t.Fatal(err)
			}
			conversation := created.Session.ID
			attachment, err := c.UploadAttachment(ctx, user, conversation, "截图.png", bytes.NewReader(image))
			if err != nil {
				t.Fatal(err)
			}
			prompt := "context-image"
			if h == "fake" {
				prompt = "context-image-hold"
			}
			first, err := c.SendMessage(ctx, user, conversation, sdk.MessageRequest{Prompt: prompt, ToAgentIDs: []string{"image-" + h}, AttachmentIDs: []string{attachment.ID}})
			if err != nil {
				t.Fatal(err)
			}
			var run sdk.Run
			e2eutil.WaitFor(t, 10*time.Second, "context image accepted", func() bool {
				detail, err := c.GetSession(ctx, user, conversation)
				if err != nil || len(detail.Runs) == 0 {
					return false
				}
				run = detail.Runs[0]
				return run.ID != ""
			})
			workspace := filepath.Join(home, ".agentuse", "tmp", ep, conversation, "workspace")
			if h == "fake" {
				future, err := c.UploadAttachment(ctx, user, conversation, "later.png", bytes.NewReader(image))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = c.SendMessage(ctx, user, conversation, sdk.MessageRequest{Prompt: "context-image", ToAgentIDs: []string{"image-fake"}, AttachmentIDs: []string{future.ID}}); err != nil {
					t.Fatal(err)
				}
				e2eutil.WaitFor(t, 10*time.Second, "queued run accepted", func() bool {
					detail, err := c.GetSession(ctx, user, conversation)
					return err == nil && len(detail.Runs) == 2
				})
				if err = os.WriteFile(filepath.Join(workspace, "future-file-id"), []byte(future.ID), 0600); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(workspace, "context-release"), []byte("go"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			finished, err := c.WaitMessageRuns(ctx, user, conversation, first.Message.ID, 20*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range finished.Runs {
				if r.ID == run.ID {
					run = r
				}
			}
			var proof imageContextProof
			if run.Status != "completed" || json.Unmarshal([]byte(run.Output), &proof) != nil {
				t.Fatal("context command failed", run.Status, run.Error, run.Output)
			}
			if proof.Context.RunID != run.ID || proof.Context.SessionID == conversation || proof.Context.ConversationID != conversation || len(proof.Context.Inputs) != 1 || proof.Context.Inputs[0].MessageID != first.Message.ID || proof.Context.Inputs[0].Text != prompt {
				t.Fatal("incorrect context binding", run.Output)
			}
			if len(proof.Attachments.Attachments) != 1 || proof.Attachments.Attachments[0].ID != attachment.ID || proof.File.MIMEType != "image/png" || proof.SHA256 != hex.EncodeToString(digest[:]) || !filepath.IsAbs(proof.File.LocalPath) {
				t.Fatal("incorrect image snapshot", run.Output)
			}
			if proof.Uploaded.RunID != run.ID || proof.Uploaded.ConversationID != conversation || proof.Uploaded.Direction != "output" || len(proof.Outputs.Attachments) != 2 {
				t.Fatal("run output binding failed", run.Output)
			}
			output, err := c.GetAttachment(ctx, user, conversation, proof.Uploaded.ID)
			if err != nil || output.CreatedByRunID != run.ID || output.CreatedByAgentKey != "image-"+h || output.Source != "daemon" || !strings.HasPrefix(output.RelativePath, "reports/") {
				t.Fatal("Server omitted output attribution", output, err)
			}
			download, err := c.DownloadAttachment(ctx, user, conversation, proof.Uploaded.ID)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(download)
			download.Close()
			if err != nil || string(body) != "image sha256: "+proof.SHA256 {
				t.Fatal("Server user could not read output", string(body), err)
			}

			path := filepath.Join(home, ".agentuse", "tmp", ep, conversation, "sessions", proof.Context.SessionID, "context.json")
			inspect := exec.Command(cli, "context", "--path", path, "--run", run.ID, "input")
			inspect.Env = e2eutil.Env(home)
			var selected struct {
				RunID string `json:"run_id"`
			}
			if err = json.Unmarshal([]byte(e2eutil.MustRun(t, inspect)), &selected); err != nil || selected.RunID != run.ID {
				t.Fatal("human context selection failed", selected, err)
			}
			shell := filepath.Join(t.TempDir(), "shell")
			if err = os.WriteFile(shell, []byte("#!/bin/sh\ncontext input\n"), 0700); err != nil {
				t.Fatal(err)
			}
			enter := exec.Command(cli, "context", "enter", "--path", path, "--run", run.ID)
			enter.Env = e2eutil.Env(home, "SHELL="+shell, "AGENTUSE_CONTEXT=/other/context.json", "AGENTUSE_CONTEXT_RUN=other", "AGENTUSE_RUN=other")
			if err = json.Unmarshal([]byte(e2eutil.MustRun(t, enter)), &selected); err != nil || selected.RunID != run.ID {
				t.Fatal("entered shell lost run binding", selected, err)
			}
			old := exec.Command(cli, "attachment", "help")
			old.Env = e2eutil.Env(home)
			if err = old.Run(); err == nil {
				t.Fatal("old attachment management alias retained")
			}
		})
	}
}
