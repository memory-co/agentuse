package securews

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestChannelEncryptionAndDevicePossession(t *testing.T) {
	for _, encryption := range []bool{true, false} {
		t.Run(map[bool]string{true: "inner-tls", false: "outer-wss"}[encryption], func(t *testing.T) {
			id, err := NewIdentity(time.Now())
			if err != nil {
				t.Fatal(err)
			}
			credentials, csr, err := NewCredentials(id.CA)
			if err != nil {
				t.Fatal(err)
			}
			cert, err := id.Issue(csr, "device", 7, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = credentials.SetCertificate(cert, time.Now()); err != nil {
				t.Fatal(err)
			}
			errors := make(chan error, 1)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := websocket.Accept(w, r, nil)
				if err != nil {
					errors <- err
					return
				}
				defer ws.CloseNow()
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				defer cancel()
				ch, policy, err := Accept(ctx, ws, id, encryption)
				if err != nil {
					errors <- err
					return
				}
				var proof Authentication
				if err = ch.Read(ctx, &proof); err != nil {
					errors <- err
					return
				}
				p, err := VerifyProof(id.CA, policy, proof, time.Now())
				if err != nil {
					errors <- err
					return
				}
				if p.DeviceID != "device" || p.Generation != 7 {
					t.Error("wrong identity", p)
				}
				if encryption && (ch.TLSPeer == nil || ch.TLSPeer.SerialNumber.Cmp(p.Certificate.SerialNumber) != 0) {
					t.Error("TLS certificate not bound to proof")
				}
				var value map[string]string
				if err = ch.Read(ctx, &value); err != nil {
					errors <- err
					return
				}
				errors <- ch.Write(ctx, value)
			})
			server := httptest.NewUnstartedServer(handler)
			if encryption {
				server.Start()
			} else {
				server.StartTLS()
			}
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client := server.Client()
			if encryption {
				client = &http.Client{}
			}
			ch, p, err := ConnectWithClient(ctx, server.URL, credentials, client)
			if err != nil {
				t.Fatal(err)
			}
			defer ch.Close()
			key, _ := privateKey(credentials.PrivateKey)
			sig, err := key.Sign(rand.Reader, signedBytes("agentuse-device-proof/v1", p), crypto.Hash(0))
			if err != nil {
				t.Fatal(err)
			}
			if err = ch.Write(ctx, Authentication{Type: "authenticate", Certificate: cert, Signature: sig}); err != nil {
				t.Fatal(err)
			}
			if err = ch.Write(ctx, map[string]string{"secret": "value"}); err != nil {
				t.Fatal(err)
			}
			var out map[string]string
			if err = ch.Read(ctx, &out); err != nil || out["secret"] != "value" {
				t.Fatal(out, err)
			}
			if err = <-errors; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDeviceProofRejectsDifferentConnectionAndIssuer(t *testing.T) {
	id, _ := NewIdentity(time.Now())
	other, _ := NewIdentity(time.Now())
	c, csr, _ := NewCredentials(id.CA)
	cert, _ := id.Issue(csr, "device", 1, time.Now())
	p := Policy{ClientNonce: randomNonce(), ServerNonce: randomNonce(), Connection: randomNonce(), ServerID: ServerID(id.CA)}
	key, _ := privateKey(c.PrivateKey)
	sig, _ := key.Sign(rand.Reader, signedBytes("agentuse-device-proof/v1", p), crypto.Hash(0))
	proof := Authentication{Type: "authenticate", Certificate: cert, Signature: sig}
	if _, err := VerifyProof(id.CA, p, proof, time.Now()); err != nil {
		t.Fatal(err)
	}
	changed := p
	changed.Connection = randomNonce()
	if _, err := VerifyProof(id.CA, changed, proof, time.Now()); err == nil {
		t.Fatal("replayed proof accepted")
	}
	if _, err := VerifyProof(other.CA, p, proof, time.Now()); err == nil {
		t.Fatal("wrong authority accepted")
	}
	if _, err := VerifyProof(id.CA, p, proof, time.Now().Add(91*24*time.Hour)); err == nil {
		t.Fatal("expired certificate accepted")
	}
}

func TestIdentityAndCredentialsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadIdentity(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadIdentity(dir, time.Now())
	if err != nil || ServerID(a.CA) != ServerID(b.CA) {
		t.Fatal("trust changed", err)
	}
	c, csr, _ := NewCredentials(a.CA)
	raw, _ := a.Issue(csr, "device", 2, time.Now())
	if _, err = c.SetCertificate(raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err = c.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tls.X509KeyPair(loaded.Certificate, loaded.PrivateKey); err != nil {
		t.Fatal(err)
	}
}
