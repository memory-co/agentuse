// Package identity generates application identifiers and credentials.
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// HashSecret returns the stored fingerprint of a password, token or device key.
func HashSecret(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(digest[:])
}

// NewToken creates a prefixed credential containing 24 cryptographically random bytes.
func NewToken(prefix string) string {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(value)
}

// NewID creates a prefixed application identifier containing 8 random bytes.
func NewID(prefix string) string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(value)
}
