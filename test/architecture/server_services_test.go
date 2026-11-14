package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Sharing a SQL transaction does not transfer ownership of the affected tables.
// Check inline SQL and store constants so an orchestrator cannot quietly acquire
// another domain's writes when new cleanup/publication paths are added.
func TestServerServicesWriteTheirOwnTables(t *testing.T) {
	root := filepath.Join("..", "..", "server", "internal")
	constants := map[string]string{}
	storeFiles, err := filepath.Glob(filepath.Join(root, "store", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range storeFiles {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value := spec.(*ast.ValueSpec)
				if len(value.Names) == 1 && len(value.Values) == 1 {
					if literal, ok := value.Values[0].(*ast.BasicLit); ok && literal.Kind == token.STRING {
						constants[value.Names[0].Name], _ = strconv.Unquote(literal.Value)
					}
				}
			}
		}
	}
	owners := map[string]string{
		"users":   "UserService",
		"devices": "DeviceService", "device_harnesses": "DeviceService", "device_capabilities": "DeviceService",
		"agents": "AgentService", "agent_versions": "AgentService", "agent_version_skills": "AgentService",
		"conversations": "ConversationService", "messages": "ConversationService", "attachments": "ConversationService",
		"message_agents": "RunService", "runs": "RunService", "run_events": "RunService", "run_requests": "RunService", "run_controls": "RunService",
		"works": "WorkService", "work_run_tokens": "WorkService", "run_notifications": "WorkService",
		"skills": "skill.Service", "skill_versions": "skill.Service",
	}
	writes := regexp.MustCompile(`(?i)\b(?:INSERT(?:\s+OR\s+\w+)?\s+INTO|UPDATE|DELETE\s+FROM)\s+([a-z_]+)`)
	paths, err := filepath.Glob(filepath.Join(root, "service", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, filepath.Join(root, "service", "skill", "service.go"))
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			receiver := ""
			if fn.Recv != nil {
				typ := fn.Recv.List[0].Type
				if pointer, ok := typ.(*ast.StarExpr); ok {
					typ = pointer.X
				}
				receiver = typ.(*ast.Ident).Name
			}
			if receiver == "conversationEvents" {
				receiver = "ConversationService"
			}
			if f.Name.Name == "skill" {
				receiver = "skill." + receiver
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				var sql string
				switch n := node.(type) {
				case *ast.BasicLit:
					if n.Kind == token.STRING {
						sql, _ = strconv.Unquote(n.Value)
					}
				case *ast.SelectorExpr:
					if id, ok := n.X.(*ast.Ident); ok && id.Name == "store" {
						sql = constants[n.Sel.Name]
					}
				}
				for _, match := range writes.FindAllStringSubmatch(sql, -1) {
					table := strings.ToLower(match[1])
					if owner, ok := owners[table]; ok && owner != receiver {
						t.Errorf("%s: %s.%s writes %s; delegate to %s with the same transaction", path, receiver, fn.Name, table, owner)
					}
				}
				return true
			})
		}
	}
}

// Domain services must declare their peers. A shared runtime or back-reference
// to the root would restore access to every business operation through embedding.
func TestServerServiceRuntimeHasNoBusinessBackReferences(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "server", "internal", "service", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(node ast.Node) bool {
			ts, ok := node.(*ast.TypeSpec)
			if !ok || ts.Name.Name == "Service" {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			ast.Inspect(st.Fields, func(field ast.Node) bool {
				if _, qualified := field.(*ast.SelectorExpr); qualified {
					return false // file.Service and skill.Service are leaf peers.
				}
				if id, ok := field.(*ast.Ident); ok {
					if id.Name == "Service" || (ts.Name.Name == "serviceRuntime" && strings.HasSuffix(id.Name, "Service")) {
						t.Errorf("%s: %s carries business back-reference %s", path, ts.Name, id.Name)
					}
				}
				return true
			})
			return false
		})
	}
}
