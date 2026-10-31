// Command agentuse-server 是服务端：start 起服务，其余是调 HTTP API 的管理命令。
package main

import (
	"os"

	"github.com/memory-co/agentuse/server/internal/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
