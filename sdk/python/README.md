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
d = agentuse.create_daemon(uid, "my-laptop")
print(d["setup_command"])

# 电脑上线后打开 claude
agentuse.set_daemon_harness(uid, d["id"], "claude", True)

# 建一个 agent,运行时是这台电脑上的 claude;以后 publish_agent_version 发布完整新配置，agent 身份不变
a = agentuse.create_agent(uid, "写代码", d["id"], "claude", id="code-helper")

# 用这个 agent 发起任务，等它回完
s = agentuse.create_session(uid, target_agent_ids=[a["id"]], prompt="修一下 README 的错别字")["session"]
detail = agentuse.wait_session(uid, s["id"], interval=2, timeout=600)
print(detail["messages"][-1]["content"])

# harness 向用户提问时会话是 waiting,wait_session 也会返回;回答后它在同一轮里接着做
if detail["session"]["status"] == "waiting":
    request = detail["questions"][-1]
    q = request["payload"]["questions"][0]
    agentuse.reply_run_request(uid, s["id"], request["run_id"], request["request_id"], {q["id"]: q["options"][0]["label"]})

# 接着回复；适配器 accepted 决定加入当前 run 或产生新 run
r = agentuse.send_message(uid, s["id"], prompt="再把 CHANGELOG 也改一下")
print(r["deliveries"])  # accepted 前 runs 可以为空
```

返回值都是接口里的 JSON 对象(dict / list),字段见 [apis](../../docs/v2/apis/README.md)。`timeout` 是单次 HTTP 请求的超时(秒),建客户端时给。

## 附件

```python
created = c.create_session(user, defer_start=True)
session_id = created['session']['id']
with open('input.csv', 'rb') as stream:
    file = c.upload_attachment(user, session_id, 'input.csv', stream)
c.send_message(user, session_id, prompt='分析附件')
with c.download_attachment(user, session_id, file['id']) as response:
    with open('download.csv', 'wb') as output:
        import shutil
        shutil.copyfileobj(response, output)
```

还支持 `list_attachments(user, session_id, limit=None, cursor=None)`、`get_attachment`、`delete_attachment`。列表返回 `attachments` 和 `next_cursor`；下载返回需要关闭的响应流。上传使用二进制文件对象，按块发送；导出 `Attachment`、`AttachmentPage` TypedDict 类型。完整约定见 [attachments API](../../docs/v2/apis/attachments.md)。

## 多 agent

```python
s = c.create_session(user, mode='multi_agent')['session']
r = c.send_message(user, s['id'], prompt='分别看看实现与风险',
                   target_agent_ids=['coder', 'reviewer'], idempotency_key='turn-1')
d = c.wait_message_runs(user, s['id'], r['message']['id'], interval=.5, timeout=600)
```

支持 get_run、wait_run、cancel_run、reply_run_request。响应 deliveries 包含各目标输入分配；accepted 回报实际 run；新模式 wait_session 默认等全部终态，return_on_waiting=True 可在提问时返回。

执行状态以 client 的公共事件为准：running/waiting/completed/failed/cancelled。发送消息先返回输入分配，不预创建 run；断线、取消入队不代表执行终态。harness 列表来自设备上报，仅供读取。
