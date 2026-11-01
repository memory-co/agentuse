package harness

import (
	"fmt"
	"github.com/memory-co/agentuse/internal/harness"
	"os/exec"
	"sort"
)

// HarnessInfo 是 agentuse harnesses 列出的一行。
type HarnessInfo struct {
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	Version  string `json:"version,omitempty"`
	Found    bool   `json:"found"`
	Disabled bool   `json:"disabled,omitempty"`
	Revision string `json:"revision,omitempty"`
}

// ListHarnesses 按 daemon 的检测逻辑列出本机 harness,包括没装上的内置 harness 和配置里禁用的。
func ListHarnesses(cfg Config) []HarnessInfo {
	found := Detect(cfg)
	names := map[string]bool{}
	builtins, _ := harness.All()
	for _, sp := range builtins {
		names[sp.Name] = true
	}
	packages, _ := (Service{Root: HarnessRoot(cfg)}).Packages()
	for n := range packages {
		names[n] = true
	}
	for n := range cfg.Harnesses {
		names[n] = true
	}
	out := make([]HarnessInfo, 0, len(names))
	for n := range names {
		info := HarnessInfo{Name: n, Disabled: cfg.Harnesses[n].Disabled}
		if la, ok := found[n]; ok {
			info.Path, info.Version, info.Found = la.Path, la.Version, true
			info.Revision = la.Revision
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s Service) Probe() ([]HarnessInfo, error) {
	packages, e := s.Packages()
	if e != nil {
		return nil, e
	}
	out := []HarnessInfo{}
	for name, la := range packages {
		item := HarnessInfo{Name: name, Revision: la.Revision, Disabled: la.Disabled}
		if !la.Disabled {
			if path, e := exec.LookPath(expandHome(la.Path)); e == nil {
				item.Path = path
				item.Found = true
				if la.Spec != nil && len(la.Spec.Detect.Version) > 0 {
					item.Version = probeVersion(path, la.Spec.Detect.Version)
				}
			}
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s Service) Resolve(name string) (Adapter, error) {
	packages, e := s.Packages()
	if e != nil {
		return Adapter{}, e
	}
	la, ok := packages[name]
	if !ok || la.Disabled {
		return Adapter{}, fmt.Errorf("harness is not installed or enabled")
	}
	la.Path, e = exec.LookPath(expandHome(la.Path))
	if e != nil {
		return Adapter{}, fmt.Errorf("harness program unavailable: %w", e)
	}
	return la, nil
}
