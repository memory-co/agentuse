package securews

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEnrollmentProofBindsImportedKeyToCurrentConnection(t *testing.T) {
	token, hash, err := GenerateDeviceKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := ImportDeviceKey(token)
	if err != nil {
		t.Fatal(err)
	}
	policy := Policy{ServerNonce: "first", Connection: "connection-1"}
	proof, err := key.EnrollmentProof(policy, "host")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := VerifyEnrollment(policy, proof); err != nil || got != hash {
		t.Fatal("proof did not identify the provisioned public key", err)
	}
	raw, err := json.Marshal(proof)
	if err != nil || strings.Contains(string(raw), token) || strings.Contains(string(raw), `"token"`) {
		t.Fatal("enrollment serialized a private key or token", err)
	}
	policy.ServerNonce = "second"
	if _, err := VerifyEnrollment(policy, proof); err == nil {
		t.Fatal("proof was accepted on another connection")
	}
}
