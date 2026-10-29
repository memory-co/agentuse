// Package attachment contains the shared attachment metadata and streaming client.
package attachment

import (
	"errors"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

const Capability = "attachments-v1"

type Attachment struct {
	ID           string `json:"id"`
	SessionID    string `json:"session_id"`
	Filename     string `json:"filename"`
	ContentType  string `json:"content_type"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
	CreatedBy    string `json:"created_by"`
	Source       string `json:"source"`
	DaemonID     string `json:"daemon_id,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	DownloadPath string `json:"download_path"`
}
type Page struct {
	Attachments []Attachment `json:"attachments"`
	NextCursor  *string      `json:"next_cursor"`
}

func Filename(s string) (string, error) {
	s = path.Base(strings.ReplaceAll(s, `\`, "/"))
	if s == "" || s == "." || s == ".." || len(s) > 255 || !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return "", errors.New("文件名不合法")
	}
	return s, nil
}
func ValidID(s, prefix string) bool {
	if !strings.HasPrefix(s, prefix) || len(s) <= len(prefix) {
		return false
	}
	for _, c := range s[len(prefix):] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
