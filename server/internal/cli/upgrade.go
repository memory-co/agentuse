package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/memory-co/agentuse/internal/command"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func runUpgrade(args []string) error {
	action := "start"
	if len(args) > 0 && (args[0] == "check" || args[0] == "status" || args[0] == "history") {
		action = args[0]
		args = args[1:]
	}
	id := ""
	if action == "status" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id = args[0]
		args = args[1:]
	}
	cf, h := clientFlags("agentuse-server upgrade " + action)
	fs := cf.FlagSet
	jsonOut := fs.Bool("json", false, "输出 JSON")
	wait := fs.Bool("wait", false, "等待升级和重启完成")
	timeout := fs.Duration("timeout", 20*time.Minute, "等待升级结果的最长时间")
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 1 && action == "status" && id == "" {
		id = fs.Arg(0)
	} else if fs.NArg() > 0 {
		return command.UsageErr("upgrade status [升级ID]，其余操作无需位置参数")
	}
	client, err := h.client()
	if err != nil {
		return err
	}
	ctx, stop := command.SignalContext()
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	switch action {
	case "check":
		r, err := client.LatestServerRelease(ctx)
		if err != nil {
			return err
		}
		if *jsonOut {
			return command.PrintJSON(os.Stdout, r)
		}
		fmt.Printf("当前版本: %s\n最新版本: %s\n有更新: %t\n%s\n", r.CurrentVersion, r.Version, r.Available, r.URL)
		return nil
	case "status":
		if id != "" {
			r, err := client.GetServerUpgrade(ctx, id)
			if err != nil {
				return err
			}
			return printUpgrade(*r, *jsonOut)
		}
		fallthrough
	case "history":
		state, err := client.ServerUpgrades(ctx, "", 100)
		if err != nil {
			return err
		}
		if *jsonOut {
			return command.PrintJSON(os.Stdout, state)
		}
		fmt.Printf("Server %s (%s)\n", state.CurrentVersion, state.Platform)
		if !state.Supported {
			fmt.Println(state.Reason)
		}
		if action == "status" {
			if state.Active != nil {
				return printUpgrade(*state.Active, false)
			}
			fmt.Println("当前没有进行中的升级")
			return nil
		}
		rows := [][]string{{"ID", "原版本", "目标版本", "状态", "错误"}}
		for _, item := range state.Upgrades {
			rows = append(rows, []string{item.ID, item.FromVersion, item.TargetVersion, item.Status, item.Error})
		}
		command.PrintTable(os.Stdout, rows)
		return nil
	}
	item, err := client.StartServerUpgrade(ctx)
	if err != nil {
		return err
	}
	if !*wait {
		return printUpgrade(*item, *jsonOut)
	}
	last := ""
	for {
		if !*jsonOut && item.Status != last {
			fmt.Fprintf(os.Stderr, "升级 %s: %s\n", item.ID, item.Status)
			last = item.Status
		}
		if item.CompletedAt != nil {
			if err = printUpgrade(*item, *jsonOut); err != nil {
				return err
			}
			if item.Status == "failed" {
				return errors.New(item.Error)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("停止等待，服务端升级继续执行，使用 upgrade status %s 查看结果: %w", item.ID, ctx.Err())
		case <-time.After(time.Second):
		}
		next, e := client.GetServerUpgrade(ctx, item.ID)
		if e != nil {
			if agentuse.IsStatus(e, 401) || agentuse.IsStatus(e, 403) || agentuse.IsStatus(e, 404) {
				return e
			}
			continue // The listener briefly disappears during process replacement.
		}
		item = next
	}
}

func printUpgrade(item agentuse.ServerUpgrade, jsonOut bool) error {
	if jsonOut {
		return command.PrintJSON(os.Stdout, item)
	}
	fmt.Printf("升级 ID: %s\n版本: %s → %s\n状态: %s\n下载: %d / %d 字节\n", item.ID, item.FromVersion, item.TargetVersion, item.Status, item.DownloadedBytes, item.TotalBytes)
	if item.Error != "" {
		fmt.Println("错误:", item.Error)
	}
	return nil
}
