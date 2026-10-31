// Package frontend 把网页的构建产物(dist/,`npm run build` 生成)嵌进 agentuse 客户端。
package frontend

import (
	"embed"
	"io/fs"
)

// dist 只提交了 .gitkeep;没构建前端时这里没有 index.html,服务照常跑 API。
//
//go:embed all:dist
var dist embed.FS

// FS 返回 dist/ 下的文件。
func FS() fs.FS {
	sub, _ := fs.Sub(dist, "dist")
	return sub
}

// Built 表示 dist/ 里有构建好的 index.html。
func Built() bool {
	_, err := fs.Stat(FS(), "index.html")
	return err == nil
}
