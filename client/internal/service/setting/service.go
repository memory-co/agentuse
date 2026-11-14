package setting

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/memory-co/agentuse/client/internal/fileutil"
	"gopkg.in/yaml.v3"
)

type Settings struct {
	LocalHTTP struct {
		Enabled bool   `yaml:"enabled" json:"enabled"`
		Host    string `yaml:"host" json:"host"`
		Port    int    `yaml:"port" json:"port"`
	} `yaml:"local_http" json:"local_http"`
	Session struct {
		WorkRoot string `yaml:"work_root" json:"work_root"`
		Quota    *Quota `yaml:"quota,omitempty" json:"quota,omitempty"`
	} `yaml:"session" json:"session"`
}
type Quota struct {
	MaxBytes int64 `yaml:"max_bytes" json:"max_bytes"`
}
type Service struct {
	Path  string
	value Settings
}

func Defaults() Settings {
	var s Settings
	s.LocalHTTP.Enabled = true
	s.LocalHTTP.Host = "127.0.0.1"
	s.LocalHTTP.Port = 7801
	home, _ := os.UserHomeDir()
	s.Session.WorkRoot = filepath.Join(home, ".agentuse", "workspace")
	return s
}
func (s Settings) Validate() error {
	if s.LocalHTTP.Host != "localhost" && net.ParseIP(s.LocalHTTP.Host) == nil {
		return fmt.Errorf("local_http.host must be an IP address or localhost")
	}
	if s.LocalHTTP.Port < 1 || s.LocalHTTP.Port > 65535 {
		return fmt.Errorf("invalid local_http.port")
	}
	if s.Session.Quota != nil && s.Session.Quota.MaxBytes <= 0 {
		return fmt.Errorf("quota.max_bytes must be positive")
	}
	p := expandHome(s.Session.WorkRoot)
	if !filepath.IsAbs(p) {
		return fmt.Errorf("session.work_root must be absolute")
	}
	p = filepath.Clean(p)
	home, _ := os.UserHomeDir()
	for _, unsafe := range []string{"/", home, filepath.Join(home, ".agentuse"), filepath.Join(home, ".codex"), filepath.Join(home, ".claude")} {
		if p == filepath.Clean(unsafe) {
			return fmt.Errorf("unsafe session.work_root")
		}
	}
	for _, protected := range []string{filepath.Join(home, ".agentuse", "harness"), filepath.Join(home, ".codex"), filepath.Join(home, ".claude")} {
		if p == protected || strings.HasPrefix(p, protected+string(filepath.Separator)) || strings.HasPrefix(protected, p+string(filepath.Separator)) {
			return fmt.Errorf("work_root overlaps protected directory")
		}
	}
	_, e := fileutil.SafePath(p)
	return e
}
func New(path string) (*Service, error) {
	s := &Service{Path: path}
	if _, e := os.Stat(path); os.IsNotExist(e) {
		v := Defaults()
		if e = fileutil.WriteYAML(path, v); e != nil {
			return nil, e
		}
	}
	if e := s.load(); e != nil {
		return nil, e
	}
	return s, nil
}
func (s *Service) load() error {
	b, e := os.ReadFile(s.Path)
	if e != nil {
		return e
	}
	v := Defaults()
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if e = dec.Decode(&v); e != nil {
		return fmt.Errorf("%s: %w", s.Path, e)
	}
	if e = v.Validate(); e != nil {
		return e
	}
	v.Session.WorkRoot = filepath.Clean(expandHome(v.Session.WorkRoot))
	s.value = v
	return nil
}
func (s *Service) Get() Settings {
	v := s.value
	if v.Session.Quota != nil {
		q := *v.Session.Quota
		v.Session.Quota = &q
	}
	return v
}
func (s *Service) Save(v Settings) error {
	if e := v.Validate(); e != nil {
		return e
	}
	unlock, err := fileutil.Lock(s.Path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	if e := fileutil.WriteYAML(s.Path, v); e != nil {
		return e
	}
	return nil
}
func (s Settings) UIAddress() string {
	if !s.LocalHTTP.Enabled {
		return ""
	}
	return net.JoinHostPort(s.LocalHTTP.Host, strconv.Itoa(s.LocalHTTP.Port))
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
