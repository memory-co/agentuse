package securews

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/memory-co/agentuse/internal/protocol"
)

var ErrProtocol = errors.New("client/server protocol upgrade required")
var ErrCredential = errors.New("device authentication rejected; register this endpoint again")

const MaxMessage = 8 << 20

type Channel struct {
	Limit   uint32
	WS      *websocket.Conn
	stream  net.Conn
	mu      sync.Mutex
	TLSPeer *x509.Certificate
}

func (c *Channel) Read(ctx context.Context, v any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	if err := c.stream.SetReadDeadline(deadline); err != nil {
		return err
	}
	defer c.stream.SetReadDeadline(time.Time{})
	var size [4]byte
	if _, err := io.ReadFull(c.stream, size[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(size[:])
	limit := c.Limit
	if limit == 0 {
		limit = MaxMessage
	}
	if n == 0 || n > limit {
		return fmt.Errorf("invalid device message length")
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(c.stream, raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func (c *Channel) Write(ctx context.Context, v any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(raw) > MaxMessage {
		return fmt.Errorf("device message too large")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	deadline, _ := ctx.Deadline()
	if deadline.IsZero() {
		deadline = time.Now().Add(15 * time.Second)
	}
	if err := c.stream.SetWriteDeadline(deadline); err != nil {
		return err
	}
	defer c.stream.SetWriteDeadline(time.Time{})
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(raw)))
	data := append(header[:], raw...)
	for len(data) > 0 {
		n, e := c.stream.Write(data)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func (c *Channel) Close() { c.WS.CloseNow() }

type Challenge struct {
	Server string `json:"server"`
	Nonce  string `json:"nonce"`
}
type Policy struct {
	Server      string `json:"server"`
	ClientNonce string `json:"client_nonce"`
	ServerNonce string `json:"server_nonce"`
	Connection  string `json:"connection"`
	ServerID    string `json:"server_id"`
	Protocol    string `json:"protocol"`
	Encryption  bool   `json:"encryption"`
}
type SignedPolicy struct {
	Policy      Policy `json:"policy"`
	Certificate []byte `json:"certificate"`
	Signature   []byte `json:"signature"`
}
type Authentication struct {
	Type        string `json:"type"`
	Certificate []byte `json:"certificate,omitempty"`
	Signature   []byte `json:"signature,omitempty"`
	Token       string `json:"token,omitempty"`
	CSR         []byte `json:"csr,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
}
type AuthResult struct {
	Type        string `json:"type"`
	DeviceID    string `json:"device_id,omitempty"`
	Name        string `json:"name,omitempty"`
	UserID      string `json:"user_id,omitempty"`
	Certificate []byte `json:"certificate,omitempty"`
	Error       string `json:"error,omitempty"`
}

func randomNonce() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func validNonce(s string) bool {
	b, e := base64.RawURLEncoding.DecodeString(s)
	return e == nil && len(b) == 32
}
func signedBytes(domain string, p Policy) []byte {
	b, _ := json.Marshal(p)
	return append([]byte(domain+"\x00"), b...)
}

// Accept negotiates an authenticated Server policy and (by default) inner TLS.
// The caller must subsequently validate the device proof against its database.
func Accept(ctx context.Context, ws *websocket.Conn, id *Identity, encryption bool) (*Channel, Policy, error) {
	handshake, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ws.SetReadLimit(16 << 10)
	var challenge Challenge
	if err := wsjson.Read(handshake, ws, &challenge); err != nil {
		return nil, Policy{}, err
	}
	if !validNonce(challenge.Nonce) {
		return nil, Policy{}, fmt.Errorf("invalid device challenge")
	}
	p := Policy{Server: challenge.Server, ClientNonce: challenge.Nonce, ServerNonce: randomNonce(), Connection: randomNonce(), ServerID: ServerID(id.CA), Protocol: protocol.ExecutionProtocol, Encryption: encryption}
	key, err := privateKey(id.PrivateKey)
	if err != nil {
		return nil, p, err
	}
	signature, err := key.Sign(rand.Reader, signedBytes("agentuse-server-policy/v1", p), crypto.Hash(0))
	if err != nil {
		return nil, p, err
	}
	if err = wsjson.Write(handshake, ws, SignedPolicy{Policy: p, Certificate: id.Certificate, Signature: signature}); err != nil {
		return nil, p, err
	}
	ws.SetReadLimit(MaxMessage)
	ws.SetReadLimit(MaxMessage)
	stream := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	if err = stream.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return nil, p, err
	}
	var tlsPeer *x509.Certificate
	if encryption {
		cert, e := tls.X509KeyPair(id.Certificate, id.PrivateKey)
		if e != nil {
			return nil, p, e
		}
		roots, e := Roots(id.CA)
		if e != nil {
			return nil, p, e
		}
		secure := tls.Server(stream, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientCAs: roots, ClientAuth: tls.VerifyClientCertIfGiven, NextProtos: []string{protocol.ExecutionProtocol}})
		if e = secure.HandshakeContext(handshake); e != nil {
			return nil, p, e
		}
		if peers := secure.ConnectionState().PeerCertificates; len(peers) > 0 {
			tlsPeer = peers[0]
		}
		stream = secure
	}
	if err = stream.SetDeadline(time.Time{}); err != nil {
		return nil, p, err
	}
	return &Channel{WS: ws, stream: stream, TLSPeer: tlsPeer}, p, nil
}

func VerifyProof(ca []byte, p Policy, auth Authentication, now time.Time) (Peer, error) {
	if auth.Type != "authenticate" {
		return Peer{}, fmt.Errorf("device authentication required")
	}
	peer, err := VerifyDevice(ca, auth.Certificate, now)
	if err != nil {
		return Peer{}, err
	}
	key, ok := peer.Certificate.PublicKey.(ed25519.PublicKey)
	if !ok || !ed25519.Verify(key, signedBytes("agentuse-device-proof/v1", p), auth.Signature) {
		return Peer{}, fmt.Errorf("invalid device possession proof")
	}
	return peer, nil
}

// Connect establishes only the trusted channel. Enrollment and normal device
// authentication share this policy negotiation and framing.
func Connect(ctx context.Context, server string, credentials *Credentials) (*Channel, Policy, error) {
	return ConnectWithClient(ctx, server, credentials, nil)
}

func ConnectWithClient(ctx context.Context, server string, credentials *Credentials, client *http.Client) (*Channel, Policy, error) {
	if credentials == nil {
		return nil, Policy{}, fmt.Errorf("device trust is missing; register again")
	}
	address := strings.Replace(strings.Replace(strings.TrimRight(server, "/"), "https://", "wss://", 1), "http://", "ws://", 1) + protocol.WSPath
	dialCtx, dialCancel := context.WithTimeout(ctx, 15*time.Second)
	defer dialCancel()
	ws, response, err := websocket.Dial(dialCtx, address, &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{protocol.ProtocolHeader: []string{protocol.ExecutionProtocol}}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		if response != nil {
			response.Body.Close()
			if response.StatusCode == http.StatusUpgradeRequired {
				return nil, Policy{}, ErrProtocol
			}
		}
		return nil, Policy{}, err
	}
	ok := false
	defer func() {
		if !ok {
			ws.CloseNow()
		}
	}()
	handshake, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ws.SetReadLimit(32 << 10)
	challenge := Challenge{Server: strings.TrimRight(server, "/"), Nonce: randomNonce()}
	if err = wsjson.Write(handshake, ws, challenge); err != nil {
		return nil, Policy{}, err
	}
	var announcement SignedPolicy
	if err = wsjson.Read(handshake, ws, &announcement); err != nil {
		return nil, Policy{}, err
	}
	p := announcement.Policy
	if p.Server != challenge.Server || p.ClientNonce != challenge.Nonce || !validNonce(p.ServerNonce) || !validNonce(p.Connection) || p.Protocol != protocol.ExecutionProtocol || p.ServerID != ServerID(credentials.CA) {
		return nil, p, fmt.Errorf("invalid Server policy binding")
	}
	cert, err := Certificate(announcement.Certificate)
	if err != nil {
		return nil, p, err
	}
	roots, err := Roots(credentials.CA)
	if err != nil {
		return nil, p, err
	}
	if _, err = cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: "agentuse", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return nil, p, err
	}
	pub, valid := cert.PublicKey.(ed25519.PublicKey)
	if !valid || !ed25519.Verify(pub, signedBytes("agentuse-server-policy/v1", p), announcement.Signature) {
		return nil, p, fmt.Errorf("invalid Server policy signature")
	}
	if !p.Encryption && !strings.HasPrefix(address, "wss://") {
		return nil, p, fmt.Errorf("disabling inner encryption requires WSS")
	}
	ws.SetReadLimit(MaxMessage)
	stream := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	if err = stream.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return nil, p, err
	}
	if p.Encryption {
		cfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "agentuse", NextProtos: []string{protocol.ExecutionProtocol}}
		if len(credentials.Certificate) > 0 {
			pair, e := tls.X509KeyPair(credentials.Certificate, credentials.PrivateKey)
			if e != nil {
				return nil, p, e
			}
			cfg.Certificates = []tls.Certificate{pair}
		}
		secure := tls.Client(stream, cfg)
		if err = secure.HandshakeContext(handshake); err != nil {
			return nil, p, err
		}
		stream = secure
	}
	if err = stream.SetDeadline(time.Time{}); err != nil {
		return nil, p, err
	}
	ok = true
	return &Channel{WS: ws, stream: stream}, p, nil
}

func Authenticate(ctx context.Context, server string, credentials *Credentials) (*Channel, AuthResult, error) {
	c, p, err := Connect(ctx, server, credentials)
	if err != nil {
		return nil, AuthResult{}, err
	}
	ok := false
	defer func() {
		if !ok {
			c.Close()
		}
	}()
	authCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	key, err := privateKey(credentials.PrivateKey)
	if err != nil {
		return nil, AuthResult{}, err
	}
	sig, err := key.Sign(rand.Reader, signedBytes("agentuse-device-proof/v1", p), crypto.Hash(0))
	if err != nil {
		return nil, AuthResult{}, err
	}
	if err = c.Write(authCtx, Authentication{Type: "authenticate", Certificate: credentials.Certificate, Signature: sig}); err != nil {
		return nil, AuthResult{}, err
	}
	var result AuthResult
	if err = c.Read(authCtx, &result); err != nil {
		return nil, result, err
	}
	if result.Type != "authenticated" || result.Error != "" {
		return nil, result, ErrCredential
	}
	ok = true
	return c, result, nil
}
