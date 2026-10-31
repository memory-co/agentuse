// Package cli 是两个命令行的入口:agentuse(用户电脑)和 agentuse-server(服务端)。
// 设计见 docs/v2/cli/。
package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/memory-co/agentuse/internal/version"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// Version 由构建时 -ldflags 注入。

// ErrUsage 表示参数不对，退出码 2。
var ErrUsage = errors.New("usage")

type Command struct {
	Name string
	Run  func(args []string) error
}

// Dispatch 按第一个参数找子命令，统一处理 help / version 和退出码。
func Dispatch(prog, usage string, args []string, cmds []Command) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	case "version", "-v", "--version":
		fmt.Println(version.Version)
		return 0
	}
	for _, c := range cmds {
		if c.Name == args[0] {
			return ExitCode(c.Run(args[1:]))
		}
	}
	fmt.Fprintf(os.Stderr, "错误: 未知命令 %s %s\n\n%s", prog, args[0], usage)
	return 2
}

func ExitCode(err error) int {
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, ErrUsage):
		return 2
	}
	fmt.Fprintln(os.Stderr, "错误:", err)
	return 1
}

func NewFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

// Parse 解析参数;参数错误时 flag 包已经打印了用法，这里返回 ErrUsage。
func Parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return ErrUsage
	}
	return nil
}

func UsageErr(format string, a ...any) error {
	fmt.Fprintf(os.Stderr, "错误: "+format+"\n", a...)
	return ErrUsage
}

func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func EnvOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func PrintJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// PrintTable 按显示宽度对齐列(中文占两格,tabwriter 按字节 / 字符算会错位)。
func PrintTable(w io.Writer, rows [][]string) {
	widths := map[int]int{}
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], displayWidth(c))
		}
	}
	for _, r := range rows {
		line := ""
		for i, c := range r {
			if i == len(r)-1 {
				line += c
				break
			}
			line += c + strings.Repeat(" ", widths[i]-displayWidth(c)+2)
		}
		fmt.Fprintln(w, line)
	}
}

func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x1100 && (r <= 0x115f || (r >= 0x2e80 && r <= 0xa4cf) || (r >= 0xac00 && r <= 0xd7a3) || (r >= 0xf900 && r <= 0xfaff) || (r >= 0xfe30 && r <= 0xfe4f) || (r >= 0xff00 && r <= 0xff60) || (r >= 0xffe0 && r <= 0xffe6)) {
			n += 2
		} else {
			n++
		}
	}
	return n
}
