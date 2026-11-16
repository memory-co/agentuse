package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	upgradeservice "github.com/memory-co/agentuse/server/internal/service/upgrade"
)

func TestUpgradeEndpointsAreAdminOnly(t *testing.T) {
	e := newEnv(t, Config{})
	_, admin := e.user("admin", agentuse.RoleAdmin)
	_, member := e.user("member", agentuse.RoleUser)
	for _, path := range []string{"/api/upgrades", "/api/upgrades/latest", "/api/upgrades/example"} {
		if code := e.code("", "GET", path, nil); code != 401 {
			t.Fatal(path, code)
		}
		if code := e.code(member, "GET", path, nil); code != 403 {
			t.Fatal(path, code)
		}
	}
	if code := e.code(member, "POST", "/api/upgrades", nil); code != 403 {
		t.Fatal(code)
	}
	var state agentuse.ServerUpgradeState
	if resp := e.req(admin, "GET", "/api/upgrades", nil, &state); resp.StatusCode != 200 || state.Supported || len(state.Upgrades) != 0 {
		t.Fatal(state, resp.StatusCode)
	}
	if code := e.code(admin, "POST", "/api/upgrades", nil); code != 503 {
		t.Fatal(code)
	}
	if code := e.code(admin, "GET", "/api/upgrades/missing", nil); code != 404 {
		t.Fatal(code)
	}
	if code := e.code(admin, "GET", "/api/upgrades?limit=-1", nil); code != 400 {
		t.Fatal(code)
	}
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"tag_name": "v9.1.0", "html_url": "https://github.com/memory-co/agentuse/releases/tag/v9.1.0"})
	}))
	defer github.Close()
	if err := e.st.ConfigureUpgrade(upgradeservice.Config{APIURL: github.URL, Version: "v9.0.0"}); err != nil {
		t.Fatal(err)
	}
	var release agentuse.ServerRelease
	if resp := e.req(admin, "GET", "/api/upgrades/latest", nil, &release); resp.StatusCode != 200 || !release.Available || release.Version != "v9.1.0" {
		t.Fatal(release, resp.StatusCode)
	}
}
