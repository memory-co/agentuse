package harness

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
)

type HarnessInfo struct {
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	Version  string `json:"version,omitempty"`
	Found    bool   `json:"found"`
	Disabled bool   `json:"disabled,omitempty"`
	Revision string `json:"revision,omitempty"`
}

func available(a Adapter) error {
	if a.Disabled {
		return fmt.Errorf("harness disabled")
	}
	info, e := os.Stat(a.Path)
	if e != nil {
		return e
	}
	if info.IsDir() || info.Mode()&0111 == 0 {
		return fmt.Errorf("harnessctl entry is not executable")
	}
	if len(a.Manifest.Platforms) > 0 {
		ok := false
		for _, platform := range a.Manifest.Platforms {
			ok = ok || platform == runtime.GOOS
		}
		if !ok {
			return fmt.Errorf("adapter does not support %s", runtime.GOOS)
		}
	}
	if a.Manifest.Requires.Python != "" {
		if _, e := exec.LookPath("python3"); e != nil {
			return e
		}
	}
	for _, bin := range a.Manifest.Requires.Bin {
		if _, e := exec.LookPath(bin); e != nil {
			return e
		}
	}
	return nil
}

// ListHarnesses is used by a new CLI process, loading its startup snapshot.
func ListHarnesses(cfg Config) []HarnessInfo {
	s := Service{Root: HarnessRoot(cfg)}
	if err := s.Freeze(cfg.Harnesses); err != nil {
		return []HarnessInfo{}
	}
	items, _ := s.Probe()
	return items
}
func (s Service) Probe() ([]HarnessInfo, error) {
	packages, e := s.Packages()
	if e != nil {
		return nil, e
	}
	out := []HarnessInfo{}
	found := s.Available()
	for name, a := range packages {
		v := HarnessInfo{Name: name, Version: a.Version, Revision: a.Revision, Disabled: a.Disabled}
		if _, ok := found[name]; ok {
			v.Path = a.Path
			v.Found = true
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (s Service) Resolve(name string) (Adapter, error) {
	if s.snapshot != nil {
		if a, ok := s.snapshot.available[name]; ok {
			return a, nil
		}
		return Adapter{}, fmt.Errorf("harness is unavailable in startup snapshot")
	}
	packages, e := s.Packages()
	if e != nil {
		return Adapter{}, e
	}
	a, ok := packages[name]
	if !ok {
		return a, fmt.Errorf("harness is not installed")
	}
	if e = available(a); e != nil {
		return a, e
	}
	return a, nil
}
