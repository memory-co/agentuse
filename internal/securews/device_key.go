package securews

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

const MaxDeviceKeyText = 4096

// GenerateDeviceKey returns the OpenSSH private key body and the fingerprint
// of its public key. Only the fingerprint is needed after the key is handed out.
func GenerateDeviceKey() (string, string, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		return "", "", err
	}
	hash, err := publicKeyHash(public)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(block.Bytes), hash, nil
}

func ImportDeviceKey(body string) (*Credentials, error) {
	if len(body) > MaxDeviceKeyText {
		return nil, fmt.Errorf("device private key is too large")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(strings.Join(strings.Fields(body), ""))
	if err != nil {
		return nil, fmt.Errorf("enter the Ed25519 OpenSSH private key body")
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: raw})
	key, err := ssh.ParseRawPrivateKey(block)
	if err != nil {
		return nil, fmt.Errorf("invalid or encrypted OpenSSH private key")
	}
	private, ok := key.(*ed25519.PrivateKey)
	if !ok || len(*private) != ed25519.PrivateKeySize || !ed25519.NewKeyFromSeed(private.Seed()).Equal(*private) {
		return nil, fmt.Errorf("valid Ed25519 OpenSSH private key required")
	}
	der, err := x509.MarshalPKCS8PrivateKey(*private)
	if err != nil {
		return nil, err
	}
	return &Credentials{PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})}, nil
}

func publicKeyHash(public ed25519.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(der)
	return hex.EncodeToString(hash[:]), nil
}

func (c *Credentials) CSR() ([]byte, error) {
	key, err := privateKey(c.PrivateKey)
	if err != nil {
		return nil, err
	}
	return csrForKey(key)
}

const enrollmentDomain = "agentuse-device-enrollment/v1"

func (c *Credentials) EnrollmentProof(policy Policy, hostname string) (Authentication, error) {
	key, err := privateKey(c.PrivateKey)
	if err != nil {
		return Authentication{}, err
	}
	csr, err := csrForKey(key)
	if err != nil {
		return Authentication{}, err
	}
	signature, err := key.Sign(rand.Reader, signedBytes(enrollmentDomain, policy), crypto.Hash(0))
	if err != nil {
		return Authentication{}, err
	}
	return Authentication{Type: "enroll", CSR: csr, Signature: signature, Hostname: hostname}, nil
}

func VerifyEnrollment(policy Policy, auth Authentication) (string, error) {
	if auth.Type != "enroll" {
		return "", ErrCredential
	}
	request, err := parseDeviceCSR(auth.CSR)
	if err != nil {
		return "", ErrCredential
	}
	public := request.PublicKey.(ed25519.PublicKey)
	if !ed25519.Verify(public, signedBytes(enrollmentDomain, policy), auth.Signature) {
		return "", ErrCredential
	}
	return publicKeyHash(public)
}
