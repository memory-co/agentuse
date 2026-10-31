package service

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/memory-co/agentuse/client/internal/store"
	"gopkg.in/yaml.v3"
)

type Endpoint struct {
	Name      string `yaml:"name" json:"name"`
	ServerURL string `yaml:"server_url" json:"server_url"`
	Enabled   bool   `yaml:"enabled" json:"enabled"`
	DeviceID  string `yaml:"device_id" json:"device_id"`
	DeviceKey string `yaml:"device_key" json:"-"`
}

func (e Endpoint) ID() string { _, id, _ := store.NormalizeEndpoint(e.ServerURL); return id }

type EndpointService struct{ Path string }

var endpointMu sync.Mutex

func (s EndpointService) List() ([]Endpoint, error) {
	b, e := os.ReadFile(s.Path)
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
	}
	return file.Endpoints, nil
}
func (s EndpointService) Save(v Endpoint) error {
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
	items, e := s.List()
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

func (s EndpointService) Get(server string) (Endpoint, error) {
	_, id, e := store.NormalizeEndpoint(server)
	if e != nil {
		return Endpoint{}, e
	}
	items, e := s.List()
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
func (s EndpointService) Remove(server string) error {
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
	items, e := s.List()
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
