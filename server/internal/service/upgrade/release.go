package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	projectrelease "github.com/memory-co/agentuse/internal/release"
	"github.com/memory-co/agentuse/internal/version"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

// These defaults are fixed to this project's official releases. Tests may inject
// a local release server through Config, including linker overrides for binaries.
var githubAPIURL = "https://api.github.com/repos/memory-co/agentuse/releases/latest"
var githubDownloadURL = projectrelease.GitHubDownloadURL

type releaseAsset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}
type release struct {
	Tag        string         `json:"tag_name"`
	URL        string         `json:"html_url"`
	Notes      string         `json:"body"`
	Published  string         `json:"published_at"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

func (s *Service) request(ctx context.Context, address string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "agentuse-server/"+s.config.Version)
	req.Header.Set("Accept", "application/octet-stream")
	if address == s.config.APIURL {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	response, err := s.config.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return response, nil
}

func (s *Service) fetchRelease(ctx context.Context) (release, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var out release
	resp, err := s.request(ctx, s.config.APIURL)
	if err != nil {
		return out, fmt.Errorf("查询 GitHub 最新版本: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return out, err
	}
	if len(b) > 1<<20 {
		return out, errors.New("GitHub 版本信息过大")
	}
	if err = json.Unmarshal(b, &out); err != nil {
		return out, fmt.Errorf("GitHub 版本信息无效: %w", err)
	}
	if !version.Stable(out.Tag) || out.Draft || out.Prerelease {
		return out, errors.New("GitHub 未返回有效的正式版本")
	}
	return out, nil
}
func (s *Service) Latest(ctx context.Context) (agentuse.ServerRelease, error) {
	r, err := s.fetchRelease(ctx)
	return agentuse.ServerRelease{Version: r.Tag, CurrentVersion: s.config.Version, Available: version.Newer(r.Tag, s.config.Version), URL: r.URL, Notes: r.Notes, PublishedAt: r.Published}, err
}

func (s *Service) LatestClient(ctx context.Context) (agentuse.ClientRelease, error) {
	s.clientReleaseMu.Lock()
	defer s.clientReleaseMu.Unlock()
	if time.Now().Before(s.clientReleaseExpires) {
		return s.clientRelease, nil
	}
	r, err := s.fetchRelease(ctx)
	if err != nil {
		return agentuse.ClientRelease{}, err
	}
	s.clientRelease = agentuse.ClientRelease{Version: r.Tag}
	s.clientReleaseExpires = time.Now().Add(5 * time.Minute)
	return s.clientRelease, nil
}
