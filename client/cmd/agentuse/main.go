// Command agentuse 跑在用户电脑上：注册本机、运行 daemon、查看本机状态。
package main

import (
	"os"

	"github.com/memory-co/agentuse/client/internal/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
