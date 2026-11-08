package run

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func (d *endpointConnection) resolveResources(ctx context.Context, p protocol.ConversationMessage) (Request, error) {
	root := filepath.Join(d.cfg.RunService.Sessions.Root, d.cfg.EndpointID, p.ConversationID, ".server", "agents")
	if !fileutil.Segment(p.AgentVersionID) {
		return Request{}, fileutil.ErrInvalid
	}
	path := filepath.Join(root, p.AgentVersionID+".json")
	client := agentuse.New(d.cfg.Server, d.cfg.Key)
	client.HTTPClient = &http.Client{Timeout: time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	var version agentuse.AgentVersion
	raw, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(raw, &version); err != nil {
			return Request{}, err
		}
	} else if os.IsNotExist(err) {
		v, e := client.GetAgentVersion(ctx, "", p.AgentID, p.AgentVersionID)
		if e != nil {
			return Request{}, e
		}
		version = *v
	} else {
		return Request{}, err
	}
	if version.ID != p.AgentVersionID || version.AgentID != p.AgentID || !fileutil.Segment(version.Harness) || len(version.Skills) > 64 {
		return Request{}, fmt.Errorf("Agent version identity mismatch")
	}
	if d.cfg.DeviceID != "" && version.DeviceID != d.cfg.DeviceID {
		return Request{}, fmt.Errorf("Agent version belongs to another device")
	}
	if err = fileutil.WriteJSON(path, version); err != nil {
		return Request{}, err
	}
	request := Request{Harness: version.Harness, Model: version.Model, AgentInstructions: version.Instructions}
	// Existing sessions own their resource snapshot; metadata/API caches are only
	// needed when preparing a fresh address, not every subsequent send.
	sessions, err := d.cfg.RunService.Sessions.Sessions(ctx, d.cfg.EndpointID, p.ConversationID)
	if err != nil {
		return request, err
	}
	adapter, err := d.cfg.Harness.Resolve(version.Harness)
	if err != nil {
		return request, err
	}
	for _, s := range sessions {
		if s.AgentID == p.AgentID && s.AgentVersionID == p.AgentVersionID && s.Harness == version.Harness && s.HarnessRevision == adapter.Revision && (s.Status == "idle" || s.Status == "running") {
			if _, err = os.Stat(filepath.Join(d.sessionDir(s), "events.jsonl")); err == nil {
				return request, nil
			}
		}
	}
	seen := map[string]bool{}
	for _, ref := range version.Skills {
		if !fileutil.Segment(ref.SkillID) || !fileutil.Segment(ref.SkillVersionID) || seen[ref.SkillID] {
			return request, fmt.Errorf("invalid fixed skill references")
		}
		seen[ref.SkillID] = true
		skillPath := filepath.Join(root, "skills", ref.SkillVersionID+".json")
		var skill agentuse.SkillVersion
		data, e := os.ReadFile(skillPath)
		if e == nil {
			if e = json.Unmarshal(data, &skill); e != nil {
				return request, e
			}
		} else if os.IsNotExist(e) {
			v, e := client.GetSkillVersion(ctx, "", ref.SkillID, ref.SkillVersionID)
			if e != nil {
				return request, e
			}
			skill = *v
		} else {
			return request, e
		}
		if skill.ID != ref.SkillVersionID || skill.SkillID != ref.SkillID {
			return request, fmt.Errorf("skill version identity mismatch")
		}
		if err = fileutil.WriteJSON(skillPath, skill); err != nil {
			return request, err
		}
		request.skills = append(request.skills, protocol.SkillResource{SkillID: skill.SkillID, SkillVersionID: skill.ID, Name: skill.Name, SHA256: skill.SHA256, SizeBytes: skill.SizeBytes, DownloadPath: skill.DownloadPath})
	}
	return request, nil
}
