package endpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/memory-co/agentuse/internal/protocol"
	"net/http"
	"os"
	"strings"
	"time"
)

// Register exchanges a setup token for a device identity and saves only its endpoint registration.
func (s Service) Register(ctx context.Context, server, token string) (Endpoint, error) {
	server = strings.TrimRight(server, "/")
	host, _ := os.Hostname()
	body, _ := json.Marshal(protocol.RegisterRequest{Token: token, Hostname: host})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+protocol.RegisterPath, bytes.NewReader(body))
	if err != nil {
		return Endpoint{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Endpoint{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return Endpoint{}, errors.New(e.Error)
	}
	var rr protocol.RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return Endpoint{}, err
	}
	ep := Endpoint{Name: server, ServerURL: server, Enabled: true, DeviceID: rr.DaemonID, DeviceKey: rr.Key, Mode: ModeServer}
	if err := s.Save(ep); err != nil {
		return Endpoint{}, err
	}
	return s.Get(server)
}
