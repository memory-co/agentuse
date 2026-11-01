package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/memory-co/agentuse/internal/protocol"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckKey(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer dk_ok":
			json.NewEncoder(w).Encode(protocol.MeResponse{DaemonID: "dm_1", Status: "online"})
		case "Bearer dk_500":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer ts.Close()
	ctx := context.Background()
	if me, err := CheckKey(ctx, Endpoint{ServerURL: ts.URL + "/", DeviceKey: "dk_ok"}); err != nil || me.DaemonID != "dm_1" {
		t.Fatalf("ok: %v %+v", err, me)
	}
	if _, err := CheckKey(ctx, Endpoint{ServerURL: ts.URL, DeviceKey: "dk_bad"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("401: %v", err)
	}
	if _, err := CheckKey(ctx, Endpoint{ServerURL: ts.URL, DeviceKey: "dk_500"}); err == nil || errors.Is(err, ErrUnauthorized) {
		t.Fatalf("500: %v", err)
	}
}
