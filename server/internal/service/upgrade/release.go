package upgrade

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

// These defaults are fixed to this project's official releases. Tests may inject
// a local release server through Config, including linker overrides for binaries.
var githubAPIURL = "https://api.github.com/repos/memory-co/agentuse/releases/latest"
var githubDownloadURL = "https://github.com/memory-co/agentuse/releases/download"

type releaseAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
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

var stableVersion = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

func versionNumbers(value string) ([]uint64, bool) {
	match := stableVersion.FindStringSubmatch(value)
	if match == nil {
		return nil, false
	}
	numbers := make([]uint64, 3)
	for i := range numbers {
		var err error
		numbers[i], err = strconv.ParseUint(match[i+1], 10, 64)
		if err != nil {
			return nil, false
		}
	}
	return numbers, true
}
func newer(target, current string) bool {
	next, ok := versionNumbers(target)
	if !ok {
		return false
	}
	previous, ok := versionNumbers(current)
	if !ok {
		return false
	}
	for i := range next {
		if next[i] != previous[i] {
			return next[i] > previous[i]
		}
	}
	return false
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
		return nil, fmt.Errorf("GitHub 请求失败: HTTP %d", response.StatusCode)
	}
	return response, nil
}

func (s *Service) fetchRelease(ctx context.Context) (release, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var out release
	resp, err := s.request(ctx, s.config.APIURL)
	if err != nil {
		return out, err
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
	if _, ok := versionNumbers(out.Tag); !ok || out.Draft || out.Prerelease {
		return out, errors.New("GitHub 未返回有效的正式版本")
	}
	return out, nil
}
func (s *Service) Latest(ctx context.Context) (agentuse.ServerRelease, error) {
	r, err := s.fetchRelease(ctx)
	return agentuse.ServerRelease{Version: r.Tag, CurrentVersion: s.config.Version, Available: newer(r.Tag, s.config.Version), URL: r.URL, Notes: r.Notes, PublishedAt: r.Published}, err
}

func artifactNames() []string {
	return []string{"agentuse-linux-amd64", "agentuse-linux-arm64", "agentuse-darwin-amd64", "agentuse-darwin-arm64", "agentuse-server-" + runtime.GOOS + "-" + runtime.GOARCH}
}
func (s *Service) assetURL(tag, name string) string {
	return strings.TrimRight(s.config.DownloadURL, "/") + "/" + url.PathEscape(tag) + "/" + url.PathEscape(name)
}

func (s *Service) download(ctx context.Context, id string, r release) (Restart, error) {
	plan := Restart{ID: id, Directory: filepath.Join(s.config.Root, id), Executable: s.config.Executable}
	sizes := map[string]int64{}
	for _, asset := range r.Assets {
		sizes[asset.Name] = asset.Size
	}
	var total int64
	for _, name := range artifactNames() {
		if sizes[name] <= 0 || sizes[name] > 1<<30 {
			return plan, fmt.Errorf("版本 %s 缺少有效安装包 %s", r.Tag, name)
		}
		total += sizes[name]
	}
	if err := os.MkdirAll(plan.Directory, 0o700); err != nil {
		return plan, err
	}
	if err := s.update(id, map[string]any{"status": "downloading", "total_bytes": total}); err != nil {
		return plan, err
	}
	resp, err := s.request(ctx, s.assetURL(r.Tag, "SHA256SUMS"))
	if err != nil {
		return plan, err
	}
	checks, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	resp.Body.Close()
	if err != nil {
		return plan, err
	}
	if len(checks) > 1<<20 {
		return plan, errors.New("SHA256SUMS 文件过大")
	}
	sums := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(checks)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return plan, errors.New("SHA256SUMS 格式无效")
		}
		name := strings.TrimPrefix(fields[1], "*")
		if _, exists := sums[name]; exists {
			return plan, errors.New("SHA256SUMS 包含重复文件")
		}
		digest, e := hex.DecodeString(fields[0])
		if e != nil || len(digest) != sha256.Size {
			return plan, errors.New("SHA256SUMS 摘要无效")
		}
		sums[name] = strings.ToLower(fields[0])
	}
	if err = scanner.Err(); err != nil {
		return plan, err
	}
	if err = os.WriteFile(filepath.Join(plan.Directory, "SHA256SUMS"), checks, 0o600); err != nil {
		return plan, err
	}
	var downloaded int64
	for _, name := range artifactNames() {
		if sums[name] == "" {
			return plan, fmt.Errorf("SHA256SUMS 缺少 %s", name)
		}
		resp, err = s.request(ctx, s.assetURL(r.Tag, name))
		if err != nil {
			return plan, fmt.Errorf("下载 %s: %w", name, err)
		}
		err = func() error {
			defer resp.Body.Close()
			file, e := os.OpenFile(filepath.Join(plan.Directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
			if e != nil {
				return e
			}
			defer file.Close()
			hash := sha256.New()
			progress := &downloadProgress{service: s, id: id, total: &downloaded, last: time.Now()}
			n, e := io.Copy(io.MultiWriter(file, hash, progress), io.LimitReader(resp.Body, sizes[name]+1))
			if e != nil {
				return e
			}
			if n != sizes[name] {
				return fmt.Errorf("%s 下载长度不符", name)
			}
			if hex.EncodeToString(hash.Sum(nil)) != sums[name] {
				return fmt.Errorf("%s SHA256 校验失败", name)
			}
			return file.Sync()
		}()
		if err != nil {
			return plan, err
		}
		if err = s.update(id, map[string]any{"downloaded_bytes": downloaded}); err != nil {
			return plan, err
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(probeCtx, filepath.Join(plan.Directory, artifactNames()[4]), "version").Output()
	if err != nil {
		return plan, fmt.Errorf("新版 Server 无法运行: %w", err)
	}
	if strings.TrimSpace(string(output)) != r.Tag {
		return plan, errors.New("新版 Server 的版本号与发布版本不一致")
	}
	return plan, nil
}

type downloadProgress struct {
	service *Service
	id      string
	total   *int64
	last    time.Time
}

func (p *downloadProgress) Write(data []byte) (int, error) {
	*p.total += int64(len(data))
	if time.Since(p.last) >= 500*time.Millisecond {
		if err := p.service.update(p.id, map[string]any{"downloaded_bytes": *p.total}); err != nil {
			return 0, err
		}
		p.last = time.Now()
	}
	return len(data), nil
}
