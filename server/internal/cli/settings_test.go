package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartSettingsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	if settings, err := loadStartSettings(path); err != nil || settings.PublicURL != "" {
		t.Fatal("missing settings should use defaults", settings, err)
	}
	for _, test := range []struct {
		name, yaml, publicURL string
		invalid               bool
	}{
		{name: "empty"},
		{name: "security defaults", yaml: "security:\n  ws_encryption: true\n  prepare_timeout: 30m\n  run_timeout: 24h\n"},
		{name: "unknown security field", yaml: "security:\n  encrypted: true\n", invalid: true},
		{name: "proxy address", yaml: "public_url: https://agentuse.example.com\n", publicURL: "https://agentuse.example.com"},
		{name: "port and path", yaml: "public_url: https://agentuse.example.com:8443/agentuse/\n", publicURL: "https://agentuse.example.com:8443/agentuse/"},
		{name: "unknown field", yaml: "public_uri: https://agentuse.example.com\n", invalid: true},
		{name: "invalid yaml", yaml: "public_url: [\n", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(test.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			settings, err := loadStartSettings(path)
			if test.invalid {
				if err == nil || !strings.Contains(err.Error(), path) {
					t.Fatal("invalid settings must report their file path", err)
				}
			} else if err != nil || settings.PublicURL != test.publicURL {
				t.Fatal(settings, err)
			}
		})
	}
}
