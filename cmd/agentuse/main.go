// Command agentuse 是服务端(server)和 daemon 共用的单一二进制。
package main

import (
	"os"

	"github.com/memory-co/agentuse/internal/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
