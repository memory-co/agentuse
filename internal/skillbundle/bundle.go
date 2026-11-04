// Package skillbundle validates and extracts one portable skill ZIP without executing it.
package skillbundle

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const MaxArchive = 50 << 20
const MaxExpanded = 200 << 20
const MaxEntries = 4096

type Metadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func Inspect(data []byte) (Metadata, error)                     { return process(data, "") }
func Extract(data []byte, destination string) (Metadata, error) { return process(data, destination) }
func process(data []byte, destination string) (Metadata, error) {
	var meta Metadata
	if len(data) == 0 || len(data) > MaxArchive {
		return meta, fmt.Errorf("skill ZIP exceeds archive size limit")
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return meta, fmt.Errorf("invalid skill ZIP: %w", err)
	}
	if len(reader.File) > MaxEntries {
		return meta, fmt.Errorf("too many skill files")
	}
	seen := map[string]bool{}
	files, parents := map[string]bool{}, map[string]bool{}
	total := int64(0)
	var manifest []byte
	for _, entry := range reader.File {
		name := strings.TrimSuffix(entry.Name, "/")
		if !utf8.ValidString(name) || name == "" || name == "." || strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, ":") {
			return meta, fmt.Errorf("invalid ZIP path %q", entry.Name)
		}
		key := strings.ToLower(name)
		if seen[key] {
			return meta, fmt.Errorf("duplicate ZIP path %q", name)
		}
		seen[key] = true
		for parent := path.Dir(key); parent != "."; parent = path.Dir(parent) {
			if files[parent] {
				return meta, fmt.Errorf("ZIP file used as directory: %q", name)
			}
			parents[parent] = true
		}
		if !entry.FileInfo().IsDir() {
			if parents[key] {
				return meta, fmt.Errorf("ZIP directory used as file: %q", name)
			}
			files[key] = true
		}
		if entry.Mode()&os.ModeType != 0 && !entry.FileInfo().IsDir() {
			return meta, fmt.Errorf("links and special files are forbidden in skill ZIP")
		}
		if entry.FileInfo().IsDir() {
			if destination != "" {
				if err = os.MkdirAll(filepath.Join(destination, filepath.FromSlash(name)), 0700); err != nil {
					return meta, err
				}
			}
			continue
		}
		if strings.HasPrefix(name, ".claude-plugin/") || strings.HasPrefix(name, ".codex-plugin/") {
			return meta, fmt.Errorf("skill ZIP must not contain plugin manifests")
		}
		if entry.UncompressedSize64 > MaxExpanded {
			return meta, fmt.Errorf("expanded skill exceeds size limit")
		}
		src, e := entry.Open()
		if e != nil {
			return meta, e
		}
		content, e := io.ReadAll(io.LimitReader(src, MaxExpanded-total+1))
		src.Close()
		if e != nil {
			return meta, e
		}
		total += int64(len(content))
		if total > MaxExpanded {
			return meta, fmt.Errorf("expanded skill exceeds size limit")
		}
		if name == "SKILL.md" {
			manifest = content
		}
		if destination != "" {
			file := filepath.Join(destination, filepath.FromSlash(name))
			if e = os.MkdirAll(filepath.Dir(file), 0700); e != nil {
				return meta, e
			}
			mode := os.FileMode(0600)
			if entry.Mode()&0100 != 0 {
				mode = 0700
			}
			if e = os.WriteFile(file, content, mode); e != nil {
				return meta, e
			}
		}
	}
	if !utf8.Valid(manifest) {
		return meta, fmt.Errorf("SKILL.md must be UTF-8")
	}
	text := strings.ReplaceAll(string(manifest), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return meta, fmt.Errorf("skill ZIP requires root SKILL.md with frontmatter")
	}
	lines := strings.Split(text[4:], "\n")
	end := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return meta, fmt.Errorf("unterminated skill frontmatter")
	}
	frontmatter := []byte(strings.Join(lines[:end], "\n"))
	var node yaml.Node
	if err = yaml.Unmarshal(frontmatter, &node); err != nil {
		return meta, err
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return meta, fmt.Errorf("skill frontmatter must be a mapping")
	}
	fields := node.Content[0].Content
	for i := 0; i < len(fields); i += 2 {
		if (fields[i].Value == "name" || fields[i].Value == "description") && (fields[i+1].Tag != "!!str" || fields[i+1].Kind != yaml.ScalarNode) {
			return meta, fmt.Errorf("skill name and description must be strings")
		}
	}
	if err = yaml.Unmarshal(frontmatter, &meta); err != nil {
		return meta, err
	}
	if len(meta.Name) == 0 || len(meta.Name) > 64 || !((meta.Name[0] >= 'a' && meta.Name[0] <= 'z') || (meta.Name[0] >= 'A' && meta.Name[0] <= 'Z') || (meta.Name[0] >= '0' && meta.Name[0] <= '9')) || strings.TrimSpace(meta.Description) == "" {
		return meta, fmt.Errorf("skill name and description are required")
	}
	for _, r := range meta.Name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return meta, fmt.Errorf("invalid skill name")
		}
	}
	return meta, nil
}
