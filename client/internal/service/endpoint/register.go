package endpoint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/memory-co/agentuse/internal/securews"
)

// Register imports a device private key and proves possession inside the trusted channel.
func (s Service) Register(ctx context.Context, server, pin, token string) (Endpoint, error) {
	server, id, err := NormalizeEndpoint(server)
	if err != nil {
		return Endpoint{}, err
	}
	credentials, err := securews.ImportDeviceKey(token)
	if err != nil {
		return Endpoint{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	channel, policy, err := securews.ConnectForEnrollment(ctx, server, pin, credentials)
	if err != nil {
		return Endpoint{}, err
	}
	defer channel.Close()
	host, _ := os.Hostname()
	proof, err := credentials.EnrollmentProof(policy, host)
	if err != nil {
		return Endpoint{}, err
	}
	if err = channel.Write(ctx, proof); err != nil {
		return Endpoint{}, err
	}
	var reply securews.AuthResult
	if err = channel.Read(ctx, &reply); err != nil {
		return Endpoint{}, err
	}
	if reply.Type != "enrolled" || reply.Error != "" || reply.DeviceID == "" {
		return Endpoint{}, securews.ErrCredential
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
