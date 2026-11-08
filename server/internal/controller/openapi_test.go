package controller

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/server/internal/service"
)

type documentedMedia struct {
	Schema map[string]any `json:"schema"`
}
type documentedOperation struct {
	OperationID string `json:"operationId"`
	Parameters  []struct {
		Name     string `json:"name"`
		In       string `json:"in"`
		Required bool   `json:"required"`
	} `json:"parameters"`
	Security    []map[string][]string `json:"security"`
	RequestBody *struct {
		Required bool                       `json:"required"`
		Content  map[string]documentedMedia `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Description string                     `json:"description"`
		Content     map[string]documentedMedia `json:"content"`
	} `json:"responses"`
}
type serverAPISpec struct {
	OpenAPI    string                                    `json:"openapi"`
	Paths      map[string]map[string]documentedOperation `json:"paths"`
	Security   []map[string][]string                     `json:"security"`
	Components struct {
		Schemas         map[string]map[string]any                          `json:"schemas"`
		SecuritySchemes map[string]struct{ Type, Name, In, Scheme string } `json:"securitySchemes"`
	} `json:"components"`
}

func readServerAPISpec(t *testing.T, e *env, token string) serverAPISpec {
	t.Helper()
	var spec serverAPISpec
	if code := e.req(token, "GET", "/api/openapi.json", nil, &spec).StatusCode; code != 200 {
		t.Fatal(code)
	}
	return spec
}
func (d serverAPISpec) resolve(schema map[string]any) map[string]any {
	if ref, ok := schema["$ref"].(string); ok {
		return d.Components.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")]
	}
	return schema
}

func TestServerDocumentationAuthentication(t *testing.T) {
	e := newEnv(t, Config{FilesDir: t.TempDir()})
	_, token := e.user("alice", service.RoleUser)
	paths := []string{"/api/docs", "/api/openapi.json", "/api/openapi.yaml", "/api/openapi-3.0.json", "/api/openapi-3.0.yaml", "/api/schemas/Message.json"}
	for _, path := range paths {
		if code := e.code("", "GET", path, nil); code != 401 {
			t.Fatalf("unauthenticated %s: %d", path, code)
		}
		if code := e.code("dk_invalid", "GET", path, nil); code != 401 {
			t.Fatalf("device credential opened documentation %s: %d", path, code)
		}
	}
	login := e.req("", "POST", "/api/login", map[string]string{"token": token}, nil)
	cookies := login.Cookies()
	if login.StatusCode != 200 || len(cookies) != 1 {
		t.Fatal("login did not set a cookie")
	}
	for _, path := range paths {
		for _, useCookie := range []bool{false, true} {
			r := httptest.NewRequest("GET", path, nil)
			if useCookie {
				r.AddCookie(cookies[0])
			} else {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			e.srv.ServeHTTP(w, r)
			if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("authenticated %s: %d %s", path, w.Code, w.Body.String())
			}
			if path == "/api/docs" && (!strings.Contains(w.Body.String(), "/api/openapi.yaml") || !strings.Contains(w.Body.String(), `tryItCredentialsPolicy="same-origin"`)) {
				t.Fatal("interactive docs cannot use the server login cookie")
			}
		}
	}
	if code := e.req("invalid", "GET", "/api/docs", nil, nil, "Cookie", cookies[0].String()).StatusCode; code != 401 {
		t.Fatal("invalid Bearer token must take precedence over a valid cookie", code)
	}
}

func TestServerOpenAPIRoutesSchemasAndCredentials(t *testing.T) {
	e := newEnv(t, Config{FilesDir: t.TempDir()})
	_, token := e.user("alice", service.RoleUser)
	spec := readServerAPISpec(t, e, token)
	if !strings.HasPrefix(spec.OpenAPI, "3.1.") || len(spec.Paths) < 35 {
		t.Fatal("incomplete OpenAPI document", spec.OpenAPI, len(spec.Paths))
	}
	if len(spec.Security) != 2 || spec.Components.SecuritySchemes["cookieAuth"].Name != cookieName || spec.Components.SecuritySchemes["bearerAuth"].Scheme != "bearer" || spec.Components.SecuritySchemes["workToken"].Scheme != "bearer" || spec.Components.SecuritySchemes["deviceKey"].Scheme != "bearer" {
		t.Fatal("incorrect authentication schemes")
	}
	ids := map[string]bool{}
	for path, methods := range spec.Paths {
		if strings.Contains(path, "...") {
			t.Fatal("ServeMux wildcard leaked into OpenAPI", path)
		}
		for method, op := range methods {
			if op.OperationID == "" || ids[op.OperationID] {
				t.Fatal("missing or repeated operation ID", path, method)
			}
			ids[op.OperationID] = true
			runtimePath, pattern := path, strings.ToUpper(method)+" "+path
			for _, part := range strings.Split(path, "/") {
				if !strings.HasPrefix(part, "{") {
					continue
				}
				found := false
				for _, param := range op.Parameters {
					if param.Name == strings.Trim(part, "{}") && param.In == "path" && param.Required {
						found = true
					}
				}
				if !found {
					t.Fatal("missing required path parameter", path, part)
				}
				runtimePath = strings.ReplaceAll(runtimePath, part, "test")
			}
			if strings.HasSuffix(path, "/files/{path}") {
				pattern = strings.ReplaceAll(pattern, "{path}", "{path...}")
				runtimePath = strings.TrimSuffix(runtimePath, "test") + "nested/report.txt"
			}
			_, registered := e.srv.mux.Handler(httptest.NewRequest(strings.ToUpper(method), runtimePath, nil))
			if registered != pattern {
				t.Fatalf("documented route is not registered: %s, got %s", pattern, registered)
			}
		}
	}
	for _, path := range []string{"/api/login", "/api/logout", protocol.RegisterPath} {
		if op := spec.Paths[path]["post"]; op.Security == nil || len(op.Security) != 0 {
			t.Fatal("public endpoint requires credentials", path)
		}
	}
	for _, path := range []string{protocol.MePath, protocol.WSPath} {
		if !reflect.DeepEqual(spec.Paths[path]["get"].Security, []map[string][]string{{"deviceKey": {}}}) {
			t.Fatal("device endpoint allows the wrong credentials", path)
		}
	}
	if len(spec.Paths["/api/conversations/{id}"]["get"].Security) != 4 || len(spec.Paths["/api/conversations/{id}/attachments/{attachment_id}"]["delete"].Security) != 3 {
		t.Fatal("incorrect conversation or attachment credential scope")
	}
	for path, status := range map[string]string{"/api/conversations": "201", "/api/agents": "201", "/api/conversations/{id}/messages": "201", "/api/conversations/{id}/runs/{run_id}/requests/{request_id}/reply": "202"} {
		if _, ok := spec.Paths[path]["post"].Responses[status]; !ok {
			t.Fatal("incorrect success status", path, status)
		}
	}
	for _, path := range []string{"/api/conversations/{id}", "/api/agents/{id}", "/api/skills/{id}", "/api/conversations/{id}/attachments/{attachment_id}"} {
		if len(spec.Paths[path]["delete"].Responses["204"].Content) != 0 {
			t.Fatal("204 response declares a body", path)
		}
	}
	for _, path := range []string{"/api/conversations/{id}/resolve", "/api/conversations/{id}/runs/{run_id}/cancel", "/api/devices/{id}/reset"} {
		if spec.Paths[path]["post"].RequestBody != nil {
			t.Fatal("bodyless action requires a JSON request", path)
		}
	}
	for _, path := range []string{"/api/conversations", "/api/conversations/{id}/messages"} {
		schema := spec.resolve(spec.Paths[path]["post"].RequestBody.Content["application/json"].Schema)
		properties := schema["properties"].(map[string]any)
		if properties["to_agent_ids"] == nil || properties["to_user_ids"] == nil || properties["target_agent_ids"] != nil || properties["agent_id"] != nil {
			t.Fatal("incorrect message recipient schema", path, properties)
		}
		if properties["input"] != nil || properties["mode"] != nil || properties["defer_start"] != nil {
			t.Fatal("obsolete execution wrapper in conversation API", properties)
		}
	}
	message := spec.Components.Schemas["Message"]["properties"].(map[string]any)
	if message["to_agent_ids"] == nil || message["to_user_ids"] == nil {
		t.Fatal("recipient response fields missing")
	}
	for _, path := range []string{"/api/skills", "/api/skills/{id}/versions", "/api/conversations/{id}/attachments"} {
		media := spec.Paths[path]["post"].RequestBody.Content
		properties := media["multipart/form-data"].Schema["properties"].(map[string]any)
		if properties["file"].(map[string]any)["format"] != "binary" || media["application/json"].Schema != nil {
			t.Fatal("upload incorrectly documented as JSON", path)
		}
	}
	file := spec.Paths["/api/conversations/{id}/files/{path}"]
	if file["put"].RequestBody.Content["application/octet-stream"].Schema["format"] != "binary" || file["get"].Responses["200"].Content["application/octet-stream"].Schema["format"] != "binary" {
		t.Fatal("raw file content is not documented as binary")
	}
	if _, ok := spec.Paths[protocol.WSPath]["get"].Responses["101"]; !ok {
		t.Fatal("WebSocket documented as a JSON response")
	}
	// All references must resolve, including nested request and response models.
	var raw any
	data, _ := json.Marshal(spec)
	json.Unmarshal(data, &raw)
	var refs func(any)
	refs = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok && strings.HasPrefix(ref, "#/components/schemas/") {
				if spec.resolve(v) == nil {
					t.Fatal("unresolved schema", ref)
				}
			}
			for _, child := range v {
				refs(child)
			}
		case []any:
			for _, child := range v {
				refs(child)
			}
		}
	}
	refs(raw)
}

func TestServerDocumentationConcurrentFirstRequests(t *testing.T) {
	e := newEnv(t, Config{FilesDir: t.TempDir()})
	_, token := e.user("alice", service.RoleUser)
	var requests sync.WaitGroup
	for range 8 {
		for _, path := range []string{"/api/openapi.json", "/api/openapi.yaml", "/api/openapi-3.0.json", "/api/openapi-3.0.yaml"} {
			requests.Add(1)
			go func() {
				defer requests.Done()
				r := httptest.NewRequest("GET", path, nil)
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				e.srv.ServeHTTP(w, r)
				if w.Code != 200 || w.Body.Len() == 0 {
					t.Errorf("concurrent documentation request %s: %d", path, w.Code)
				}
			}()
		}
	}
	requests.Wait()
}
