// Package service assembles the five client services. Business logic lives in
// endpoint, setting, session, run and harness; this package owns startup wiring.
package service

import (
	"context"
	"fmt"
	"github.com/memory-co/agentuse/client/internal/service/endpoint"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/client/internal/service/run"
	"github.com/memory-co/agentuse/client/internal/service/session"
	"github.com/memory-co/agentuse/client/internal/service/setting"
	"github.com/memory-co/agentuse/client/internal/store"
	"os"
	"path/filepath"
)

func LoadConfigs(path string) ([]run.Config, error) {
	if _, e := os.Stat(path); e != nil {
		return nil, e
	}
	registered, e := (endpoint.Service{Path: path}).Registrations()
	if e != nil {
		return nil, e
	}
	settings, e := setting.New(filepath.Join(filepath.Dir(path), "setting.yaml"))
	if e != nil {
		return nil, e
	}
	out := []run.Config{}
	for _, ep := range registered {
		out = append(out, run.Config{ConfigPath: path, Server: ep.ServerURL, DaemonID: ep.DeviceID, Key: ep.DeviceKey, EndpointID: ep.ID(), Enabled: ep.Enabled, Settings: settings.Get()})
	}
	return out, nil
}
func LoadConfig(path string) (run.Config, error) {
	all, e := LoadConfigs(path)
	if e != nil {
		return run.Config{}, e
	}
	wanted := os.Getenv("AGENTUSE_SERVER")
	if wanted != "" {
		return LoadConfigForServer(path, wanted)
	}
	for _, c := range all {
		if c.Enabled {
			return c, nil
		}
	}
	if len(all) > 0 {
		return all[0], nil
	}
	return run.Config{}, fmt.Errorf("no registered endpoints in %s", path)
}
func LoadConfigForServer(path, server string) (run.Config, error) {
	all, e := LoadConfigs(path)
	if e != nil {
		return run.Config{}, e
	}
	normalized, _, e := store.NormalizeEndpoint(server)
	if e != nil {
		return run.Config{}, e
	}
	for _, c := range all {
		if c.Server == normalized {
			return c, nil
		}
	}
	return run.Config{}, fmt.Errorf("server is not registered: %s", server)
}
func Register(ctx context.Context, server, token, path string) (run.Config, error) {
	ep, e := (endpoint.Service{Path: path}).Register(ctx, server, token)
	if e != nil {
		return run.Config{}, e
	}
	return LoadConfigForServer(path, ep.ServerURL)
}

func New(path string, files *store.Ledger) (*run.Service, error) {
	settings, e := setting.New(filepath.Join(filepath.Dir(path), "setting.yaml"))
	if e != nil {
		return nil, e
	}
	endpoints, e := (endpoint.Service{Path: path}).Snapshot()
	if e != nil {
		return nil, e
	}
	sessions := &session.Service{Files: files, Settings: settings}
	harnesses := &harness.Service{Root: harness.HarnessRoot(harness.Config{ConfigPath: path})}
	if e = harnesses.Freeze(nil); e != nil {
		return nil, e
	}
	return run.New(run.Dependencies{Endpoints: endpoints, Settings: settings, Sessions: sessions, Harness: harnesses, Store: files}), nil
}
