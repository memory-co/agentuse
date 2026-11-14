package controller

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/skillbundle"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

// Huma registers documentation routes through the same router as business routes.
// Protect its generated HTML, specifications, and schemas with normal user auth.
type documentationRouter struct {
	server *Server
	mu     sync.Mutex
}

func (m *documentationRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.server.mux.ServeHTTP(w, r)
}
func (m *documentationRouter) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	if pattern == "GET /api/docs" || strings.HasPrefix(pattern, "GET /api/openapi") || strings.HasPrefix(pattern, "GET /api/schemas/") {
		render := handler
		next := m.server.auth(func(w http.ResponseWriter, r *http.Request) {
			// Huma v2.34 lazily caches the serialized specifications without a lock.
			m.mu.Lock()
			defer m.mu.Unlock()
			render(w, r)
		})
		handler = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			next(w, r)
		}
	}
	m.server.mux.HandleFunc(pattern, handler)
}

type apiAccess struct {
	wrap        func(http.HandlerFunc) http.HandlerFunc
	security    []map[string][]string
	description string
}

// Each binding is the single definition of the runtime route and its OpenAPI
// operation. Existing handlers keep their HTTP parsing, streaming, and errors.
func (s *Server) registerAPI() {
	config := huma.DefaultConfig("AgentUse Server API", "1.0.0")
	config.OpenAPIPath = "/api/openapi"
	config.DocsPath = "/api/docs"
	config.SchemasPath = "/api/schemas"
	config.Info.Description = "AgentUse Server API。登录管理页面后可使用 cookie 在文档中调试，也可通过 Authorize 输入 Bearer token。Server 对话统一使用 /api/conversations 和 conversation_id；session_id 表示 Client 本地执行会话。设备入口使用 device 命名。消息用 to_agent_ids 和 to_user_ids 指定收件人；只有 Agent 收件人触发执行，实际 run_id 由 Client accepted 返回。"
	config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"cookieAuth": {Type: "apiKey", In: "cookie", Name: cookieName, Description: "POST /api/login 或登录链接设置的 HttpOnly cookie"},
		"bearerAuth": {Type: "http", Scheme: "bearer", Description: "用户令牌 ut_…"},
		"workToken":  {Type: "http", Scheme: "bearer", Description: "协调者执行期间有效的 wt_…，权限限于对应工作"},
		"deviceKey":  {Type: "http", Scheme: "bearer", Description: "注册设备返回的 dk_…，只能访问授权的会话及固定资源版本"},
	}
	config.Security = []map[string][]string{{"cookieAuth": {}}, {"bearerAuth": {}}}
	api := humago.New(&documentationRouter{server: s}, config)
	schema := func(v any) *huma.Schema { return api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(v), true, "") }
	jsonResponse := func(status int, v any) *huma.Response {
		r := &huma.Response{Description: http.StatusText(status)}
		if v != nil {
			r.Content = map[string]*huma.MediaType{"application/json": {Schema: schema(v)}}
		}
		return r
	}
	public := apiAccess{security: []map[string][]string{}}
	user := apiAccess{wrap: s.auth, security: config.Security}
	admin := apiAccess{wrap: s.admin, security: config.Security, description: "仅管理员可调用。"}
	work := apiAccess{wrap: s.workAuth, security: []map[string][]string{{"cookieAuth": {}}, {"bearerAuth": {}}, {"workToken": {}}}, description: "也接受工作令牌，访问范围限于对应工作。"}
	resource := apiAccess{wrap: s.resourceAuth, security: []map[string][]string{{"cookieAuth": {}}, {"bearerAuth": {}}, {"workToken": {}}, {"deviceKey": {}}}, description: "设备和工作令牌只能读取分配给自己的固定资源版本。"}
	conversation := apiAccess{wrap: s.conversationReadAuth, security: resource.security, description: "工作令牌限于对应工作；设备密钥限于已投递给该设备的会话。"}
	attachment := apiAccess{wrap: s.attachmentAuth, security: resource.security, description: "工作令牌限于对应工作；设备密钥只能访问已投递给本设备的会话。设备上传必须通过 X-Agentuse-Run 指定本设备在该会话的活动 run。"}
	attachmentDelete := apiAccess{wrap: s.attachmentAuth, security: work.security, description: "仅用户或工作令牌可删除附件，设备密钥不能删除。"}
	device := apiAccess{security: []map[string][]string{{"deviceKey": {}}}}

	bind := func(method, path, id, summary, tag string, access apiAccess, input, output any, status int, handler http.HandlerFunc, params ...*huma.Param) *huma.Operation {
		op := &huma.Operation{Method: method, Path: strings.ReplaceAll(path, "...}", "}"), OperationID: id, Summary: summary, Description: access.description, Tags: []string{tag}, Security: access.security, Parameters: append([]*huma.Param{}, params...), Responses: map[string]*huma.Response{}}
		for _, part := range strings.Split(op.Path, "/") {
			if strings.HasPrefix(part, "{") {
				op.Parameters = append(op.Parameters, &huma.Param{Name: strings.Trim(part, "{}"), In: "path", Required: true, Schema: &huma.Schema{Type: "string"}})
			}
		}
		for _, credentials := range access.security {
			if _, ok := credentials["bearerAuth"]; ok {
				op.Parameters = append(op.Parameters, &huma.Param{Name: actAsHeader, In: "header", Description: "仅管理员用户令牌可代为此用户执行；用户不存在时自动创建。设备或工作令牌不支持代为用户。", Schema: &huma.Schema{Type: "string"}})
				break
			}
		}
		if access.wrap != nil {
			handler = access.wrap(handler)
		}
		for _, code := range []int{400, 401, 403, 404, 409, 500} {
			op.Responses[strconv.Itoa(code)] = jsonResponse(code, apiErrorBody{})
		}
		op.Responses[strconv.Itoa(status)] = jsonResponse(status, output)
		if input != nil {
			op.RequestBody = &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {Schema: schema(input)}}}
		}
		api.OpenAPI().AddOperation(op)
		// OpenAPI uses {path}; ServeMux needs {path...} to capture nested files.
		route := *op
		route.Path = path
		api.Adapter().Handle(&route, func(ctx huma.Context) {
			r, w := humago.Unwrap(ctx)
			handler(w, r)
		})
		return op
	}
	query := func(name, description string) *huma.Param {
		return &huma.Param{Name: name, In: "query", Description: description, Schema: &huma.Schema{Type: "string"}}
	}
	binary := &huma.Schema{Type: "string", Format: "binary"}
	binaryResponse := func(op *huma.Operation, mediaType string) {
		op.Responses["200"] = &huma.Response{Description: "文件内容", Content: map[string]*huma.MediaType{mediaType: {Schema: binary}}}
		op.Responses["503"] = jsonResponse(503, apiErrorBody{})
	}
	multipart := func(op *huma.Operation, properties map[string]*huma.Schema) {
		op.RequestBody = &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"multipart/form-data": {Schema: &huma.Schema{Type: "object", Properties: properties, Required: []string{"file"}}}}}
		op.Responses["413"] = jsonResponse(413, apiErrorBody{})
		op.Responses["415"] = jsonResponse(415, apiErrorBody{})
	}

	bind("POST", "/api/login", "login", "登录并设置会话 cookie", "auth", public, loginBody{}, agentuse.User{}, 200, s.handleLogin)
	bind("POST", "/api/logout", "logout", "清除登录 cookie", "auth", public, nil, nil, 204, s.handleLogout)
	bind("GET", "/api/browser/ws", "browser-notifications", "Browser change notifications over WebSocket; fetch data through HTTP APIs", "browser", apiAccess{security: config.Security}, nil, nil, 101, s.handleBrowserWS, query("as", "Acting user ID; administrators only. Authentication uses the login cookie."))
	bind("GET", "/api/me", "get-current-user", "读取当前用户", "auth", user, nil, agentuse.User{}, 200, s.handleMe)
	bind("GET", "/api/users", "list-users", "列出用户", "user", admin, nil, []agentuse.User{}, 200, s.handleListUsers)
	bind("POST", "/api/users", "create-user", "创建用户并返回登录令牌", "user", admin, agentuse.CreateUserRequest{}, agentuse.UserToken{}, 201, s.handleCreateUser)
	bind("POST", "/api/users/{id}/token", "reset-user-token", "重置用户登录令牌", "user", admin, nil, agentuse.UserToken{}, 200, s.handleResetUserToken)
	bind("GET", "/api/harnesses", "list-harnesses", "列出设备上报的 harness", "harness", user, nil, []agentuse.Harness{}, 200, s.handleListHarnesses)
	bind("GET", "/api/agents", "list-agents", "列出可见的 Agent", "agent", work, nil, []agentuse.Agent{}, 200, s.handleListAgents)
	bind("POST", "/api/agents", "create-agent", "创建 Agent 及其首个配置版本", "agent", user, agentuse.CreateAgentRequest{}, agentuse.Agent{}, 201, s.handleCreateAgent)
	bind("GET", "/api/agents/{id}", "get-agent", "读取 Agent 当前版本", "agent", user, nil, agentuse.Agent{}, 200, s.handleGetAgent)
	publish := bind("POST", "/api/agents/{id}/versions", "publish-agent-version", "发布固定 Agent 配置版本", "agent", user, agentuse.PublishAgentVersionRequest{}, agentuse.AgentVersion{}, 201, s.handlePublishAgentVersion)
	publish.Responses["200"] = jsonResponse(200, agentuse.AgentVersion{})
	publish.Description = "base_version_id 必须匹配当前版本，冲突返回 409；内容没有变化时返回现有版本及 200，否则创建新版本并返回 201。"
	bind("GET", "/api/agents/{id}/versions", "list-agent-versions", "列出 Agent 配置版本", "agent", user, nil, []agentuse.AgentVersion{}, 200, s.handleAgentVersions)
	bind("GET", "/api/agents/{id}/versions/{version_id}", "get-agent-version", "读取固定 Agent 配置版本", "agent", resource, nil, agentuse.AgentVersion{}, 200, s.handleAgentVersion)
	bind("DELETE", "/api/agents/{id}", "delete-agent", "删除 Agent，保留历史版本和署名", "agent", user, nil, nil, 204, s.handleDeleteAgent)

	bind("GET", "/api/skills", "list-skills", "列出 skills", "skill", user, nil, []agentuse.Skill{}, 200, s.handleListSkills)
	createSkill := bind("POST", "/api/skills", "create-skill", "上传 ZIP 并创建 skill 首个版本", "skill", user, nil, agentuse.Skill{}, 201, s.handlePublishSkill)
	multipart(createSkill, map[string]*huma.Schema{"file": binary})
	createSkill.Description = "ZIP 内包含 SKILL.md。压缩包最大 " + strconv.FormatInt(skillbundle.MaxArchive, 10) + " 字节。"
	bind("GET", "/api/skills/{id}", "get-skill", "读取 skill 当前版本", "skill", user, nil, agentuse.Skill{}, 200, s.handleGetSkill)
	bind("DELETE", "/api/skills/{id}", "delete-skill", "删除 skill，保留已引用版本", "skill", user, nil, nil, 204, s.handleDeleteSkill)
	publishSkill := bind("POST", "/api/skills/{id}/versions", "publish-skill-version", "上传 ZIP 并发布 skill 版本", "skill", user, nil, agentuse.Skill{}, 201, s.handlePublishSkill)
	multipart(publishSkill, map[string]*huma.Schema{"file": binary, "base_version_id": {Type: "string", Description: "必须匹配当前版本；不匹配时返回 409"}})
	publishSkill.RequestBody.Content["multipart/form-data"].Schema.Required = []string{"file", "base_version_id"}
	bind("GET", "/api/skills/{id}/versions", "list-skill-versions", "列出 skill 版本", "skill", user, nil, []agentuse.SkillVersion{}, 200, s.handleSkillVersions)
	bind("GET", "/api/skills/{id}/versions/{version_id}", "get-skill-version", "读取固定 skill 版本", "skill", resource, nil, agentuse.SkillVersion{}, 200, s.handleSkillVersion)
	packageAccess := resource
	packageAccess.wrap = nil // This streaming handler already authenticates each credential kind.
	getPackage := bind("GET", "/api/skills/{id}/versions/{version_id}/package", "download-skill-package", "下载固定版本的 skill ZIP", "skill", packageAccess, nil, nil, 200, s.handleSkillPackage)
	binaryResponse(getPackage, "application/zip")

	getFile := bind("GET", "/api/conversations/{id}/files/{path...}", "download-conversation-file", "读取会话文件，路径可包含子目录", "file", attachment, nil, nil, 200, s.handleConversationFile)
	binaryResponse(getFile, "application/octet-stream")
	putFile := bind("PUT", "/api/conversations/{id}/files/{path...}", "upload-conversation-file", "写入会话文件，路径可包含子目录", "file", attachment, nil, nil, 204, s.handleConversationFile)
	putFile.RequestBody = &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/octet-stream": {Schema: binary}}}
	putFile.Responses["503"] = jsonResponse(503, apiErrorBody{})

	bind("GET", "/api/devices", "list-devices", "列出设备及安装命令", "device", user, nil, []deviceView{}, 200, s.handleListDevices)
	createDevice := bind("POST", "/api/devices", "create-device", "创建设备并返回安装命令", "device", user, createDeviceBody{}, deviceView{}, 201, s.handleCreateDevice)
	createDevice.RequestBody.Required = false
	bind("DELETE", "/api/devices/{id}", "delete-device", "删除设备并断开连接", "device", user, nil, nil, 204, s.handleDeleteDevice)
	bind("POST", "/api/devices/{id}/reset", "reset-device-setup", "重置设备安装令牌", "device", user, nil, deviceView{}, 200, s.handleResetDevice)
	bind("PUT", "/api/devices/{id}/harnesses/{harness}", "toggle-device-harness", "启用或禁用设备的 harness", "device", user, toggleHarnessBody{}, deviceView{}, 200, s.handleToggleHarness)
	bind("GET", "/api/works", "list-works", "列出工作", "work", user, nil, []agentuse.Work{}, 200, s.handleListWorks)
	bind("POST", "/api/works", "create-work", "创建工作及协调者会话", "work", user, agentuse.CreateWorkRequest{}, agentuse.CreateWorkResponse{}, 201, s.handleCreateWork)
	bind("GET", "/api/works/{id}", "get-work", "读取工作", "work", user, nil, agentuse.Work{}, 200, s.handleGetWork)
	bind("PATCH", "/api/works/{id}", "update-work", "更新工作说明及协调者", "work", user, agentuse.WorkPatch{}, agentuse.Work{}, 200, s.handleUpdateWork)
	bind("DELETE", "/api/works/{id}", "delete-work", "删除工作及所属会话", "work", user, nil, nil, 204, s.handleDeleteWork)
	management := apiAccess{wrap: s.workManagementAuth, security: config.Security, description: "仅工作所有者或管理员可操作，工作范围由路径固定。"}
	bind("GET", "/api/works/{work_id}/threads", "list-work-threads", "列出工作线程", "work", management, nil, []agentuse.Conversation{}, 200, s.handleListConversations)
	bind("POST", "/api/works/{work_id}/threads", "create-work-thread", "在指定工作中创建线程", "work", management, createConversationBody{}, agentuse.CreateConversationResponse{}, 201, s.handleCreateConversation)
	bind("GET", "/api/works/{work_id}/threads/{id}", "get-work-thread", "读取工作线程", "work", management, nil, agentuse.ConversationDetail{}, 200, s.handleGetConversation)
	bind("POST", "/api/works/{work_id}/threads/{id}/messages", "send-work-thread-message", "向工作线程发消息", "work", management, messageBody{}, agentuse.Reply{}, 201, s.handleAddMessage)
	bind("POST", "/api/works/{work_id}/threads/{id}/resolve", "resolve-work-thread", "标记工作线程完成", "work", management, nil, agentuse.Conversation{}, 200, s.handleResolveConversation)
	tool := bind("GET", "/api/agent-tools/work.py", "download-work-tool", "下载 Server 提供的 Work 工具", "work", public, nil, nil, 200, s.handleWorkTool)
	binaryResponse(tool, "text/x-python")
	authorization := bind("POST", "/api/agent-tools/work/authorize", "authorize-work-tool", "校验来源并为协调输入签发工作授权", "work", device, workToolAuthorization{}, workToolCredential{}, 200, s.handleAuthorizeWorkTool)
	authorization.Description = "Server 根据设备、conversation、message、agent 和实际 run 确认协调权限；受理前沿用已投递消息授权，受理后校验 run_id。令牌在对应输入结束或被拒绝后失效。Client 不判断业务角色。"

	bind("GET", "/api/conversations", "list-conversations", "列出会话；work_id 指定工作的线程", "conversation", work, nil, []agentuse.Conversation{}, 200, s.handleListConversations, query("work_id", "工作 ID；省略时列普通会话，工作令牌默认列本工作线程"))
	bind("POST", "/api/conversations", "create-conversation", "创建会话，可同时发送首条消息", "conversation", work, createConversationBody{}, agentuse.CreateConversationResponse{}, 201, s.handleCreateConversation)
	contextRoute := bind("GET", "/api/conversations/{id}/messages/{message_id}/agents/{agent_id}/context", "get-message-context", "为本地 session 补齐尚未提供的共享消息", "conversation", apiAccess{security: []map[string][]string{{"deviceKey": {}}}}, nil, agentuse.MessageContext{}, 200, s.handleMessageContext)
	contextRoute.Parameters = append(contextRoute.Parameters, query("session_id", "Client 实际选择的本地 session ID"))
	bind("GET", "/api/conversations/{id}", "get-conversation", "读取消息、执行、投递、事件和问题的统一快照", "conversation", conversation, nil, agentuse.ConversationDetail{}, 200, s.handleGetConversation)
	bind("DELETE", "/api/conversations/{id}", "delete-conversation", "删除无活跃执行的会话", "conversation", user, nil, nil, 204, s.handleDeleteConversation)
	bind("POST", "/api/conversations/{id}/messages", "send-message", "发送消息，明确指定 Agent 和用户收件人", "conversation", work, messageBody{}, agentuse.Reply{}, 201, s.handleAddMessage)
	bind("POST", "/api/conversations/{id}/resolve", "resolve-conversation", "标记会话已完成", "conversation", work, nil, agentuse.Conversation{}, 200, s.handleResolveConversation)
	bind("POST", "/api/conversations/{id}/reopen", "reopen-conversation", "重新打开会话", "conversation", user, nil, agentuse.Conversation{}, 200, s.handleReopenConversation)
	bind("GET", "/api/conversations/{id}/runs/{run_id}", "get-run", "读取 Client 上报的 run 及事件", "run", work, nil, agentuse.RunDetail{}, 200, s.handleGetRun)
	bind("POST", "/api/conversations/{id}/runs/{run_id}/cancel", "cancel-run", "发送取消请求，等待 Client 上报结果", "run", work, nil, agentuse.Run{}, 200, s.handleCancelRun)
	bind("POST", "/api/conversations/{id}/runs/{run_id}/requests/{request_id}/reply", "answer-run-request", "回答执行中的交互请求", "run", work, answerRunBody{}, answerRunResponse{}, 202, s.handleAnswerRun)

	runHeader := &huma.Param{Name: "X-Agentuse-Run", In: "header", Description: "设备上传必填，Server 校验本会话活动 run 的归属", Schema: &huma.Schema{Type: "string"}}
	upload := bind("POST", "/api/conversations/{id}/attachments", "upload-attachment", "上传会话附件", "attachment", attachment, nil, agentuse.Attachment{}, 201, s.handleUploadAttachment, runHeader)
	multipart(upload, map[string]*huma.Schema{"file": binary, "filename": {Type: "string", Description: "可选的显示文件名，覆盖 file 自带的文件名"}, "path": {Type: "string", Description: "conversation 内的相对文件路径；用户上传固定归入 user_input/"}})
	upload.Description += " 单文件最大 " + strconv.FormatInt(s.cfg.AttachmentMaxBytes, 10) + " 字节，会话附件总量最大 " + strconv.FormatInt(s.cfg.AttachmentConversationMaxBytes, 10) + " 字节。"
	min, max := float64(1), float64(100)
	bind("GET", "/api/conversations/{id}/attachments", "list-attachments", "分页读取会话附件", "attachment", attachment, nil, agentuse.AttachmentPage{}, 200, s.handleListAttachments, runHeader, &huma.Param{Name: "limit", In: "query", Schema: &huma.Schema{Type: "integer", Minimum: &min, Maximum: &max, Default: 100}}, query("cursor", "上一页的 next_cursor"), query("run_id", "只列出该 run 上传到本会话的输出附件"))
	bind("GET", "/api/conversations/{id}/attachments/{attachment_id}", "get-attachment", "读取附件元数据", "attachment", attachment, nil, agentuse.Attachment{}, 200, s.handleGetAttachment, runHeader)
	content := bind("GET", "/api/conversations/{id}/attachments/{attachment_id}/content", "download-attachment", "下载附件内容", "attachment", attachment, nil, nil, 200, s.handleAttachmentContent, runHeader)
	binaryResponse(content, "*/*")
	bind("DELETE", "/api/conversations/{id}/attachments/{attachment_id}", "delete-attachment", "删除附件", "attachment", attachmentDelete, nil, nil, 204, s.handleDeleteAttachment)
	bind("POST", protocol.RegisterPath, "register-device", "用一次性安装令牌注册设备", "client", public, protocol.RegisterRequest{}, protocol.RegisterResponse{}, 200, s.handleRegister)
	bind("GET", protocol.MePath, "get-device-identity", "使用设备密钥读取设备身份", "client", device, nil, protocol.MeResponse{}, 200, s.handleDeviceMe)
	ws := bind("GET", protocol.WSPath, "connect-client", "升级为 Client 双向 WebSocket 连接", "client", device, nil, nil, 101, s.cfg.ClientHandler.ServeHTTP, &huma.Param{Name: protocol.ProtocolHeader, In: "header", Required: true, Schema: &huma.Schema{Type: "string", Enum: []any{protocol.ExecutionProtocol}}})
	ws.Description = "HTTP 只负责握手；连接后使用 agentuse-client/2 的 heartbeat、message、event、answer、cancel、ack 报文。完整报文和恢复规则见仓库 designs/v3/protocol.md。"
	ws.Extensions = map[string]any{"x-websocket-protocol": protocol.ExecutionProtocol}
	ws.Responses = map[string]*huma.Response{"101": {Description: "Switching Protocols"}, "503": jsonResponse(503, apiErrorBody{})}
	for _, code := range []int{400, 401, 403, 426} {
		ws.Responses[strconv.Itoa(code)] = &huma.Response{Description: http.StatusText(code), Content: map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: "string"}}}}
	}
}
