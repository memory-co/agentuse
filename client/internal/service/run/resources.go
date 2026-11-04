package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/client/internal/fileutil"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/session"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/skillbundle"
)

func normalizeLocalResources(r *Request) error {
	if r.agentVersionID != "" {
		return nil
	}
	if len(r.SkillPackages) > 64 {
		return fmt.Errorf("too many skills")
	}
	hashes := []string{}
	for _, name := range r.SkillPackages {
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, skillbundle.MaxArchive+1))
		f.Close()
		if err != nil {
			return err
		}
		meta, err := skillbundle.Inspect(data)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		hash := hex.EncodeToString(digest[:])
		hashes = append(hashes, hash)
		r.localPackages = append(r.localPackages, data)
		r.skills = append(r.skills, protocol.SkillResource{Name: meta.Name, SHA256: hash, SizeBytes: int64(len(data))})
	}
	config, _ := json.Marshal(struct {
		Harness, Model, Instructions string
		Skills                       []string
	}{r.Harness, r.Model, r.AgentInstructions, hashes})
	r.agentVersionID = uuid.NewSHA1(uuid.NameSpaceURL, config).String()
	return nil
}
func (s *Service) prepareResources(ctx context.Context, m session.Session, r Request, a *harness.Adapter) (func(), error) {
	noop := func() {}
	// A retained native session owns its initialized snapshot. Never re-download latest.
	if _, err := os.Stat(a.EventsPath); err == nil {
		_, err = s.Harness.Status(ctx, *a, a.EventsPath)
		return noop, err
	} else if !os.IsNotExist(err) {
		return noop, err
	}
	if r.Model != "" {
		a.InitArgs = append(a.InitArgs, "--model", r.Model)
	}
	if r.AgentInstructions == "" && len(r.skills) == 0 {
		return noop, nil
	}
	dir, err := fileutil.SafePath(s.Sessions.Root, m.EndpointID, m.ConversationID, "sessions", m.ID, "init-resources")
	if err != nil {
		return noop, err
	}
	if err = os.RemoveAll(dir); err != nil {
		return noop, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return noop, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	fail := func(err error) (func(), error) { cleanup(); return noop, err }
	if r.AgentInstructions != "" {
		name := filepath.Join(dir, "AGENTS.md")
		if err = os.WriteFile(name, []byte(r.AgentInstructions), 0600); err != nil {
			return fail(err)
		}
		a.InitArgs = append(a.InitArgs, "--agents-md", name)
	}
	names := map[string]bool{}
	for index, resource := range r.skills {
		if !fileutil.Segment(resource.Name) || names[strings.ToLower(resource.Name)] {
			return fail(fmt.Errorf("duplicate/invalid skill name"))
		}
		names[strings.ToLower(resource.Name)] = true
		var data []byte
		if m.EndpointID == "local" {
			data = r.localPackages[index]
		} else {
			if resource.SizeBytes <= 0 || resource.SizeBytes > skillbundle.MaxArchive || uuid.Validate(resource.SkillID) != nil || uuid.Validate(resource.SkillVersionID) != nil {
				return fail(fmt.Errorf("invalid skill package descriptor"))
			}
			expected := "/api/skills/" + resource.SkillID + "/versions/" + resource.SkillVersionID + "/package"
			if resource.DownloadPath != expected {
				return fail(fmt.Errorf("skill download must use this endpoint's version API"))
			}
			ep, e := s.Endpoints.GetByID(m.EndpointID)
			if e != nil {
				return fail(e)
			}
			request, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(ep.ServerURL, "/")+expected, nil)
			if e != nil {
				return fail(e)
			}
			request.Header.Set("Authorization", "Bearer "+ep.DeviceKey)
			response, e := (&http.Client{Timeout: time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}).Do(request)
			if e != nil {
				return fail(e)
			}
			if response.StatusCode != 200 {
				response.Body.Close()
				return fail(fmt.Errorf("skill download: HTTP %d", response.StatusCode))
			}
			data, e = io.ReadAll(io.LimitReader(response.Body, resource.SizeBytes+1))
			response.Body.Close()
			if e != nil {
				return fail(e)
			}
		}
		digest := sha256.Sum256(data)
		if int64(len(data)) != resource.SizeBytes || hex.EncodeToString(digest[:]) != resource.SHA256 {
			return fail(fmt.Errorf("skill package size/digest mismatch"))
		}
		destination := filepath.Join(dir, "skills", resource.Name)
		if err = os.MkdirAll(destination, 0700); err != nil {
			return fail(err)
		}
		metadata, e := skillbundle.Extract(data, destination)
		if e != nil {
			return fail(e)
		}
		if metadata.Name != resource.Name {
			return fail(fmt.Errorf("skill package name mismatch"))
		}
	}
	if len(r.skills) > 0 {
		a.InitArgs = append(a.InitArgs, "--skills-dir", filepath.Join(dir, "skills"))
	}
	return cleanup, nil
}
