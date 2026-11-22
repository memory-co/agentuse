package controller

import "encoding/json"

// Request types are shared by HTTP decoding and Huma schema generation.
type loginBody struct {
	Token string `json:"token" doc:"用户登录令牌，或安装时设置的管理员登录密码"`
}

type createDeviceBody struct {
	Name string `json:"name,omitempty" doc:"设备名称，省略时注册后使用主机名"`
}

type toggleHarnessBody struct {
	Enabled bool `json:"enabled" doc:"是否允许向该设备的 harness 投递消息"`
}

type renameConversationBody struct {
	Title string `json:"title" doc:"新的会话名称，去除首尾空白后不能为空"`
}

type answerRunBody struct {
	Answer json.RawMessage `json:"answer" doc:"harnessctl request 对应的原生 JSON 答案"`
}

type answerRunResponse struct {
	State string `json:"state" enum:"pending" doc:"已入队，等待 Client 确认"`
}

type apiErrorBody struct {
	Error string `json:"error"`
}
