package endpoint

import (
	"fmt"
	"net/url"
	"strings"
)

func NormalizeEndpoint(raw string) (string, string, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", fmt.Errorf("invalid server URL")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "http" && u.Port() == "80") || (u.Scheme == "https" && u.Port() == "443") {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	normalized := u.String()
	suffix := strings.TrimPrefix(normalized, u.Scheme+"://")
	var id strings.Builder
	id.WriteString(u.Scheme + "---")
	for _, b := range []byte(suffix) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '.' || b == '-' || b == '_' {
			id.WriteByte(b)
		} else {
			fmt.Fprintf(&id, "%%%02X", b)
		}
	}
	return normalized, id.String(), nil
}
