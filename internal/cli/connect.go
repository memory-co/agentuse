package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/memory-co/agentuse/internal/config"
	"github.com/memory-co/agentuse/internal/daemon"
	"github.com/memory-co/agentuse/internal/hub"
)

// connect 是用户在自己电脑上跑的那条命令:检查前提 → 兑换接入码 → 写 daemon.json。
func connect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	dir := fs.String("dir", ".", "agent 干活的仓库目录")
	baseRef := fs.String("base-ref", "", "worktree 从哪个分支切(默认:仓库当前分支)")
	name := fs.String("name", "", "这台电脑在平台上显示的名字(默认:主机名)")
	path := fs.String("config", daemonConfigPath(), "daemon 配置文件")
	noCheck := fs.Bool("no-check", false, "跳过 claude 命令检查")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return errors.New("usage: agentuse connect <hub 地址> <接入码> [-dir .]")
	}
	hubURL, code := strings.TrimRight(pos[0], "/"), pos[1]

	// 1. 前提。不满足就停,接入码不消耗。
	if !*noCheck {
		if _, err := exec.LookPath("claude"); err != nil {
			return errors.New("没找到 claude 命令:先装好 Claude Code 并登录(https://claude.com/claude-code),再跑一次这条命令")
		}
		ok("找到 claude")
	}
	repo, ref, err := checkRepo(*dir, *baseRef)
	if err != nil {
		return err
	}
	ok(fmt.Sprintf("仓库 %s(基准分支 %s)", repo, ref))

	// 2. 兑换。这台电脑在这个 hub 上已有的设备身份一并带上,同一个主人就复用。
	cfg, err := loadOrEmpty(*path)
	if err != nil {
		return err
	}
	req := hub.ConnectRequest{Code: code, DeviceName: deviceName(*name), OS: runtime.GOOS + "/" + runtime.GOARCH, Dir: repo, BaseRef: ref}
	for _, c := range cfg.On(hubURL) {
		req.Devices = append(req.Devices, hub.DeviceCred{ID: c.DeviceID, Key: c.DeviceKey})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := daemon.Connect(ctx, hubURL, req)
	if errors.Is(err, daemon.ErrBadCode) {
		return errors.New("接入码不对、已经用过或已过期(15 分钟有效):回平台的设置页重新生成一条")
	}
	if err != nil {
		return err
	}

	// 3. 写配置(0600)。复用设备时密钥不变,沿用已有的那条。
	if res.DeviceKey != "" {
		cfg.Upsert(config.HubConn{URL: hubURL, Platform: res.Platform, DeviceID: res.DeviceID, DeviceKey: res.DeviceKey})
		if err := config.SaveDaemon(*path, cfg); err != nil {
			return fmt.Errorf("已接入,但写 %s 失败:%w", *path, err)
		}
	}
	ok(fmt.Sprintf("已接入 %s:agent @%s → %s:%s", res.Platform, res.Agent.Handle, req.DeviceName, repo))
	fmt.Println("下一步:运行 `agentuse daemon start`(已经在跑的话重启它),然后在评论里 @" + res.Agent.Handle + " 试试。")
	return nil
}

func ok(msg string) { fmt.Println("✓ " + msg) }

// checkRepo 确认 dir 在一个 git 仓库里,交回仓库根目录和基准分支。
func checkRepo(dir, baseRef string) (string, string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	top, err := git(abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", fmt.Errorf("%s 不是 git 仓库:到你想让 agent 干活的仓库目录里再跑,或者用 -dir 指定", abs)
	}
	if baseRef == "" {
		baseRef, err = git(top, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil || baseRef == "HEAD" {
			baseRef = "main"
		}
	}
	if _, err := git(top, "rev-parse", "--verify", "--quiet", baseRef+"^{commit}"); err != nil {
		return "", "", fmt.Errorf("仓库里没有基准分支 %s:用 -base-ref 指定一个存在的分支", baseRef)
	}
	return top, baseRef, nil
}

func git(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

func deviceName(name string) string {
	if name != "" {
		return name
	}
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSuffix(h, ".local")
}

// loadOrEmpty 读已有的 daemon.json;没有就从空的开始。文件坏了报错,不覆盖。
func loadOrEmpty(path string) (config.Daemon, error) {
	var cfg config.Daemon
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("%s 不是合法的 JSON(%v):修好或删掉它再接入", path, err)
	}
	return cfg, nil
}

// agents 列出这台电脑在每个 hub 上绑着的 agent。
func agents(args []string) error {
	fs := flag.NewFlagSet("agents", flag.ContinueOnError)
	path := fs.String("config", daemonConfigPath(), "daemon 配置文件")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.LoadDaemon(*path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, c := range cfg.Hubs {
		info, err := daemon.NewClient(c.URL, c.DeviceID, c.DeviceKey).Info(ctx)
		if err != nil {
			fmt.Printf("%s [%s] 设备 %s:%v\n", c.URL, c.Platform, c.DeviceID, err)
			continue
		}
		state := "离线"
		if info.Device.Online {
			state = "在线"
		}
		fmt.Printf("%s [%s] 设备 %s(%s,%s)\n", c.URL, info.Platform, info.Device.Name, c.DeviceID, state)
		if len(info.Agents) == 0 {
			fmt.Println("  (没有绑定的 agent)")
		}
		for _, a := range info.Agents {
			fmt.Printf("  @%s  %s  wake=%s\n", a.Handle, a.Workdir, a.Wake)
		}
	}
	return nil
}

// disconnect 把一个 agent 从这台电脑上解绑。
func disconnect(args []string) error {
	fs := flag.NewFlagSet("disconnect", flag.ContinueOnError)
	path := fs.String("config", daemonConfigPath(), "daemon 配置文件")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: agentuse disconnect <agent>")
	}
	handle := strings.TrimPrefix(pos[0], "@")
	cfg, err := config.LoadDaemon(*path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, c := range cfg.Hubs {
		err := daemon.NewClient(c.URL, c.DeviceID, c.DeviceKey).Disconnect(ctx, handle)
		if err == nil {
			ok(fmt.Sprintf("@%s 已从这台电脑解绑(%s [%s])", handle, c.URL, c.Platform))
			return nil
		}
	}
	return fmt.Errorf("这台电脑上没有绑定 @%s", handle)
}
