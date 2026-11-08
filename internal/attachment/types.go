// Package attachment validates attachment filenames and IDs.
package attachment

import (
	"errors"
	"net/url"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

const Capability = "attachments-v1"

type Attachment = agentuse.Attachment
type Page = agentuse.AttachmentPage

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

func Path(conversation string) string {
	return "/api/conversations/" + url.PathEscape(conversation) + "/attachments"
}
