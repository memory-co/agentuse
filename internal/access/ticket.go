// Package access signs input tickets. Business owners decide their permissions
// and whether they are preparing an input or bound to an actual run.
package access

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/memory-co/agentuse/internal/privatefile"
	"os"
	"path/filepath"
	"strings"
)

var ErrTicket = errors.New("invalid execution access ticket")

const Prefix = "at1."

type Claims struct {
	ServerID       string `json:"server_id"`
	DeviceID       string `json:"device_id"`
	Generation     int64  `json:"credential_generation"`
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	AgentID        string `json:"agent_id"`
	AgentVersionID string `json:"agent_version_id"`
	Nonce          string `json:"nonce"`
	BindBefore     int64  `json:"bind_before"`
	AccessLimit    int64  `json:"access_limit"`
}

type Signer struct {
	serverID string
	keyID    string
	key      []byte
}

func New(key []byte, serverID string) (*Signer, error) {
	if len(key) < 32 || serverID == "" {
		return nil, fmt.Errorf("execution signing key needs 32 bytes and a Server identity")
	}
	hash := sha256.Sum256(key)
	return &Signer{serverID: serverID, keyID: hex.EncodeToString(hash[:8]), key: append([]byte(nil), key...)}, nil
}

// LoadKey creates a private key atomically, retaining the winner if another
// process initializes the same installation concurrently.
func LoadKey(path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".access-key-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if err = privatefile.Protect(f.Name()); err != nil {
		f.Close()
		return nil, err
	}
	_, err = f.Write(key)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err = os.Link(f.Name(), path); err != nil && !os.IsExist(err) {
		return nil, err
	}
	key, err = os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid execution signing key file")
	}
	if err := privatefile.Protect(path); err != nil {
		return nil, err
	}
	return key, nil
}

func (s *Signer) Prepare(c Claims) (Claims, error) {
	c.ServerID = s.serverID
	if c.Nonce == "" {
		nonce := make([]byte, 32)
		if _, err := rand.Read(nonce); err != nil {
			return Claims{}, err
		}
		c.Nonce = base64.RawURLEncoding.EncodeToString(nonce)
	}
	_, err := s.Sign(c)
	return c, err
}

func (s *Signer) Sign(c Claims) (string, error) {
	nonce, err := base64.RawURLEncoding.DecodeString(c.Nonce)
	if err != nil || len(nonce) != 32 || c.ServerID != s.serverID || c.DeviceID == "" || c.Generation < 1 || c.ConversationID == "" || c.MessageID == "" || c.AgentID == "" || c.AgentVersionID == "" || c.BindBefore <= 0 || c.AccessLimit < c.BindBefore {
		return "", ErrTicket
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	body := Prefix + s.keyID + "." + base64.RawURLEncoding.EncodeToString(raw)
	return body + "." + base64.RawURLEncoding.EncodeToString(s.mac(body)), nil
}

func (s *Signer) mac(body string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte("agentuse-input-access/v1\x00"))
	m.Write([]byte(body))
	return m.Sum(nil)
}

// Verify authenticates the exact signed bytes, without treating bind_before as
// an execution expiry. A bound run has its own Server-controlled deadline.
func (s *Signer) Verify(token string) (Claims, error) {
	if len(token) > 8192 {
		return Claims{}, ErrTicket
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "at1" || parts[1] != s.keyID {
		return Claims{}, ErrTicket
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || !hmac.Equal(signature, s.mac(strings.Join(parts[:3], "."))) {
		return Claims{}, ErrTicket
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrTicket
	}
	var c Claims
	if err = json.Unmarshal(raw, &c); err != nil {
		return Claims{}, ErrTicket
	}
	canonical, err := s.Sign(c)
	if err != nil || !hmac.Equal([]byte(token), []byte(canonical)) {
		return Claims{}, ErrTicket
	}
	return c, nil
}

func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func EqualHash(token, hash string) bool {
	return hash != "" && hmac.Equal([]byte(Hash(token)), []byte(hash))
}
