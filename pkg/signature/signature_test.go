package signature

import (
	"net/http"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"thread":"issue:MUL-42"}`)
	h := http.Header{}
	SetHeaders(h, "s3cret", body, now)

	if err := Verify(h, "s3cret", body, now.Add(time.Minute)); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := Verify(h, "other", body, now); err != ErrInvalid {
		t.Fatalf("wrong secret: got %v", err)
	}
	if err := Verify(h, "s3cret", []byte(`{}`), now); err != ErrInvalid {
		t.Fatalf("tampered body: got %v", err)
	}
	if err := Verify(h, "s3cret", body, now.Add(time.Hour)); err != ErrExpired {
		t.Fatalf("replay: got %v", err)
	}
	if err := Verify(http.Header{}, "s3cret", body, now); err != ErrMissing {
		t.Fatalf("missing: got %v", err)
	}
}
