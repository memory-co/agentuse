// Package attachment validates attachment filenames and IDs.
package attachment

import (
	"errors"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"net/url"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
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

func Path(session string) string { return "/api/sessions/" + url.PathEscape(session) + "/attachments" }
