package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/memory-co/agentuse/internal/release"
	"github.com/memory-co/agentuse/internal/version"
)

var clientReleaseURL = release.GitHubDownloadURL

type clientUpgrade struct {
	executable string
	directory  string
}

func clientUpgradeSupported() bool {
	return version.Stable(version.Version) && (runtime.GOOS == "linux" || runtime.GOOS == "darwin") && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64")
}

func prepareClientUpgrade(ctx context.Context, target string) (*clientUpgrade, error) {
	if !version.Newer(target, version.Version) {
		return nil, errors.New("目标版本必须高于当前 Client 版本")
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	current, err := os.Stat(executable)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(filepath.Dir(executable), ".agentuse-upgrade-")
	if err != nil {
		return nil, fmt.Errorf("无法写入 Client 安装目录: %w", err)
	}
	plan := &clientUpgrade{executable: executable, directory: dir}
	keep := false
	defer func() {
		if !keep {
			plan.cleanup()
		}
	}()
	asset := "agentuse-" + runtime.GOOS + "-" + runtime.GOARCH
	staged := filepath.Join(dir, "agentuse")
	sources := []string{clientReleaseURL, release.OSSDownloadURL, "https://agentuse.cn-beijing.taihangpfm.cn/releases"}
	var downloadErr error
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		downloadErr = downloadClientRelease(ctx, strings.TrimRight(source, "/")+"/"+target, asset, staged)
		if downloadErr == nil {
			break
		}
	}
	if downloadErr != nil {
		return nil, fmt.Errorf("无法下载并校验 Client %s: %w", target, downloadErr)
	}
	if err := os.Chmod(staged, current.Mode().Perm()); err != nil {
		return nil, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, staged, "version").Output()
	if err != nil || strings.TrimSpace(string(out)) != target {
		return nil, errors.New("发布包无法运行或版本号不匹配")
	}
	keep = true
	return plan, nil
}

func downloadClientRelease(ctx context.Context, base, asset, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	httpClient := &http.Client{CheckRedirect: func(req *http.Request, previous []*http.Request) error {
		if len(previous) >= 10 || (previous[0].URL.Scheme == "https" && req.URL.Scheme != "https") {
			return errors.New("invalid release redirect")
		}
		return nil
	}}
	fetch := func(name string) (io.ReadCloser, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+name, nil)
		if err != nil {
			return nil, err
		}
		response, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return nil, fmt.Errorf("%s: HTTP %d", name, response.StatusCode)
		}
		return response.Body, nil
	}
	manifest, err := fetch("SHA256SUMS")
	if err != nil {
		return err
	}
	sums, err := release.Checksums(manifest)
	manifest.Close()
	if err != nil {
		return err
	}
	if sums[asset] == "" {
		return fmt.Errorf("SHA256SUMS 缺少 %s", asset)
	}
	body, err := fetch(asset)
	if err != nil {
		return err
	}
	defer body.Close()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	const maxBinaryBytes = 1 << 30
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(body, maxBinaryBytes+1))
	if err == nil && (n == 0 || n > maxBinaryBytes || hex.EncodeToString(hash.Sum(nil)) != sums[asset]) {
		err = errors.New("Client 发布包 SHA256 校验失败")
	}
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

func (u *clientUpgrade) cleanup() { _ = os.RemoveAll(u.directory) }

func (u *clientUpgrade) installAndExec() error {
	defer u.cleanup()
	old := filepath.Join(u.directory, "previous")
	if err := os.Link(u.executable, old); err != nil {
		return err
	}
	backup := u.executable + ".previous"
	if err := os.Rename(old, backup); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(u.directory, "agentuse"), u.executable); err != nil {
		return err
	}
	u.cleanup()
	err := syscall.Exec(u.executable, os.Args, os.Environ())
	return errors.Join(err, os.Rename(backup, u.executable))
}
