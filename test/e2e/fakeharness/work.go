package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Execute the actual tool delivered by the Server, with no Work command in the Client.
func serverWorkTool(args ...string) ([]byte, error) {
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(strings.TrimRight(os.Getenv("AGENTUSE_SERVER"), "/") + "/api/agent-tools/work.py")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("tool download: %d", response.StatusCode)
	}
	code, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("python3", append([]string{"-"}, args...)...)
	cmd.Stdin = strings.NewReader(string(code))
	return cmd.CombinedOutput()
}

func serverWorkSpawn(agent, prompt string) ([]byte, error) {
	if data, err := serverWorkTool("agents"); err != nil {
		return data, err
	}
	data, err := serverWorkTool("new", "--defer-start")
	if err != nil {
		return data, err
	}
	var created struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err = json.Unmarshal(data, &created); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp("", "agentuse-work-input-*.txt")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	if _, err = file.WriteString("input from coordinator"); err != nil {
		file.Close()
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	if b, err := serverWorkTool("upload", created.Session.ID, file.Name()); err != nil {
		return b, err
	}
	if b, err := serverWorkTool("send", "--agents", agent, created.Session.ID, prompt); err != nil {
		return b, err
	}
	if b, err := serverWorkTool("threads"); err != nil {
		return b, err
	}
	if b, err := serverWorkTool("read", created.Session.ID); err != nil {
		return b, err
	}
	return data, nil
}
