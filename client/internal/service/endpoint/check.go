package endpoint

import (
	"context"
	"errors"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/securews"
	"time"
)

var ErrUnauthorized = errors.New("device certificate is unavailable or expired; register this endpoint again")

func (e Endpoint) Credentials() (*securews.Credentials, error) {
	if e.CredentialsFile == "" {
		return nil, ErrUnauthorized
	}
	return securews.LoadCredentials(e.CredentialsFile)
}

// CheckKey validates the certificate without publishing a heartbeat or replacing
// a running device connection.
func CheckKey(ctx context.Context, cfg Endpoint) (protocol.MeResponse, error) {
	credentials, err := cfg.Credentials()
	if err != nil {
		return protocol.MeResponse{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	channel, result, err := securews.Authenticate(ctx, cfg.ServerURL, credentials)
	if err != nil {
		return protocol.MeResponse{}, err
	}
	defer channel.Close()
	return protocol.MeResponse{DeviceID: result.DeviceID, UserID: result.UserID, Name: result.Name}, nil
}
