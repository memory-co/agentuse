// Command agentuse-server 是服务端：start 起服务，其余是调 HTTP API 的管理命令。
package main

import (
	"os"

	"github.com/memory-co/agentuse/internal/cli"
)

func main() { os.Exit(cli.ServerMain(os.Args[1:])) }
