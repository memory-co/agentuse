package controller

import "encoding/json"

// Request types are shared by HTTP decoding and Huma schema generation.
type loginBody struct {
	Token string `json:"token" doc:"用户登录令牌，ut_ 开头"`
}

type createDeviceBody struct {
	Name string `json:"name,omitempty" doc:"设备名称，省略时注册后使用主机名"`
}

type toggleHarnessBody struct {
	Enabled bool `json:"enabled" doc:"是否允许向该设备的 harness 投递消息"`
}

type createConversationBody struct {
	Mode                 string   `json:"mode,omitempty" enum:"single,collaborative" doc:"single（默认）自己执行；collaborative 协调子对话。子对话只能为 single"`
	Title                string   `json:"title,omitempty"`
	ToAgentIDs           []string `json:"to_agent_ids,omitempty" doc:"处理首条消息的 Agent ID；省略为空数组，重复 ID 去重"`
	ToUserIDs            []string `json:"to_user_ids,omitempty" doc:"已有会话读取权限的用户 ID；不触发执行"`
	ParentConversationID string   `json:"parent_conversation_id,omitempty" doc:"仅已校验的顶层 Agent 可创建执行子对话；parent_agent_id 由来源自动确定"`
	Prompt               string   `json:"prompt,omitempty" doc:"首条消息文本；省略时创建空会话"`
}

type messageBody struct {
	AttachmentIDs    []string `json:"attachment_ids,omitempty" maxItems:"64" doc:"本条消息选定的同会话附件 ID；省略或空数组表示没有附件"`
	ReplyToMessageID string   `json:"reply_to_message_id,omitempty" doc:"回复同一 conversation 中已有的消息"`
	ToAgentIDs       []string `json:"to_agent_ids,omitempty" doc:"处理本条消息的 Agent ID；仅这些收件人创建执行关系"`
	ToUserIDs        []string `json:"to_user_ids,omitempty" doc:"已有会话读取权限的用户 ID；不改变会话可见性"`
	Prompt           string   `json:"prompt,omitempty" doc:"消息文本；两组收件人均省略时只保存备注"`
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
