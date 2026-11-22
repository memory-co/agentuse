package upgrade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	projectrelease "github.com/memory-co/agentuse/internal/release"
)

const (
	manifestTimeout = 6 * time.Second
	responseTimeout = 8 * time.Second
	transferIdle    = 15 * time.Second
)

func artifactNames() []string {
	return []string{"agentuse-linux-amd64", "agentuse-linux-arm64", "agentuse-darwin-amd64", "agentuse-darwin-arm64", "agentuse-server-" + runtime.GOOS + "-" + runtime.GOARCH}
}

func assetURL(base, tag, name string) string {
	return strings.TrimRight(base, "/") + "/" + url.PathEscape(tag) + "/" + url.PathEscape(name)
}

type downloadSource struct {
	base     string
	manifest []byte
	sums     map[string]string
	elapsed  time.Duration
}

func (s *Service) probeSource(ctx context.Context, base, tag, digest string, names []string) (downloadSource, error) {
	ctx, cancel := context.WithTimeout(ctx, manifestTimeout)
	defer cancel()
	start := time.Now()
	source := downloadSource{base: base}
	resp, err := s.request(ctx, assetURL(base, tag, "SHA256SUMS"))
	if err != nil {
		return source, err
	}
	defer resp.Body.Close()
	source.manifest, err = io.ReadAll(io.LimitReader(resp.Body, projectrelease.MaxChecksumBytes+1))
	if err != nil {
		return source, err
	}
	if digest != "" && !strings.EqualFold(digest, fmt.Sprintf("sha256:%x", sha256.Sum256(source.manifest))) {
		return source, errors.New("SHA256SUMS 与 GitHub 发布摘要不符")
	}
	source.sums, err = projectrelease.Checksums(bytes.NewReader(source.manifest))
	if err != nil {
		return source, err
	}
	for _, name := range names {
		if source.sums[name] == "" {
			return source, fmt.Errorf("SHA256SUMS 缺少 %s", name)
		}
	}
	source.elapsed = time.Since(start)
	return source, nil
}

func (s *Service) selectSources(ctx context.Context, tag, digest string, names []string) ([]downloadSource, downloadSource, error) {
	bases := append([]string{s.config.DownloadURL}, s.config.MirrorURLs...)
	type probeResult struct {
		source downloadSource
		err    error
	}
	probes := make([]probeResult, len(bases))
	var pending sync.WaitGroup
	for i, base := range bases {
		pending.Go(func() { probes[i].source, probes[i].err = s.probeSource(ctx, base, tag, digest, names) })
	}
	pending.Wait()
	if err := ctx.Err(); err != nil {
		return nil, downloadSource{}, err
	}
	var ready []downloadSource
	var failures []error
	for _, probe := range probes {
		if probe.err == nil {
			ready = append(ready, probe.source)
		} else {
			failures = append(failures, fmt.Errorf("%s: %w", probe.source.base, probe.err))
		}
	}
	if len(ready) == 0 {
		return nil, downloadSource{}, fmt.Errorf("无法读取版本 %s 的 SHA256SUMS: %w", tag, errors.Join(failures...))
	}
	sort.SliceStable(ready, func(i, j int) bool { return ready[i].elapsed < ready[j].elapsed })
	// Validate every attempt against one manifest, preferring GitHub when reachable.
	manifest := ready[0]
	if probes[0].err == nil {
		manifest = probes[0].source
	}
	return ready, manifest, nil
}

func (s *Service) download(ctx context.Context, id string, r release) (Restart, error) {
	plan := Restart{ID: id, Directory: filepath.Join(s.config.Root, id), Executable: s.config.Executable}
	names := artifactNames()
	assets := map[string]releaseAsset{}
	for _, asset := range r.Assets {
		assets[asset.Name] = asset
	}
	var total int64
	for _, name := range names {
		size := assets[name].Size
		if size <= 0 || size > 1<<30 {
			return plan, fmt.Errorf("版本 %s 缺少有效安装包 %s", r.Tag, name)
		}
		total += size
	}
	if err := os.MkdirAll(plan.Directory, 0o700); err != nil {
		return plan, err
	}
	if err := s.update(id, map[string]any{"status": "downloading", "total_bytes": total}); err != nil {
		return plan, err
	}
	sources, manifest, err := s.selectSources(ctx, r.Tag, assets["SHA256SUMS"].Digest, names)
	if err != nil {
		return plan, err
	}
	if err = os.WriteFile(filepath.Join(plan.Directory, "SHA256SUMS"), manifest.manifest, 0o600); err != nil {
		return plan, err
	}
	var downloaded int64
	for _, name := range names {
		asset := artifact{name: name, size: assets[name].Size, digest: manifest.sums[name]}
		if err = s.downloadArtifact(ctx, plan, r.Tag, sources, asset, downloaded); err != nil {
			return plan, err
		}
		downloaded += asset.size
		if err = s.update(id, map[string]any{"downloaded_bytes": downloaded}); err != nil {
			return plan, err
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(probeCtx, filepath.Join(plan.Directory, names[len(names)-1]), "version").Output()
	if err != nil {
		return plan, fmt.Errorf("新版 Server 无法运行: %w", err)
	}
	if strings.TrimSpace(string(output)) != r.Tag {
		return plan, errors.New("新版 Server 的版本号与发布版本不一致")
	}
	return plan, nil
}

type artifact struct {
	name, digest string
	size         int64
}

func (s *Service) downloadArtifact(ctx context.Context, plan Restart, tag string, sources []downloadSource, asset artifact, downloaded int64) error {
	var failures []error
	for i, source := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := s.downloadFrom(ctx, plan, assetURL(source.base, tag, asset.name), asset, downloaded)
		if err == nil {
			// A source that worked after a failover gets the next file first.
			copy(sources[1:i+1], sources[:i])
			sources[0] = source
			return nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", source.base, err))
		if err = s.update(plan.ID, map[string]any{"downloaded_bytes": downloaded}); err != nil {
			return err
		}
	}
	return fmt.Errorf("下载 %s: %w", asset.name, errors.Join(failures...))
}

func (s *Service) downloadFrom(ctx context.Context, plan Restart, address string, asset artifact, downloaded int64) (err error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	timer := time.AfterFunc(responseTimeout, func() { cancel(context.DeadlineExceeded) })
	defer timer.Stop()
	resp, err := s.request(ctx, address)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	timer.Reset(transferIdle)
	path := filepath.Join(plan.Directory, asset.name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, file.Close())
		if err != nil {
			err = errors.Join(err, os.Remove(path))
		}
	}()
	hash := sha256.New()
	progress := &downloadProgress{service: s, id: plan.ID, base: downloaded, size: asset.size, last: time.Now(), timer: timer}
	n, err := io.Copy(io.MultiWriter(file, hash, progress), io.LimitReader(resp.Body, asset.size+1))
	timer.Stop()
	if err != nil {
		return err
	}
	if n != asset.size {
		return errors.New("下载长度不符")
	}
	if hex.EncodeToString(hash.Sum(nil)) != asset.digest {
		return errors.New("SHA256 校验失败")
	}
	return file.Sync()
}

type downloadProgress struct {
	service         *Service
	id              string
	base, size, got int64
	last            time.Time
	timer           *time.Timer
}

func (p *downloadProgress) Write(data []byte) (int, error) {
	p.timer.Reset(transferIdle)
	p.got += int64(len(data))
	if time.Since(p.last) >= 500*time.Millisecond {
		if err := p.service.update(p.id, map[string]any{"downloaded_bytes": p.base + min(p.got, p.size)}); err != nil {
			return 0, err
		}
		p.last = time.Now()
	}
	return len(data), nil
}
