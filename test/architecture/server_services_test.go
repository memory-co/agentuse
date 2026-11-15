package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
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
		"users":   "user",
		"devices": "device", "device_harnesses": "device", "device_capabilities": "device",
		"agents": "agent", "agent_versions": "agent", "agent_version_skills": "agent",
		"conversations": "conversation", "conversation_rounds": "conversation", "messages": "conversation", "attachments": "conversation",
		"run_inputs": "run", "runs": "run", "run_events": "run", "run_requests": "run", "run_controls": "run",
		"skills": "skill", "skill_versions": "skill",
	}
	writes := regexp.MustCompile(`(?i)\b(?:INSERT(?:\s+OR\s+\w+)?\s+INTO|UPDATE|DELETE\s+FROM)\s+([a-z_]+)`)
	models := map[string]string{
		"User": "users", "Device": "devices", "DeviceHarness": "device_harnesses", "DeviceCapability": "device_capabilities",
		"Agent": "agents", "AgentVersion": "agent_versions", "AgentVersionSkill": "agent_version_skills",
		"Conversation": "conversations", "ConversationRound": "conversation_rounds", "Message": "messages", "Attachment": "attachments",
		"Run": "runs", "RunInput": "run_inputs", "RunEvent": "run_events", "RunRequest": "run_requests", "RunControl": "run_controls",
		"Skill": "skills", "SkillVersion": "skill_versions",
	}
	serviceRoot := filepath.Join(root, "service")
	paths := []string{}
	err = filepath.WalkDir(serviceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") {
			paths = append(paths, path)
		}
		return nil
	})
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
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			rel, _ := filepath.Rel(serviceRoot, path)
			owner := strings.Split(filepath.ToSlash(rel), "/")[0]
			variables := map[string]string{}
			var modelTable func(ast.Expr) string
			modelTable = func(expr ast.Expr) string {
				switch value := expr.(type) {
				case *ast.UnaryExpr:
					return modelTable(value.X)
				case *ast.CompositeLit:
					return modelTable(value.Type)
				case *ast.Ident:
					return variables[value.Name]
				case *ast.SelectorExpr:
					if id, ok := value.X.(*ast.Ident); ok && id.Name == "store" {
						return models[value.Sel.Name]
					}
				}
				return ""
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				if assignment, ok := node.(*ast.AssignStmt); ok {
					for i, right := range assignment.Rhs {
						if i < len(assignment.Lhs) {
							if name, ok := assignment.Lhs[i].(*ast.Ident); ok {
								variables[name.Name] = modelTable(right)
							}
						}
					}
				}
				if call, ok := node.(*ast.CallExpr); ok {
					if method, ok := call.Fun.(*ast.SelectorExpr); ok {
						switch method.Sel.Name {
						case "Create", "Save", "Delete", "Update", "Updates", "UpdateColumn", "UpdateColumns":
							table := ""
							for _, arg := range call.Args {
								if candidate := modelTable(arg); candidate != "" {
									table = candidate
								}
							}
							for expr := method.X; expr != nil; {
								chain, ok := expr.(*ast.CallExpr)
								if !ok {
									break
								}
								selector, ok := chain.Fun.(*ast.SelectorExpr)
								if !ok {
									break
								}
								if selector.Sel.Name == "Model" && len(chain.Args) > 0 {
									table = modelTable(chain.Args[0])
								}
								if selector.Sel.Name == "Table" && len(chain.Args) > 0 {
									if literal, ok := chain.Args[0].(*ast.BasicLit); ok {
										table, _ = strconv.Unquote(literal.Value)
									}
								}
								expr = selector.X
							}
							if domain, ok := owners[table]; ok && owner != domain {
								t.Errorf("%s: %s.%s writes %s through GORM; delegate to %s", path, owner, fn.Name, table, domain)
							}
						}
					}
				}
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
					if domain, ok := owners[table]; ok && owner != domain {
						t.Errorf("%s: %s.%s writes %s; delegate to %s with the same transaction", path, owner, fn.Name, table, domain)
					}
				}
				return true
			})
		}
	}
}

// Domain packages depend on contracts and leaf services, never the root or an
// orchestration peer. Tests exercise the full composition from service/test/.
func TestServerServicePackageDependencies(t *testing.T) {
	root := filepath.Join("..", "..", "server", "internal", "service")
	const prefix = "github.com/memory-co/agentuse/server/internal/service"
	allowed := map[string]map[string]bool{
		"user": {}, "device": {}, "agent": {"skill": true},
		"conversation": {"run": true, "file": true, "device": true}, "run": {"skill": true},
		"skill": {"file": true}, "file": {},
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) == 1 {
			if d.Name() != "services.go" && !strings.HasSuffix(d.Name(), "_test.go") {
				t.Errorf("%s adds business implementation to the composition root", rel)
			}
			return nil
		}
		domain := parts[0]
		if domain == "test" {
			if !strings.HasSuffix(d.Name(), "_test.go") {
				t.Errorf("%s adds runtime implementation to the service test directory", rel)
			}
			return nil
		}
		peers, ok := allowed[domain]
		if !ok {
			t.Errorf("unexpected Server service package %s", domain)
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, im := range f.Imports {
			value, _ := strconv.Unquote(im.Path.Value)
			if value == prefix {
				t.Errorf("%s imports the composition root", rel)
			}
			if !strings.HasPrefix(value, prefix+"/") {
				continue
			}
			target := strings.TrimPrefix(value, prefix+"/")
			peer := strings.Split(target, "/")[0]
			if peer != domain && !peers[peer] {
				t.Errorf("%s imports %s outside its allowed dependencies; use a narrow peer contract", rel, peer)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Services declare the capabilities they consume. DTO aliases and an embedded
// dependency container would recreate the removed core layer under another name.
func TestServerServicesUseDirectModelsAndExplicitDependencies(t *testing.T) {
	root := filepath.Join("..", "..", "server", "internal")
	business := map[string]bool{"user": true, "device": true, "agent": true, "conversation": true, "run": true}
	for _, directory := range []string{"service", "store"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(node ast.Node) bool {
				typeSpec, ok := node.(*ast.TypeSpec)
				if !ok {
					return true
				}
				if typeSpec.Assign.IsValid() {
					t.Errorf("%s re-exports type %s; use its owning package directly", path, typeSpec.Name.Name)
				}
				if directory != "service" || !business[filepath.Base(filepath.Dir(path))] || typeSpec.Name.Name != "Service" {
					return true
				}
				if service, ok := typeSpec.Type.(*ast.StructType); ok {
					for _, field := range service.Fields.List {
						if len(field.Names) == 0 {
							t.Errorf("%s embeds a shared dependency container; declare each dependency", path)
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestServerNotificationsDoNotQueryBusinessData(t *testing.T) {
	root := filepath.Join("..", "..", "server", "internal", "notification")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, im := range f.Imports {
			value, _ := strconv.Unquote(im.Path.Value)
			if value == "database/sql" || strings.HasPrefix(value, "github.com/memory-co/agentuse/server/internal/") {
				t.Errorf("%s imports %s; notification audiences must be resolved by the business owner", path, value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
