package server

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSetupScript(t *testing.T) {
	e := newEnv(t, Config{})
	resp, err := http.Get(e.ts.URL + "/setup.sh")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	script := string(b)
	if !strings.Contains(script, `SERVER="${AGENTUSE_SERVER:-`+e.ts.URL+`}"`) || strings.Contains(script, "__SERVER__") ||
		resp.Header.Get("Cache-Control") != "no-store" || !strings.HasPrefix(script, "#!/usr/bin/env bash") {
		t.Fatalf("setup.sh: %v\n%s", resp.Header, script)
	}
}

func TestDownload(t *testing.T) {
	dist := t.TempDir()
	os.WriteFile(filepath.Join(dist, "agentuse-linux-amd64"), []byte("BIN"), 0o755)
	os.WriteFile(filepath.Join(dist, "secret.txt"), []byte("no"), 0o644)
	e := newEnv(t, Config{DistDir: dist})

	get := func(name string) (int, string) {
		resp, err := http.Get(e.ts.URL + "/download/" + name)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get("agentuse-linux-amd64"); code != 200 || body != "BIN" {
		t.Fatalf("download: %d %q", code, body)
	}
	if code, body := get("agentuse-darwin-arm64"); code != 404 || !strings.Contains(body, "darwin/arm64") {
		t.Fatalf("missing platform: %d %q", code, body)
	}
	// 只给 agentuse-<os>-<arch>,别的文件拿不到
	for _, name := range []string{"secret.txt", "agentuse-windows-amd64", "..%2Fsecret.txt"} {
		if code, _ := get(name); code != 404 {
			t.Fatalf("%s: %d", name, code)
		}
	}
	// 没配 dist 目录
	e2 := newEnv(t, Config{})
	if resp, _ := http.Get(e2.ts.URL + "/download/agentuse-linux-amd64"); resp.StatusCode != 404 {
		t.Fatalf("no dist: %d", resp.StatusCode)
	}
}

func TestMissingDist(t *testing.T) {
	if got := MissingDist(""); !reflect.DeepEqual(got, Platforms) {
		t.Fatalf("empty dir: %v", got)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "agentuse-linux-amd64"), nil, 0o755)
	os.Mkdir(filepath.Join(dir, "agentuse-darwin-arm64"), 0o755) // 目录不算
	if got := MissingDist(dir); !reflect.DeepEqual(got, []string{"linux-arm64", "darwin-amd64", "darwin-arm64"}) {
		t.Fatalf("missing: %v", got)
	}
}
