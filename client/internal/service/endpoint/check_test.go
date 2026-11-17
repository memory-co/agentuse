package endpoint

import (
	"context"
	"errors"
	"fmt"
	"github.com/memory-co/agentuse/internal/securews/securewstest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckKey(t *testing.T) {
	peer := securewstest.New(t)
	svc := Service{Path: filepath.Join(t.TempDir(), "endpoints.yaml")}
	ep, err := svc.Register(context.Background(), peer.URL, peer.Code)
	if err != nil {
		t.Fatal(err)
	}
	me, err := CheckKey(context.Background(), ep)
	if err != nil || me.DeviceID != ep.DeviceID {
		t.Fatal(me, err)
	}
	peer.Revoked.Store(true)
	if _, err = CheckKey(context.Background(), ep); err == nil {
		t.Fatal("revoked certificate accepted")
	}
	if _, err = CheckKey(context.Background(), Endpoint{ServerURL: peer.URL, DeviceKey: "dk_legacy"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("legacy bearer accepted", err)
	}
	if _, err = svc.Register(context.Background(), peer.URL, "st_good"); err == nil {
		t.Fatal("plaintext installation token accepted")
	}
}

func TestRegistrationReplacesLegacyKeyWithoutPersistingPrivateMaterial(t *testing.T) {
	peer := securewstest.New(t)
	path := filepath.Join(t.TempDir(), "endpoints.yaml")
	legacy := fmt.Sprintf("endpoints:\n  - name: old\n    server_url: %s\n    enabled: true\n    device_id: old-device\n    device_key: dk_legacy\n", peer.URL)
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	svc := Service{Path: path}
	if _, err := svc.Registrations(); err == nil {
		t.Fatal("legacy key enabled runtime fallback")
	}
	ep, err := svc.Register(context.Background(), peer.URL, peer.Code)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Registrations(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"device_key:", "dk_legacy", "private_key", "BEGIN PRIVATE KEY"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("registration YAML persisted secret", secret)
		}
	}
	credentials, err := ep.Credentials()
	if err != nil || len(credentials.PrivateKey) == 0 {
		t.Fatal("private credential file unavailable", err)
	}
	info, err := os.Stat(ep.CredentialsFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private material permissions", info, err)
	}
}
