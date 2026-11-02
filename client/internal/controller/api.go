package controller

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/memory-co/agentuse/client/internal/store"
)

// Register through Huma's adapter so the runtime routes and OpenAPI operations
// share one definition. Existing handlers retain their error and cursor contracts.
func (c *Controller) registerAPI() {
	config := huma.DefaultConfig("AgentUse Client API", "1.0.0")
	config.OpenAPIPath = "/api/openapi"
	config.DocsPath = "/api/docs"
	config.SchemasPath = "/api/schemas"
	config.Info.Description = "Client 本机 API。先通过 agentuse ui 登录，再打开文档使用 cookie 调试，或通过 Authorize 输入本机 Bearer token。平台任务只读；local endpoint 支持创建 conversation、执行及控制 harness。所有列表都限定在指定 endpoint / conversation / session，不提供全局 run 遍历。"
	config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearerAuth": {Type: "http", Scheme: "bearer", Description: "agentuse ui 使用的本机 token"},
		"cookieAuth": {Type: "apiKey", In: "cookie", Name: "agentuse_client_" + c.port, Description: "登录后自动设置的 HttpOnly cookie"},
	}
	config.Security = []map[string][]string{{"cookieAuth": {}}, {"bearerAuth": {}}}
	api := humago.New(c.mux, config)
	schema := func(v any) *huma.Schema { return api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(v), true, "") }
	bind := func(method, path, id, summary, tag string, output any, status int, input any, handler http.HandlerFunc, params ...*huma.Param) {
		op := &huma.Operation{Method: method, Path: path, OperationID: id, Summary: summary, Tags: []string{tag}, Parameters: params, Responses: map[string]*huma.Response{}}
		for _, part := range strings.Split(path, "/") {
			if strings.HasPrefix(part, "{") {
				op.Parameters = append(op.Parameters, &huma.Param{Name: strings.Trim(part, "{}"), In: "path", Required: true, Schema: &huma.Schema{Type: "string"}})
			}
		}
		op.Responses[strconv.Itoa(status)] = &huma.Response{Description: http.StatusText(status), Content: map[string]*huma.MediaType{"application/json": {Schema: schema(output)}}}
		for _, code := range []int{400, 401, 403, 404, 405, 500, 503} {
			op.Responses[strconv.Itoa(code)] = &huma.Response{Description: http.StatusText(code), Content: map[string]*huma.MediaType{"application/json": {Schema: schema(apiErrorBody{})}}}
		}
		if method == http.MethodPost {
			op.MaxBodyBytes = 300 << 10
			op.RequestBody = &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {Schema: schema(input)}}}
			for _, code := range []int{415, 503} {
				op.Responses[strconv.Itoa(code)] = &huma.Response{Description: http.StatusText(code), Content: map[string]*huma.MediaType{"application/json": {Schema: schema(apiErrorBody{})}}}
			}
		}
		api.OpenAPI().AddOperation(op)
		api.Adapter().Handle(op, func(ctx huma.Context) {
			r, w := humago.Unwrap(ctx)
			handler(w, r)
		})
	}
	query := func(name, description string) *huma.Param {
		return &huma.Param{Name: name, In: "query", Description: description, Schema: &huma.Schema{Type: "string"}}
	}
	pagination := func() []*huma.Param {
		min, max := float64(1), float64(200)
		return []*huma.Param{{Name: "limit", In: "query", Description: "每页条数", Schema: &huma.Schema{Type: "integer", Minimum: &min, Maximum: &max, Default: 50}}, query("cursor", "上一页 next_cursor；绑定当前路径及筛选条件")}
	}
	public := func(fn func() any) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if fn == nil {
				jsonResponse(w, map[string]any{})
				return
			}
			v := fn()
			if failed, ok := v.(ReadFailure); ok {
				failure(w, failed.Err)
				return
			}
			jsonResponse(w, v)
		}
	}
	bind("GET", "/api/endpoints", "list-endpoints", "列出 endpoints（包含内置 local，不含凭据）", "endpoint", endpointList{}, 200, nil, public(c.data.Endpoints))
	bind("GET", "/api/settings", "get-settings", "读取本机配置及待重启项", "setting", settingsResponse{}, 200, nil, public(c.data.Settings))
	bind("GET", "/api/harnesses", "list-harnesses", "读取已缓存的 harness 探测结果", "harness", []harnessView{}, 200, nil, public(c.data.Harnesses))
	bind("GET", "/api/status", "get-status", "读取连接与执行状态", "run", statusResponse{}, 200, nil, public(c.data.Status))
	ep := query("endpoint_id", "endpoint 的目录标识；本地模式为 local")
	ep.Required = true
	bind("GET", "/api/conversations", "list-conversations", "列出指定 endpoint 的 conversations", "conversation", apiPage[store.Conversation]{}, 200, nil, c.conversations, append(pagination(), ep)...)
	bind("GET", "/api/local/harnesses", "probe-local-harnesses", "读取启动时可用于本地执行的 harness", "harness", harnessList{}, 200, nil, c.localHarnesses)
	bind("POST", "/api/local/conversations", "create-local-conversation", "创建本地 conversation", "conversation", store.Conversation{}, 201, createConversationBody{}, c.localCreate)
	bind("POST", "/api/local/conversations/{conversation}/runs", "start-local-run", "启动本地 harness run", "run", store.Run{}, 202, startRunBody{}, c.localStart)
	bind("POST", "/api/local/conversations/{conversation}/sessions/{session}/runs/{run}/attempts/{attempt}/{action}", "control-local-run", "控制本地 run：cancel / steer / answer", "run", okResponse{}, 200, controlRunBody{}, c.localControl)
	control := api.OpenAPI().Paths["/api/local/conversations/{conversation}/sessions/{session}/runs/{run}/attempts/{attempt}/{action}"].Post
	control.Description = "action=cancel 时发送 {}；steer 时发送 {\"prompt\":\"补充内容\"}；answer 时发送 {\"tool_use_id\":\"...\",\"answers\":{\"问题\":\"回答\"}}。仅控制当前 local run。"
	for _, p := range control.Parameters {
		if p.Name == "action" {
			p.Schema.Enum = []any{"cancel", "steer", "answer"}
		}
	}
	base := "/api/conversations/{endpoint}/{conversation}"
	bind("GET", base, "get-conversation", "读取 conversation", "conversation", store.Conversation{}, 200, nil, c.conversation)
	bind("GET", base+"/sessions", "list-sessions", "列出 conversation 的原生 harness sessions", "session", apiPage[store.Session]{}, 200, nil, c.sessions, append(pagination(), query("harness", "harness 名称"), query("status", "session 状态"))...)
	bind("GET", base+"/sessions/{session}", "get-session", "读取 session", "session", store.Session{}, 200, nil, c.session)
	filters := func() []*huma.Param {
		return append(pagination(), query("status", "run 状态"), &huma.Param{Name: "since", In: "query", Description: "开始时间，Unix 毫秒", Schema: &huma.Schema{Type: "integer", Format: "int64"}}, &huma.Param{Name: "until", In: "query", Description: "结束时间，Unix 毫秒", Schema: &huma.Schema{Type: "integer", Format: "int64"}})
	}
	bind("GET", base+"/sessions/{session}/runs", "list-session-runs", "分页读取 session runs（不含 output / events）", "run", store.Page{}, 200, nil, c.list, filters()...)
	bind("GET", base+"/sessions/{session}/runs/{run}/attempts/{attempt}", "get-run-attempt", "读取 run attempt 详情、输出和事件", "run", store.Run{}, 200, nil, c.detail)
	bind("GET", base+"/sessions/{session}/usage", "get-session-usage", "统计仍保留在本机的 session runs 用量", "session", usageResponse{}, 200, nil, c.stats, filters()...)
}

type apiErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
type apiPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
}
type createConversationBody struct {
	Title string `json:"title" doc:"任意 conversation 标题，非空，最多 4096 字节"`
}
type startRunBody struct {
	Harness string `json:"harness" doc:"已安装的 harness 名称"`
	Prompt  string `json:"prompt,omitempty" doc:"未提供 input 时必填"`
	Input   any    `json:"input,omitempty" doc:"自定义 harness 原生 JSON 输入；优先于 prompt，最多 256 KiB"`
}
type controlRunBody struct {
	Prompt    string            `json:"prompt,omitempty"`
	ToolUseID string            `json:"tool_use_id,omitempty"`
	Answers   map[string]string `json:"answers,omitempty"`
}
type okResponse struct {
	OK bool `json:"ok"`
}
type usageResponse struct {
	Items []store.Statistic `json:"items"`
	Scope string            `json:"scope" enum:"retained_session_runs"`
}
type endpointView struct {
	ID        string `json:"endpoint_id"`
	Name      string `json:"name"`
	ServerURL string `json:"server_url"`
	Enabled   bool   `json:"enabled"`
	Mode      string `json:"mode" enum:"local,server"`
}
type endpointList struct {
	Items []endpointView `json:"items"`
}
type harnessView struct {
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	Version  string `json:"version,omitempty"`
	Found    bool   `json:"found"`
	Disabled bool   `json:"disabled,omitempty"`
	Revision string `json:"revision,omitempty"`
}
type harnessList struct {
	Items []harnessView `json:"items"`
}
type settingsResponse struct {
	LocalHTTP struct {
		Enabled bool   `json:"enabled"`
		Host    string `json:"host"`
		Port    int    `json:"port"`
	} `json:"local_http"`
	Session struct {
		WorkRoot string `json:"work_root"`
		Quota    *struct {
			MaxBytes int64 `json:"max_bytes"`
		} `json:"quota,omitempty"`
	} `json:"session"`
}

type statusResponse struct {
	Endpoints []struct {
		EndpointID string `json:"endpoint_id"`
		State      string `json:"state"`
		ActiveRuns int    `json:"active_runs"`
	} `json:"endpoints"`
	RunningRuns int `json:"running_runs"`
}
