package endpoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEndpointYAMLOnlyStoresAddressAndRegistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endpoints.yaml")
	svc := Service{Path: path}
	for _, ep := range []Endpoint{{Name: "one", ServerURL: "https://Company.example.com:443/", Enabled: true, DeviceID: "d1", DeviceKey: "key1"}, {Name: "two", ServerURL: "http://localhost:8090", Enabled: true, DeviceID: "d2", DeviceKey: "key2"}} {
		if e := svc.Save(ep); e != nil {
			t.Fatal(e)
		}
	}
	all, e := svc.Registrations()
	if e != nil || len(all) != 2 || all[0].ID() != "https---company.example.com" || all[1].ID() != "http---localhost%3A8090" {
		t.Fatal(all, e)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "endpoint_id:") || strings.Contains(string(b), "\n    id:") {
		t.Fatal(string(b))
	}
	ep := all[0]
	ep.DeviceKey = "new-key"
	if e = svc.Save(ep); e != nil {
		t.Fatal(e)
	}
	all, _ = svc.Registrations()
	if len(all) != 2 || all[0].DeviceKey != "new-key" {
		t.Fatal(all)
	}
}

func TestBuiltinLocalIsAnEndpointNotARegistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endpoints.yaml")
	service := Service{Path: path}
	all, e := service.List()
	if e != nil || len(all) != 1 || all[0].ID() != LocalID || !all[0].IsLocal() {
		t.Fatal(all, e)
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("listing local wrote a registration", e)
	}
	if e = service.Save(Local()); e == nil {
		t.Fatal("builtin local was saved")
	}
	if e = service.Remove(LocalID); e == nil {
		t.Fatal("builtin local was removed")
	}
	remote := Endpoint{Name: "platform", ServerURL: "https://example.com", Enabled: true, DeviceID: "d", DeviceKey: "secret-key"}
	if e = service.Save(remote); e != nil {
		t.Fatal(e)
	}
	all, e = service.List()
	if e != nil || len(all) != 2 || all[1].IsLocal() {
		t.Fatal(all, e)
	}
	ep, e := service.GetByID(LocalID)
	if e != nil || !ep.IsLocal() {
		t.Fatal(ep, e)
	}
	views, e := service.Views()
	if e != nil {
		t.Fatal(e)
	}
	data, _ := json.Marshal(views)
	if strings.Contains(string(data), "secret-key") {
		t.Fatal("views exposed device credentials")
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "本地调试") || strings.Contains(string(b), "mode:") {
		t.Fatal("builtin data persisted", string(b))
	}
}
