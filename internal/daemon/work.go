package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/memory-co/agentuse/internal/protocol"
)

// coordinatorGuide 放在协调者说明文件的最前面，教它只派活、用 agentuse work 开线程。见 docs/v2/designs/work.md。
const coordinatorGuide = `# 你是这个工作的协调者：只派活，不干活

用户在这里跟你说要做什么。**你自己不做任何具体的活**:不查资料、不跑诊断、不改文件、不写代码，也不要用你自带的子 agent / 子任务。
不管活多小(哪怕「检查一下网络」),都用 agentuse work new 开**线程**交出去。线程是独立的会话，可以用用户名下的任何 agent,不限于这台机器(agent 在哪台机器上，线程就跑在哪台上)。

你只用本机的 agentuse 命令(环境变量已经设好，直接用):

    agentuse work agents                              # 能用哪些 agent(先看这个，按 agent 的说明挑)
    agentuse work threads                             # 这个工作的线程和状态
    agentuse work new --agent <agent_id> "要做的事"     # 开线程，打印线程 id
    agentuse work send <线程 id> "补充的话"             # 给线程发消息(它在跑就是插话)
    agentuse work read <线程 id>                       # 看线程的消息
    agentuse work resolve <线程 id>                    # 线程的活做完了，标记已完成

- 开线程时把背景、目标、交付物写清楚，线程看不到你和用户的对话(但能看到这份文件里「工作说明」那部分)。
- 开完线程就结束这一轮，简短告诉用户开了哪些线程、各交给了哪个 agent。不要等、不要轮询。
- 线程每回完一条，结果会作为一条以「[线程 … 回复了]」开头的消息发给你。收到后决定：接着给它发消息、标记完成、再开线程、或者汇总给用户。
- 你可以直接回答的只有：闲聊、澄清需求、汇报线程的进展和结果。
- agentuse work 命令报错(连不上、令牌无效等)时，把错误原样告诉用户，然后结束这一轮。**不要绕过去自己干**。
`

// instructionsFor 是这条消息要写进说明文件的内容：协调者是 coordinatorGuide 在前，后面接工作的说明;线程只有工作的说明。
func instructionsFor(m protocol.Dispatch) string {
	text := strings.TrimSpace(m.Instructions)
	if m.WorkToken == "" {
		return text
	}
	if text == "" {
		return coordinatorGuide
	}
	return coordinatorGuide + "\n# 工作说明\n\n" + text
}

// writeInstructions 把说明写进会话目录的 name 文件;没有说明就删掉它(说明清空后不留旧的)。
func writeInstructions(dir, name, text string) error {
	if name == "" {
		return nil
	}
	p := filepath.Join(dir, name)
	if text == "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return os.WriteFile(p, []byte(text+"\n"), 0o644)
}

// workEnv 是协调者进程要多带的环境变量：服务地址、工作令牌，再把本程序所在目录放到 PATH 最前面，
// 让它找得到 agentuse 命令。不是协调者时返回 nil。
// 令牌的变量名里不带 TOKEN/KEY/SECRET:codex 默认会把这类变量从它跑的命令里过滤掉。
func workEnv(server string, m protocol.Dispatch) []string {
	if m.WorkToken == "" {
		return nil
	}
	env := []string{"AGENTUSE_SERVER=" + server, "AGENTUSE_WORK=" + m.WorkToken}
	if exe, err := os.Executable(); err == nil {
		env = append(env, "PATH="+filepath.Dir(exe)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return env
}
