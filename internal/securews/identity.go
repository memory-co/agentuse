// Package securews implements the authenticated device channel independently of
// business services. Device revocation and execution authorization live above it.
package securews

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/memory-co/agentuse/internal/privatefile"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Identity struct {
	CA          []byte `json:"ca"`
	CAKey       []byte `json:"ca_key"`
	Certificate []byte `json:"certificate"`
	PrivateKey  []byte `json:"private_key"`
}

type Credentials struct {
	CA          []byte `json:"ca"`
	Certificate []byte `json:"certificate"`
	PrivateKey  []byte `json:"private_key"`
}

type Peer struct {
	DeviceID    string
	Generation  int64
	Serial      string
	KeyHash     string
	NotAfter    time.Time
	Certificate *x509.Certificate
}

func serial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

func keyPair() (ed25519.PublicKey, []byte, error) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pub, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func privateKey(raw []byte) (crypto.Signer, error) {
	block, rest := pem.Decode(raw)
	if block == nil || len(rest) != 0 || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("invalid private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	signer, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("Ed25519 key required")
	}
	return signer, nil
}

func Certificate(raw []byte) (*x509.Certificate, error) {
	b, rest := pem.Decode(raw)
	if b == nil || b.Type != "CERTIFICATE" || len(rest) != 0 {
		return nil, fmt.Errorf("invalid certificate")
	}
	return x509.ParseCertificate(b.Bytes)
}

func Roots(ca []byte) (*x509.CertPool, error) {
	cert, err := Certificate(ca)
	if err != nil || !cert.IsCA {
		return nil, fmt.Errorf("invalid Server trust root")
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return roots, nil
}

func ServerID(ca []byte) string {
	sum := sha256.Sum256(ca)
	return hex.EncodeToString(sum[:])
}

func NewIdentity(now time.Time) (*Identity, error) {
	pub, key, err := keyPair()
	if err != nil {
		return nil, err
	}
	caKey, err := privateKey(key)
	if err != nil {
		return nil, err
	}
	sn, err := serial()
	if err != nil {
		return nil, err
	}
	ca := &x509.Certificate{SerialNumber: sn, Subject: pkix.Name{CommonName: "AgentUse device authority"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, caKey)
	if err != nil {
		return nil, err
	}
	id := &Identity{CA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), CAKey: key}
	if err = id.renewServer(now); err != nil {
		return nil, err
	}
	return id, nil
}

func (id *Identity) renewServer(now time.Time) error {
	ca, err := Certificate(id.CA)
	if err != nil {
		return err
	}
	key, err := privateKey(id.CAKey)
	if err != nil {
		return err
	}
	pub, raw, err := keyPair()
	if err != nil {
		return err
	}
	sn, err := serial()
	if err != nil {
		return err
	}
	cert := &x509.Certificate{SerialNumber: sn, Subject: pkix.Name{CommonName: "AgentUse device transport"}, DNSNames: []string{"agentuse"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, ca, pub, key)
	if err != nil {
		return err
	}
	id.Certificate = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	id.PrivateKey = raw
	return nil
}

// LoadIdentity publishes a complete identity atomically, never an empty or
// partially-written trust root. Existing malformed material is not overwritten.
func LoadIdentity(directory string, now time.Time) (*Identity, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "identity.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		id, e := NewIdentity(now)
		if e != nil {
			return nil, e
		}
		raw, e = json.Marshal(id)
		if e != nil {
			return nil, e
		}
		tmp, e := os.CreateTemp(directory, ".identity-*")
		if e != nil {
			return nil, e
		}
		defer os.Remove(tmp.Name())
		if e = privatefile.Protect(tmp.Name()); e != nil {
			tmp.Close()
			return nil, e
		}
		if _, e = tmp.Write(raw); e == nil {
			e = tmp.Sync()
		}
		ce := tmp.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			return nil, e
		}
		if e = os.Link(tmp.Name(), path); e != nil && !os.IsExist(e) {
			return nil, e
		}
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	var id Identity
	if err = json.Unmarshal(raw, &id); err != nil {
		return nil, err
	}
	if _, err = Roots(id.CA); err != nil {
		return nil, err
	}
	ca, err := Certificate(id.CA)
	if err != nil {
		return nil, err
	}
	key, err := privateKey(id.CAKey)
	if err != nil {
		return nil, err
	}
	if !key.Public().(ed25519.PublicKey).Equal(ca.PublicKey) {
		return nil, fmt.Errorf("Server CA key mismatch")
	}
	if err = ca.CheckSignatureFrom(ca); err != nil {
		return nil, err
	}
	if !now.Before(ca.NotAfter) || now.Before(ca.NotBefore) {
		return nil, fmt.Errorf("Server CA expired or not yet valid")
	}
	if _, err = tls.X509KeyPair(id.Certificate, id.PrivateKey); err != nil {
		return nil, err
	}
	leaf, err := Certificate(id.Certificate)
	if err != nil {
		return nil, err
	}
	if err = leaf.CheckSignatureFrom(ca); err != nil {
		return nil, err
	}
	if !now.Before(leaf.NotAfter.Add(-30 * 24 * time.Hour)) {
		if err = id.renewServer(now); err != nil {
			return nil, err
		}
		if err = writePrivate(path, &id); err != nil {
			return nil, err
		}
	}
	if err = privatefile.Protect(path); err != nil {
		return nil, err
	}
	return &id, nil
}

func writePrivate(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = privatefile.Protect(f.Name()); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func NewCredentials(ca []byte) (*Credentials, []byte, error) {
	if _, err := Roots(ca); err != nil {
		return nil, nil, err
	}
	_, key, err := keyPair()
	if err != nil {
		return nil, nil, err
	}
	signer, err := privateKey(key)
	if err != nil {
		return nil, nil, err
	}
	csr, err := csrForKey(signer)
	if err != nil {
		return nil, nil, err
	}
	return &Credentials{CA: append([]byte(nil), ca...), PrivateKey: key}, csr, nil
}

func csrForKey(key crypto.Signer) ([]byte, error) {
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "AgentUse device"}}, key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

func parseDeviceCSR(csr []byte) (*x509.CertificateRequest, error) {
	block, rest := pem.Decode(csr)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(rest) != 0 {
		return nil, fmt.Errorf("invalid device certificate request")
	}
	request, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, err
	}
	if _, ok := request.PublicKey.(ed25519.PublicKey); !ok {
		return nil, fmt.Errorf("Ed25519 device key required")
	}
	if err := request.CheckSignature(); err != nil {
		return nil, err
	}
	return request, nil
}

func (id *Identity) Issue(csr []byte, device string, generation int64, now time.Time) ([]byte, error) {
	if device == "" || generation < 1 {
		return nil, fmt.Errorf("invalid device certificate identity")
	}
	r, err := parseDeviceCSR(csr)
	if err != nil {
		return nil, err
	}

	ca, err := Certificate(id.CA)
	if err != nil {
		return nil, err
	}
	key, err := privateKey(id.CAKey)
	if err != nil {
		return nil, err
	}
	sn, err := serial()
	if err != nil {
		return nil, err
	}
	uri := &url.URL{Scheme: "agentuse", Host: "device", Path: "/" + device, RawQuery: "generation=" + strconv.FormatInt(generation, 10)}
	cert := &x509.Certificate{SerialNumber: sn, Subject: pkix.Name{CommonName: device}, URIs: []*url.URL{uri}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(90 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, ca, r.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func VerifyDevice(ca, raw []byte, now time.Time) (Peer, error) {
	cert, err := Certificate(raw)
	if err != nil {
		return Peer{}, err
	}
	roots, err := Roots(ca)
	if err != nil {
		return Peer{}, err
	}
	if _, err = cert.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return Peer{}, err
	}
	if len(cert.URIs) != 1 || cert.URIs[0].Scheme != "agentuse" || cert.URIs[0].Host != "device" {
		return Peer{}, fmt.Errorf("invalid device certificate identity")
	}
	uri := cert.URIs[0]
	gen, err := strconv.ParseInt(uri.Query().Get("generation"), 10, 64)
	if err != nil || gen < 1 || len(uri.Path) < 2 || cert.Subject.CommonName != uri.Path[1:] {
		return Peer{}, fmt.Errorf("invalid device generation")
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return Peer{DeviceID: uri.Path[1:], Generation: gen, Serial: cert.SerialNumber.Text(16), KeyHash: hex.EncodeToString(sum[:]), NotAfter: cert.NotAfter, Certificate: cert}, nil
}

func (c *Credentials) SetCertificate(raw []byte, now time.Time) (Peer, error) {
	p, err := VerifyDevice(c.CA, raw, now)
	if err != nil {
		return p, err
	}
	if _, err = tls.X509KeyPair(raw, c.PrivateKey); err != nil {
		return Peer{}, err
	}
	c.Certificate = append([]byte(nil), raw...)
	return p, nil
}

func (c *Credentials) Save(path string) error { return writePrivate(path, c) }
func LoadCredentials(path string) (*Credentials, error) {
	if err := privatefile.Protect(path); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Credentials
	if err = json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	if _, err = c.SetCertificate(c.Certificate, time.Now()); err != nil {
		return nil, err
	}
	return &c, nil
}
