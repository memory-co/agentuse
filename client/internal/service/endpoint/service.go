package endpoint

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/memory-co/agentuse/client/internal/store"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"sync"
)

const LocalID = "local"
const ModeLocal = "local"
const ModeServer = "server"

type Endpoint struct {
	Mode      string `yaml:"-" json:"mode"`
	Name      string `yaml:"name" json:"name"`
	ServerURL string `yaml:"server_url" json:"server_url"`
	Enabled   bool   `yaml:"enabled" json:"enabled"`
	DeviceID  string `yaml:"device_id" json:"device_id"`
	DeviceKey string `yaml:"device_key" json:"-"`
}

func (e Endpoint) ID() string {
	if e.IsLocal() {
		return LocalID
	}
	_, id, _ := store.NormalizeEndpoint(e.ServerURL)
	return id
}
func (e Endpoint) IsLocal() bool { return e.Mode == ModeLocal }
func Local() Endpoint            { return Endpoint{Name: "本地调试", Enabled: true, Mode: ModeLocal} }
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentuse", "endpoints.yaml")
}

type View struct {
	ID        string `json:"endpoint_id"`
	Name      string `json:"name"`
	ServerURL string `json:"server_url"`
	Enabled   bool   `json:"enabled"`
	Mode      string `json:"mode"`
}

func (s Service) Views() ([]View, error) {
	items, e := s.List()
	if e != nil {
		return nil, e
	}
	out := make([]View, 0, len(items))
	for _, ep := range items {
		out = append(out, View{ep.ID(), ep.Name, ep.ServerURL, ep.Enabled, ep.Mode})
	}
	return out, nil
}
func (s Service) List() ([]Endpoint, error) {
	items, e := s.Registrations()
	if e != nil {
		return nil, e
	}
	return append([]Endpoint{Local()}, items...), nil
}
func (s Service) GetByID(id string) (Endpoint, error) {
	items, e := s.List()
	if e != nil {
		return Endpoint{}, e
	}
	for _, ep := range items {
		if ep.ID() == id {
			return ep, nil
		}
	}
	return Endpoint{}, os.ErrNotExist
}

type Service struct {
	Path     string
	snapshot *[]Endpoint
}

// Snapshot loads registrations once for a running client. File writes apply next startup.
func (s Service) Snapshot() (*Service, error) {
	items, err := s.Registrations()
	if err != nil {
		return nil, err
	}
	s.snapshot = &items
	return &s, nil
}

var endpointMu sync.Mutex

func (s Service) Registrations() ([]Endpoint, error) {
	if s.snapshot != nil {
		return append([]Endpoint{}, (*s.snapshot)...), nil
	}
	b, e := os.ReadFile(s.Path)
	if errors.Is(e, os.ErrNotExist) {
		return []Endpoint{}, nil
	}
	if e != nil {
		return nil, e
	}
	var file struct {
		Endpoints []Endpoint `yaml:"endpoints"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if e = dec.Decode(&file); e != nil {
		return nil, fmt.Errorf("%s: %w", s.Path, e)
	}
	seen := map[string]bool{}
	for i, v := range file.Endpoints {
		url, id, e := store.NormalizeEndpoint(v.ServerURL)
		if e != nil {
			return nil, e
		}
		if seen[id] || v.DeviceID == "" || v.DeviceKey == "" {
			return nil, fmt.Errorf("duplicate or incomplete endpoint")
		}
		seen[id] = true
		file.Endpoints[i].ServerURL = url
		file.Endpoints[i].Mode = ModeServer
	}
	return file.Endpoints, nil
}
func (s Service) Save(v Endpoint) error {
	if v.IsLocal() || v.ServerURL == LocalID {
		return fmt.Errorf("local is built in and cannot be saved as a registration")
	}
	endpointMu.Lock()
	defer endpointMu.Unlock()
	unlock, err := store.Lock(s.Path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	u, id, e := store.NormalizeEndpoint(v.ServerURL)
	if e != nil {
		return e
	}
	v.ServerURL = u
	if v.DeviceID == "" || v.DeviceKey == "" {
		return fmt.Errorf("device identity required")
	}
	items, e := s.Registrations()
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	found := false
	for i, x := range items {
		if x.ID() == id {
			items[i] = v
			found = true
		}
	}
	if !found {
		items = append(items, v)
	}
	return store.WriteYAML(s.Path, struct {
		Endpoints []Endpoint `yaml:"endpoints"`
	}{items})
}

func (s Service) Get(server string) (Endpoint, error) {
	if server == LocalID {
		return Local(), nil
	}
	_, id, e := store.NormalizeEndpoint(server)
	if e != nil {
		return Endpoint{}, e
	}
	items, e := s.Registrations()
	if e != nil {
		return Endpoint{}, e
	}
	for _, v := range items {
		if v.ID() == id {
			return v, nil
		}
	}
	return Endpoint{}, os.ErrNotExist
}
func (s Service) Remove(server string) error {
	if server == LocalID {
		return fmt.Errorf("local is built in and cannot be removed")
	}
	endpointMu.Lock()
	defer endpointMu.Unlock()
	unlock, e := store.Lock(s.Path + ".lock")
	if e != nil {
		return e
	}
	defer unlock()
	_, id, e := store.NormalizeEndpoint(server)
	if e != nil {
		return e
	}
	items, e := s.Registrations()
	if e != nil {
		return e
	}
	out := []Endpoint{}
	for _, v := range items {
		if v.ID() != id {
			out = append(out, v)
		}
	}
	return store.WriteYAML(s.Path, struct {
		Endpoints []Endpoint `yaml:"endpoints"`
	}{out})
}
