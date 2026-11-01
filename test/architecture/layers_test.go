package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Endpoint discovery, task orchestration and native execution must have separate
// package boundaries. A new endpoint mode cannot bypass these owners in the CLI.
func TestClientServiceDependencies(t *testing.T) {
	root := filepath.Join("..", "..", "client", "internal", "service")
	const prefix = "github.com/memory-co/agentuse/client/internal/service"
	allowed := map[string]map[string]bool{
		"endpoint": {}, "setting": {}, "session": {"endpoint": true, "setting": true}, "harness": {}, "run": {"endpoint": true, "setting": true, "session": true, "harness": true},
	}
	e := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) == 1 {
			if d.Name() != "services.go" && !strings.HasSuffix(d.Name(), "_test.go") {
				t.Errorf("top-level service file %s must belong to one of the five service packages", rel)
			}
			return nil
		}
		owner := parts[0]
		deps, ok := allowed[owner]
		if !ok {
			t.Errorf("unexpected service package %s", owner)
			return nil
		}
		file, e := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if e != nil {
			return e
		}
		for _, im := range file.Imports {
			value, _ := strconv.Unquote(im.Path.Value)
			if value == prefix {
				t.Errorf("%s imports the composition layer", rel)
			}
			if strings.HasPrefix(value, prefix+"/") {
				peer := strings.Split(strings.TrimPrefix(value, prefix+"/"), "/")[0]
				if peer != owner && !deps[peer] {
					t.Errorf("%s imports service %s outside its responsibility", rel, peer)
				}
			}
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.Name == "LocalService" {
					t.Errorf("%s reintroduces a standalone service for an endpoint mode", rel)
				}
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
