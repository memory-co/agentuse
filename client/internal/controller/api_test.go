package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestAPIDocumentationAuthenticationAndContract(t *testing.T) {
	token := strings.Repeat("a", 64)
	c, err := New(nil, nil, "127.0.0.1:8091", token)
	if err != nil {
		t.Fatal(err)
	}
	call := func(path string, cookie *http.Cookie, bearer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://127.0.0.1:8091"+path, nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		c.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/docs", "/api/openapi.json", "/api/openapi.yaml", "/api/schemas/Run.json"} {
		if w := call(path, nil, ""); w.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", path, w.Code)
		}
	}
	spec := call("/api/openapi.json", nil, token)
	if spec.Code != 200 {
		t.Fatal(spec.Code, spec.Body.String())
	}
	var doc struct {
		OpenAPI string `json:"openapi"`
		Paths   map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Parameters  []struct {
				Name     string
				In       string
				Required bool
			} `json:"parameters"`
			Responses   map[string]json.RawMessage `json:"responses"`
			RequestBody json.RawMessage            `json:"requestBody"`
		} `json:"paths"`
		Security   []map[string][]string `json:"security"`
		Components struct {
			Schemas         map[string]json.RawMessage                         `json:"schemas"`
			SecuritySchemes map[string]struct{ Type, Name, In, Scheme string } `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal(spec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.1.") || len(doc.Paths) != 16 {
		t.Fatalf("unexpected document version or routes: %s %d", doc.OpenAPI, len(doc.Paths))
	}
	if len(doc.Security) != 2 || doc.Security[0]["cookieAuth"] == nil || doc.Components.SecuritySchemes["cookieAuth"].Name != "agentuse_client_8091" || doc.Components.SecuritySchemes["bearerAuth"].Scheme != "bearer" {
		t.Fatal("missing authentication schemes")
	}
	if _, ok := doc.Paths["/api/runs"]; ok {
		t.Fatal("global run enumeration documented")
	}
	for _, foreign := range []string{"attempt_id", "harness_run_ids", "server_confirmed", "/attempts/"} {
		if strings.Contains(spec.Body.String(), foreign) {
			t.Fatalf("legacy identity in public API: %s", foreign)
		}
	}
	ids := map[string]bool{}
	for path, methods := range doc.Paths {
		for method, op := range methods {
			if op.OperationID == "" || ids[op.OperationID] {
				t.Fatalf("invalid operation ID: %s", path)
			}
			ids[op.OperationID] = true
			for _, status := range []string{"400", "401", "403", "404", "500"} {
				if _, ok := op.Responses[status]; !ok {
					t.Fatalf("%s missing %s", path, status)
				}
			}
			for _, part := range strings.Split(path, "/") {
				if !strings.HasPrefix(part, "{") {
					continue
				}
				name := strings.Trim(part, "{}")
				found := false
				for _, p := range op.Parameters {
					if p.Name == name && p.In == "path" && p.Required {
						found = true
					}
				}
				if !found {
					t.Fatalf("%s missing path parameter %s", path, name)
				}
			}
			if method == "post" && len(op.RequestBody) == 0 {
				t.Fatalf("%s missing JSON request body", path)
			}
		}
	}
	if doc.Paths["/api/local/conversations"]["post"].Responses["201"] == nil || doc.Paths["/api/local/conversations/{conversation}/runs"]["post"].Responses["202"] == nil {
		t.Fatal("incorrect mutation response codes")
	}
	if len(doc.Components.Schemas["Run"]) == 0 || len(doc.Components.Schemas["Session"]) == 0 {
		t.Fatal("missing service response schemas")
	}
	var usageSchema map[string]any
	if err := json.Unmarshal(doc.Components.Schemas["RunUsage"], &usageSchema); err != nil || !reflect.DeepEqual(usageSchema["type"], []any{"object", "null"}) {
		t.Fatal("Run usage must be nullable in OpenAPI", usageSchema, err)
	}
	for _, field := range []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens"} {
		counter := usageSchema["properties"].(map[string]any)[field].(map[string]any)
		if !reflect.DeepEqual(counter["type"], []any{"integer", "null"}) || counter["minimum"] != float64(0) {
			t.Fatal("token measurements must be nullable non-negative integers", field, counter)
		}
	}
	var controlResponse struct {
		Content map[string]struct {
			Schema struct {
				OneOf []struct {
					Ref string `json:"$ref"`
				} `json:"oneOf"`
			} `json:"schema"`
		} `json:"content"`
	}
	control := doc.Paths["/api/local/conversations/{conversation}/sessions/{session}/{action}"]["post"]
	if err := json.Unmarshal(control.Responses["200"], &controlResponse); err != nil {
		t.Fatal(err)
	}
	variants := controlResponse.Content["application/json"].Schema.OneOf
	if len(variants) != 2 {
		t.Fatal("control response must document Run for send and acknowledgement for reply/cancel", controlResponse)
	}
	var runResponse, acknowledgement bool
	for _, variant := range variants {
		name := strings.TrimPrefix(variant.Ref, "#/components/schemas/")
		var model struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(doc.Components.Schemas[name], &model); err != nil {
			t.Fatal("unresolved control response schema", variant.Ref, err)
		}
		runResponse = runResponse || name == "Run"
		acknowledgement = acknowledgement || len(model.Properties["ok"]) > 0
	}
	if !runResponse || !acknowledgement {
		t.Fatal("control response omitted an action's result", variants)
	}
	cookies := spec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no login cookie")
	}
	for _, path := range []string{"/api/docs", "/api/openapi.yaml", "/api/schemas/Run.json"} {
		w := call(path, cookies[0], "")
		if w.Code != 200 {
			t.Fatalf("authenticated %s: %d %s", path, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("authenticated documentation cacheable")
		}
	}
	if w := call("/api/openapi.json", cookies[0], "wrong"); w.Code != 401 {
		t.Fatal("invalid Bearer overrode cookie authentication")
	}
	docs := call("/api/docs", cookies[0], "").Body.String()
	if !strings.Contains(docs, "/api/openapi.yaml") || !strings.Contains(docs, `tryItCredentialsPolicy="same-origin"`) {
		t.Fatal("interactive docs not configured for authenticated local API")
	}
}
