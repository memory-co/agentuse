package run

import (
	"github.com/memory-co/agentuse/internal/protocol"
	"os"
	"path/filepath"
	"strconv"
)

// workEnv 给所有 harness 设置服务地址、当前会话/消息和 agentuse 的 PATH；协调者再带工作令牌。
// 令牌的变量名里不带 TOKEN/KEY/SECRET:codex 默认会把这类变量从它跑的命令里过滤掉。
func workEnv(server string, m protocol.Dispatch) []string {
	env := []string{"AGENTUSE_SERVER=" + server, "AGENTUSE_SESSION=" + m.SessionID, "AGENTUSE_MESSAGE_ID=" + strconv.FormatInt(m.ID, 10)}
	if m.RunID != "" {
		env = append(env, "AGENTUSE_RUN="+m.RunID, "AGENTUSE_ATTEMPT="+m.AttemptID, "AGENTUSE_AGENT="+m.AgentID)
	}
	if m.WorkToken != "" {
		env = append(env, "AGENTUSE_WORK="+m.WorkToken)
	}
	if exe, err := os.Executable(); err == nil {
		env = append(env, "PATH="+filepath.Dir(exe)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return env
}
