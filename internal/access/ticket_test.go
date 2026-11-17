package access

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestTicketsAuthenticateIssuerAndExactClaims(t *testing.T) {
	s, err := New(bytes.Repeat([]byte{1}, 32), "server-a")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Prepare(Claims{DeviceID: "device", Generation: 1, ConversationID: "conversation", MessageID: "message", AgentID: "agent", AgentVersionID: "version", BindBefore: 100, AccessLimit: 200})
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Sign(c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Verify(token)
	if err != nil || got != c {
		t.Fatal(got, err)
	}
	if !EqualHash(token, Hash(token)) {
		t.Fatal("Client can compute the same public SHA256")
	}
	parts := strings.Split(token, ".")
	for _, field := range []string{"device_id", "message_id", "agent_id", "agent_version_id", "credential_generation", "bind_before", "access_limit", "server_id"} {
		t.Run(field, func(t *testing.T) {
			body, _ := base64.RawURLEncoding.DecodeString(parts[2])
			var values map[string]any
			json.Unmarshal(body, &values)
			switch field {
			case "credential_generation", "bind_before", "access_limit":
				values[field] = 999999
			default:
				values[field] = "attacker"
			}
			body, _ = json.Marshal(values)
			altered := append([]string(nil), parts...)
			altered[2] = base64.RawURLEncoding.EncodeToString(body)
			if _, err := s.Verify(strings.Join(altered, ".")); err == nil {
				t.Fatal("tampered ticket accepted")
			}
		})
	}
	wrong, _ := New(bytes.Repeat([]byte{2}, 32), "server-a")
	if _, err = wrong.Verify(token); err == nil {
		t.Fatal("wrong signing key accepted")
	}
	other, _ := New(bytes.Repeat([]byte{1}, 32), "server-b")
	if _, err = other.Verify(token); err == nil {
		t.Fatal("cross-server ticket accepted")
	}
	// The signer authenticates bind_before, but expiry is phase-dependent and
	// must be decided by the Server's run record, not by JWT exp handling.
	if _, err = s.Verify(token); err != nil {
		t.Fatal("old preparation date is not a blanket signature failure", err)
	}
}

func TestSigningKeyPersistsAcrossConcurrentStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security", "key")
	var group sync.WaitGroup
	results := make(chan []byte, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() { defer group.Done(); key, err := LoadKey(path); results <- key; errs <- err }()
	}
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	first := <-results
	for key := range results {
		if !bytes.Equal(first, key) {
			t.Fatal("concurrent signing keys differ")
		}
	}
	key, err := LoadKey(path)
	if err != nil || !bytes.Equal(first, key) {
		t.Fatal("restart changed key", err)
	}
}
