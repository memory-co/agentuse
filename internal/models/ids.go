package models

import (
	"crypto/rand"
	"encoding/hex"
)

// NewID 生成带类型前缀的 id:s_ 会话,r_ 运行。
func NewID(prefix string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}
