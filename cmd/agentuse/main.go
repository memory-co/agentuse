// Command agentuse 是 hub、daemon 和 agent 侧命令的单一二进制。
package main

import (
	"os"

	"github.com/memory-co/agentuse/internal/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
