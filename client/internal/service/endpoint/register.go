package endpoint

import (
	"context"
	"fmt"
	"github.com/memory-co/agentuse/internal/securews"
	"os"
	"path/filepath"
	"time"
)

// Register consumes a trusted installation code inside the authenticated channel.
// The generated device private key never leaves this process or its private file.
func (s Service) Register(ctx context.Context, server, token string) (Endpoint, error) {
	invitation, err := securews.DecodeInvitation(token)
	if err != nil {
		return Endpoint{}, err
	}
	if server == "" {
		server = invitation.Server
	}
	server, id, err := NormalizeEndpoint(server)
	if err != nil {
		return Endpoint{}, err
	}
	expected, _, err := NormalizeEndpoint(invitation.Server)
	if err != nil || expected != server {
		return Endpoint{}, fmt.Errorf("installation code belongs to another Server address")
	}
	credentials, csr, err := securews.NewCredentials(invitation.CA)
	if err != nil {
		return Endpoint{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	channel, _, err := securews.Connect(ctx, server, credentials)
	if err != nil {
		return Endpoint{}, err
	}
	defer channel.Close()
	host, _ := os.Hostname()
	if err = channel.Write(ctx, securews.Authentication{Type: "enroll", Token: invitation.Token, CSR: csr, Hostname: host}); err != nil {
		return Endpoint{}, err
	}
	var reply securews.AuthResult
	if err = channel.Read(ctx, &reply); err != nil {
		return Endpoint{}, err
	}
	if reply.Type != "enrolled" || reply.Error != "" || reply.DeviceID == "" {
		return Endpoint{}, fmt.Errorf("installation code is invalid or already used")
	}
	peer, err := credentials.SetCertificate(reply.Certificate, time.Now())
	if err != nil {
		return Endpoint{}, err
	}
	if peer.DeviceID != reply.DeviceID {
		return Endpoint{}, fmt.Errorf("registered device identity mismatch")
	}
	path, err := filepath.Abs(filepath.Join(filepath.Dir(s.Path), "credentials", id, "device.json"))
	if err != nil {
		return Endpoint{}, err
	}
	if err = credentials.Save(path); err != nil {
		return Endpoint{}, err
	}
	ep := Endpoint{Name: server, ServerURL: server, Enabled: true, DeviceID: reply.DeviceID, CredentialsFile: path, Mode: ModeServer}
	if err = s.Save(ep); err != nil {
		return Endpoint{}, err
	}
	return ep, nil
}
