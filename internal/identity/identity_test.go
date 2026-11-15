package identity

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestCredentialAndIdentifierFormats(t *testing.T) {
	for _, test := range []struct {
		prefix string
		bytes  int
		make   func(string) string
	}{
		{"ut_", 24, NewToken},
		{"st_", 24, NewToken},
		{"dk_", 24, NewToken},
		{"dev_", 8, NewID},
		{"ag_", 8, NewID},
		{"at_", 8, NewID},
	} {
		t.Run(test.prefix, func(t *testing.T) {
			first, second := test.make(test.prefix), test.make(test.prefix)
			if first == second || !strings.HasPrefix(first, test.prefix) {
				t.Fatal("identifiers must be independently generated and retain their prefix")
			}
			value, err := hex.DecodeString(strings.TrimPrefix(first, test.prefix))
			if err != nil || len(value) != test.bytes {
				t.Fatal("identifier format changed", len(value), err)
			}
		})
	}
}

func TestStoredCredentialFingerprint(t *testing.T) {
	// Existing persisted passwords and keys must retain the same fingerprint.
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := HashSecret("abc"); got != want {
		t.Fatal("stored credential hash changed")
	}
}
