package server

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/memory-co/agentuse/frontend"
)

func TestWeb(t *testing.T) {
	e := newEnv(t, Config{})
	resp, err := http.Get(e.ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if frontend.Built() {
		if resp.StatusCode != 200 || !strings.Contains(string(b), "<html") {
			t.Fatalf("built web: %d", resp.StatusCode)
		}
		return
	}
	// 没构建前端时给提示,API 照常
	if resp.StatusCode != 404 || !strings.Contains(string(b), "npm run build") {
		t.Fatalf("unbuilt web: %d %s", resp.StatusCode, b)
	}
}
