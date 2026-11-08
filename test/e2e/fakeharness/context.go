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
	files, err := call("attachment", "list")
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
	file, err := call("attachment", "read", attachments.Attachments[0].ID)
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
		if _, err = call("attachment", "read", strings.TrimSpace(string(future))); err == nil {
			return "", fmt.Errorf("future attachment exposed")
		}
	}
	reportPath := "report-" + view.RunID + ".txt"
	reportBody := []byte("image sha256: " + hex.EncodeToString(digest[:]))
	if err = os.WriteFile(reportPath, reportBody, 0600); err != nil {
		return "", err
	}
	uploaded, err := call("attachment", "upload", reportPath)
	if err != nil {
		return "", err
	}
	var output struct {
		ID             string `json:"id"`
		RunID          string `json:"run_id"`
		ConversationID string `json:"conversation_id"`
		Direction      string `json:"direction"`
	}
	if err = json.Unmarshal(uploaded, &output); err != nil || output.RunID != view.RunID || output.ConversationID != view.ConversationID || output.Direction != "output" {
		return "", fmt.Errorf("upload lost its run binding: %v", err)
	}
	listedOutputs, err := call("attachment", "list")
	if err != nil {
		return "", err
	}
	saved, err := call("attachment", "read", output.ID)
	if err != nil {
		return "", err
	}
	var savedFile struct {
		LocalPath string `json:"local_path"`
	}
	if err = json.Unmarshal(saved, &savedFile); err != nil {
		return "", err
	}
	savedBody, err := os.ReadFile(savedFile.LocalPath)
	if err != nil || string(savedBody) != string(reportBody) {
		return "", fmt.Errorf("uploaded output could not be read: %v", err)
	}
	after, err := call("input")
	if err != nil {
		return "", err
	}
	var state struct {
		Status string `json:"status"`
	}
	if err = json.Unmarshal(after, &state); err != nil || state.Status != "running" {
		return "", fmt.Errorf("upload completed the run unexpectedly: %v", err)
	}

	proof, _ := json.Marshal(map[string]any{"context": json.RawMessage(input), "attachments": json.RawMessage(files), "file": json.RawMessage(file), "sha256": hex.EncodeToString(digest[:]), "help": string(help), "uploaded": json.RawMessage(uploaded), "outputs": json.RawMessage(listedOutputs)})
	return string(proof), nil
}
