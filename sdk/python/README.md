# agentuse(Python)

agentuse 的 Python 客户端，只用标准库。总体说明见 [sdk](../README.md)。

```bash
pip install "git+https://github.com/memory-co/agentuse#subdirectory=sdk/python"
```

```python
import os
from agentuse import Client, AgentuseError

agentuse = Client("http://agentuse.internal:8080", token=os.environ["AGENTUSE_TOKEN"])  # admin 令牌，全局一个

uid = "u_10086"  # 网站自己的 user id,每次调用直接传

# 连接我的电脑：把命令给用户复制
d = agentuse.create_device(uid, "my-laptop")
print(d["setup_command"])

# 电脑上线后打开 claude
agentuse.set_device_harness(uid, d["id"], "claude", True)

# 建一个 agent,运行时是这台电脑上的 claude;以后 publish_agent_version 发布完整新配置，agent 身份不变
a = agentuse.create_agent(uid, "写代码", d["id"], "claude", id="code-helper")

# 用这个 agent 发起任务，等它回完
s = agentuse.create_conversation(uid, to_agent_ids=[a["id"]], prompt="修一下 README 的错别字")["conversation"]
detail = agentuse.wait_conversation(uid, s["id"], interval=2, timeout=600)
print(detail["messages"][-1]["content"])

# 接着回复；适配器 accepted 决定加入当前 run 或产生新 run
r = agentuse.send_message(uid, s["id"], prompt="再把 CHANGELOG 也改一下", to_agent_ids=[a["id"]])
print(r["deliveries"])  # accepted 前 runs 可以为空
```

返回值都是接口里的 JSON 对象(dict / list),字段见 [apis](../../docs/server/conversations.md)。`timeout` 是单次 HTTP 请求的超时(秒),建客户端时给。

## 附件

```python
created = c.create_conversation(user, )
conversation_id = created['conversation']['id']
with open('input.csv', 'rb') as stream:
    file = c.upload_attachment(user, conversation_id, 'input.csv', stream)
c.send_message(user, conversation_id, prompt='分析附件')
with c.download_attachment(user, conversation_id, file['id']) as response:
    with open('download.csv', 'wb') as output:
        import shutil
        shutil.copyfileobj(response, output)
```

还支持 `list_attachments(user, conversation_id, limit=None, cursor=None)`、`get_attachment`、`delete_attachment`。列表返回 `attachments` 和 `next_cursor`；下载返回需要关闭的响应流。上传使用二进制文件对象，按块发送；导出 `Attachment`、`AttachmentPage` TypedDict 类型。完整约定见 [attachments API](../../docs/server/conversations.md)。

## 多 agent

```python
s = c.create_conversation(user)['conversation']
r = c.send_message(user, s['id'], prompt='分别看看实现与风险',
                   to_agent_ids=['coder', 'reviewer'])
d = c.wait_message_runs(user, s['id'], r['message']['id'], interval=.5, timeout=600)
```

支持 get_run、wait_run、cancel_run、reply_run_request。响应 deliveries 包含各目标输入分配；accepted 回报实际 run；新模式 wait_conversation 默认等全部终态，return_on_waiting=True 可在提问时返回。

执行状态以 client 的公共事件为准：running/waiting/completed/failed/cancelled。发送消息先返回输入分配，不预创建 run；断线、取消入队不代表执行终态。harness 列表来自设备上报，仅供读取。

消息使用 `to_agent_ids` 和 `to_user_ids`，可同时指定；返回值始终包含这两组数组。只接受新字段，不保留旧字段别名。

```python
# 只发给已有会话读取权限的用户，不启动 Agent。
agentuse.send_message(uid, s["id"], prompt="请查看结果", to_user_ids=[uid])
```

逐条消息通过 `attachment_ids` 选择本次文件；未提供则没有附件。Message 返回 `attachments` 元数据快照，Attachment 的 `relative_path` 是当前 conversation 内的路径，用户上传归入 `user_input/`。文件树与接口见 [对话文件](../../docs/server/conversation-files.md)。

## 模式与子对话

创建时 mode 为 single（默认）或 collaborative，创建后不可改。两种模式均支持多个 Agent。子对话由协作 Agent 通过 context conversation create 创建，固定 single；parent_agent_id 由实际来源决定，人类请求不能代填父对话创建。

```python
r = c.create_conversation(uid, mode="collaborative", prompt="拆分任务并汇总", to_agent_ids=["planner"])
children = c.list_children(uid, r["conversation"]["id"])
c.delete_conversation_tree(uid, r["conversation"]["id"])
```

设备工具使用 Source/source 携带实际 conversation_id、agent_id、session_id、run_id，由 Server 验证；不另申请工作令牌。向父反馈和最终回复的回传规则见 [Conversation 文档](../../docs/server/conversations.md#模式与嵌套)。
