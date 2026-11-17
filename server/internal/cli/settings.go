package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

type startSettings struct {
	PublicURL string `yaml:"public_url"`
	Security  struct {
		WSEncryption   *bool  `yaml:"ws_encryption"`
		PrepareTimeout string `yaml:"prepare_timeout"`
		RunTimeout     string `yaml:"run_timeout"`
	} `yaml:"security"`
}

func loadStartSettings(path string) (startSettings, error) {
	var settings startSettings
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, fmt.Errorf("%s: %w", path, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)
	if err = decoder.Decode(&settings); err != nil && !errors.Is(err, io.EOF) {
		return settings, fmt.Errorf("%s: %w", path, err)
	}
	return settings, nil
}
