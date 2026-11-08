package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runs the same bare command that an AI shell tool receives, not a test API.
func contextProof(hold bool) (string, error) {
	if hold {
		deadline := time.Now().Add(20 * time.Second)
		for {
			if _, err := os.Stat("context-release"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				return "", fmt.Errorf("context release timed out")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	call := func(args ...string) ([]byte, error) {
		out, err := exec.Command("context", args...).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("context %s: %w: %s", strings.Join(args, " "), err, out)
		}
		return out, nil
	}
	input, err := call("input")
	if err != nil {
		return "", err
	}
	var view struct {
		RunID          string            `json:"run_id"`
		SessionID      string            `json:"session_id"`
		ConversationID string            `json:"conversation_id"`
		Inputs         []json.RawMessage `json:"inputs"`
	}
	if err = json.Unmarshal(input, &view); err != nil {
		return "", err
	}
	if view.RunID == "" || view.SessionID != os.Getenv("AGENTUSE_SESSION") || len(view.Inputs) == 0 {
		return "", fmt.Errorf("missing native context binding")
	}
	if id := os.Getenv("AGENTUSE_RUN"); id != "" && id != view.RunID {
		return "", fmt.Errorf("context selected another run")
	}
	files, err := call("attachments")
	if err != nil {
		return "", err
	}
	var attachments struct {
		Attachments []struct {
			ID string `json:"id"`
		} `json:"attachments"`
	}
	if err = json.Unmarshal(files, &attachments); err != nil {
		return "", err
	}
	if len(attachments.Attachments) == 0 {
		return "", fmt.Errorf("missing image attachments")
	}
	file, err := call("attachment", attachments.Attachments[0].ID)
	if err != nil {
		return "", err
	}
	var attachment struct {
		LocalPath string `json:"local_path"`
		MIMEType  string `json:"mime_type"`
	}
	if err = json.Unmarshal(file, &attachment); err != nil {
		return "", err
	}
	image, err := os.ReadFile(attachment.LocalPath)
	if err != nil || attachment.MIMEType != "image/png" {
		return "", fmt.Errorf("image unavailable: %v", err)
	}
	digest := sha256.Sum256(image)
	help, err := call("--help")
	if err != nil {
		return "", err
	}
	for _, foreign := range []string{"agentuse register", "agentuse-server", "--server", "--token", "--path"} {
		if strings.Contains(string(help), foreign) {
			return "", fmt.Errorf("management command exposed in context help")
		}
	}
	if future, err := os.ReadFile("future-file-id"); err == nil && hold {
		if _, err = call("attachment", strings.TrimSpace(string(future))); err == nil {
			return "", fmt.Errorf("future attachment exposed")
		}
	}
	proof, _ := json.Marshal(map[string]any{"context": json.RawMessage(input), "attachments": json.RawMessage(files), "file": json.RawMessage(file), "sha256": hex.EncodeToString(digest[:]), "help": string(help)})
	return string(proof), nil
}
