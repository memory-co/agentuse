package attachment

import (
	"fmt"
	"io/fs"
	"strings"
	"unicode"
	"unicode/utf8"
)

// RelativePath names a file inside one conversation's file tree.
func RelativePath(value string) (string, error) {
	if len(value) > 4096 || !utf8.ValidString(value) || !fs.ValidPath(value) || value == "." || strings.Contains(value, `\`) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("文件路径必须是会话内的相对路径")
	}
	for _, part := range strings.Split(value, "/") {
		if len(part) > 255 {
			return "", fmt.Errorf("文件名过长")
		}
	}
	return value, nil
}
