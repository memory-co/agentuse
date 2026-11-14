package controller

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/service"
)

func zipSkill(t *testing.T, text string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.Create("SKILL.md")
	f.Write([]byte("---\nname: review\ndescription: Review tools\n---\n" + text))
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestSkillAPIAndAssignedDeviceDownload(t *testing.T) {
	e := newEnv(t, Config{FilesDir: t.TempDir()})
	ctx := context.Background()
	u, token := e.user("alice", service.RoleUser)
	_, otherToken := e.user("bob", service.RoleUser)
	c := agentuse.New(e.ts.URL, token)
	data := zipSkill(t, "v1")
	skill, err := c.PublishSkill(ctx, "", "", "", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if skill.CurrentVersion.Version != 1 || skill.CurrentVersion.SizeBytes != int64(len(data)) {
		t.Fatal(skill)
	}
	other := agentuse.New(e.ts.URL, otherToken)
	if _, err = other.GetSkillVersion(ctx, "", skill.ID, skill.CurrentVersionID); !agentuse.IsStatus(err, 404) {
		t.Fatal("other user read skill", err)
	}
	d, err := e.st.CreateDevice(ctx, u.ID, "assigned")
	if err != nil {
		t.Fatal(err)
	}
	d, key, err := e.st.RegisterDevice(ctx, d.SetupToken, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.st.DeviceConnected(ctx, d, []service.ReportedHarness{{Name: "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.st.SetDeviceHarnessEnabled(ctx, u, d.ID, "codex", true); err != nil {
		t.Fatal(err)
	}
	device := agentuse.New(e.ts.URL, key)
	if _, err = device.DownloadSkillPackage(ctx, "", skill.ID, skill.CurrentVersionID); !agentuse.IsStatus(err, 404) {
		t.Fatal("unassigned package downloaded", err)
	}
	a, err := c.CreateAgent(ctx, "", agentuse.CreateAgentRequest{ID: "reviewer", Name: "Reviewer", DeviceID: d.ID, Harness: "codex", Instructions: "fixed", Skills: []agentuse.SkillRef{{SkillID: skill.ID, SkillVersionID: skill.CurrentVersionID}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = device.GetAgentVersion(ctx, "", a.ID, a.CurrentVersionID); !agentuse.IsStatus(err, 404) {
		t.Fatal("device read an unassigned Agent version", err)
	}
	if _, err = device.GetSkillVersion(ctx, "", skill.ID, skill.CurrentVersionID); !agentuse.IsStatus(err, 404) {
		t.Fatal("device read unassigned skill metadata", err)
	}
	_, _, err = e.st.CreateConversation(ctx, u, "", "", "test", []string{a.ID}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.st.DispatchMessage(ctx, d)
	if err != nil || p.AgentVersionID != a.CurrentVersionID {
		t.Fatal(p, err)
	}
	fixed, err := device.GetAgentVersion(ctx, "", a.ID, p.AgentVersionID)
	if err != nil || fixed.Instructions != "fixed" || len(fixed.Skills) != 1 || fixed.Skills[0].SkillVersionID != skill.CurrentVersionID {
		t.Fatal("fixed configuration unavailable before accepted", fixed, err)
	}
	meta, err := device.GetSkillVersion(ctx, "", skill.ID, skill.CurrentVersionID)
	if err != nil || meta.SHA256 != skill.CurrentVersion.SHA256 {
		t.Fatal("assigned skill metadata unavailable", meta, err)
	}
	if code := e.req(key, "GET", "/api/agents/"+a.ID+"/versions/"+p.AgentVersionID, nil, nil, actAsHeader, u.ID).StatusCode; code != 403 {
		t.Fatal("device impersonation permitted", code)
	}
	stream, err := device.DownloadSkillPackage(ctx, "", skill.ID, skill.CurrentVersionID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(stream)
	stream.Close()
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("package changed", err)
	}
	if _, err = c.PublishSkill(ctx, "", skill.ID, "wrong", bytes.NewReader(data)); !agentuse.IsStatus(err, 409) {
		t.Fatal("skill conflict", err)
	}
	next, err := c.PublishSkill(ctx, "", skill.ID, skill.CurrentVersionID, bytes.NewReader(zipSkill(t, "v2")))
	if err != nil || next.CurrentVersion.Version != 2 {
		t.Fatal(next, err)
	}
	if _, err = device.DownloadSkillPackage(ctx, "", skill.ID, next.CurrentVersionID); !agentuse.IsStatus(err, 404) {
		t.Fatal("device downloaded unassigned version", err)
	}
	if err = c.DeleteSkill(ctx, "", skill.ID); err != nil {
		t.Fatal(err)
	}
	stream, err = device.DownloadSkillPackage(ctx, "", skill.ID, skill.CurrentVersionID)
	if err != nil {
		t.Fatal("deleted identity broke in-flight resource", err)
	}
	stream.Close()
	vs, err := c.ListAgentVersions(ctx, "", a.ID)
	if err != nil || len(vs) != 1 || vs[0].Skills[0].SkillVersionID != skill.CurrentVersionID {
		t.Fatal(vs, err)
	}
	if code := e.code(token, "PATCH", "/api/agents/"+a.ID, map[string]string{"name": "mutated"}); code != http.StatusMethodNotAllowed {
		t.Fatal("mutable agent route retained", code)
	}
}

func TestConversationFilesUseOwnNamespaceAndCleanup(t *testing.T) {
	e := newEnv(t, Config{FilesDir: t.TempDir()})
	ctx := context.Background()
	u, token := e.user("files", service.RoleUser)
	_, other := e.user("other", service.RoleUser)
	ss, _, err := e.st.CreateConversation(ctx, u, "", "files", "", nil, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/conversations/" + ss.ID + "/files/inputs/data.txt"
	if code := e.code(token, "PUT", path, "hello"); code != 204 {
		t.Fatal(code)
	}
	if code := e.code(other, "GET", path, nil); code != 404 {
		t.Fatal("cross-user file access", code)
	}
	req, _ := http.NewRequest("GET", e.ts.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "hello" {
		t.Fatal(resp.StatusCode, string(body))
	}
	if err = e.st.Files.Put(ctx, "skills", "retained/version/skill.zip", bytes.NewReader([]byte("retained")), true); err != nil {
		t.Fatal(err)
	}
	if code := e.code(token, "DELETE", "/api/conversations/"+ss.ID, nil); code != 204 {
		t.Fatal(code)
	}
	rows, err := e.st.Files.List(ctx, "conversations", ss.ID)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	stream, _, err := e.st.Files.Get(ctx, "skills", "retained/version/skill.zip")
	if err != nil {
		t.Fatal("conversation deletion touched skills", err)
	}
	stream.Close()
}
