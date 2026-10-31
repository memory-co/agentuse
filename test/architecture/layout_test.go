package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Check tests too: fixtures must not undo the compile-time separation.
func TestLayerDependencies(t *testing.T) {
	root := filepath.Join("..", "..")
	const module = "github.com/memory-co/agentuse/"
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imp, _ := strconv.Unquote(spec.Path.Value)
			imp = strings.TrimPrefix(imp, module)
			forbidden := strings.HasPrefix(rel, "client/") && strings.HasPrefix(imp, "server/") || strings.HasPrefix(rel, "server/") && strings.HasPrefix(imp, "client/")
			if strings.HasPrefix(rel, "internal/") {
				forbidden = forbidden || strings.HasPrefix(imp, "client/") || strings.HasPrefix(imp, "server/")
			}
			for _, pair := range [][2]string{{"client/internal/controller/", "client/internal/service"}, {"client/internal/service/", "client/internal/controller"}, {"server/internal/store/", "server/internal/service"}, {"server/internal/store/", "server/internal/controller"}, {"server/internal/store/", "server/internal/hub"}, {"server/internal/service/", "server/internal/hub"}, {"server/internal/service/", "server/internal/controller"}} {
				forbidden = forbidden || strings.HasPrefix(rel, pair[0]) && strings.HasPrefix(imp, pair[1])
			}
			if forbidden {
				t.Errorf("%s imports forbidden layer %s", rel, imp)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
