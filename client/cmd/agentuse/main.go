// Command agentuse 跑在用户电脑上：注册本机、运行 daemon、查看本机状态。
package main

import (
	"os"
	"path/filepath"

	"github.com/memory-co/agentuse/client/internal/cli"
)

func main() {
	if filepath.Base(os.Args[0]) == "context" {
		os.Exit(cli.ContextMain(os.Args[1:]))
	}
	os.Exit(cli.Main(os.Args[1:]))
}
