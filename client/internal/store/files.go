package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

var ErrInvalid = errors.New("invalid local reference")

func Segment(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\\x00")
}

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

// SafePath rejects symbolic links in every existing component. Callers only use
// private configuration roots and validated IDs, never a user supplied subpath.
func SafePath(root string, parts ...string) (string, error) {
	p, e := filepath.Abs(root)
	if e != nil {
		return "", e
	}
	for _, v := range parts {
		if !Segment(v) {
			return "", ErrInvalid
		}
		p = filepath.Join(p, v)
	}
	for cur := p; ; cur = filepath.Dir(cur) {
		st, err := os.Lstat(cur)
		if err == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symbolic link is not allowed: %s", cur)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if filepath.Dir(cur) == cur {
			break
		}
	}
	return p, nil
}
func Atomic(path string, data []byte) error {
	if _, err := SafePath(filepath.Dir(path), filepath.Base(path)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func WriteYAML(path string, v any) error {
	b, e := yaml.Marshal(v)
	if e != nil {
		return e
	}
	return Atomic(path, b)
}
func ReadYAML(path string, v any) error {
	if _, e := SafePath(filepath.Dir(path), filepath.Base(path)); e != nil {
		return e
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	return yaml.Unmarshal(b, v)
}
func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return Atomic(path, append(b, '\n'))
}
