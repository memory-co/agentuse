# agentuse(Python)

agentuse 的 Python 客户端，只用标准库。总体说明见 [sdk](../README.md)。

```bash
pip install "git+https://github.com/memory-co/agentuse#subdirectory=sdk/python"
```

```python
from agentuse import Client, AgentuseError

agentuse = Client("http://agentuse.internal:8080", token=os.environ["AGENTUSE_TOKEN"])  # admin 令牌，全局一个

uid = "u_10086"  # 网站自己的 user id,每次调用直接传

# 连接我的电脑：把命令给用户复制
d = agentuse.create_daemon(uid, "my-laptop")
print(d["setup_command"])

# 电脑上线后打开 claude
agentuse.set_daemon_harness(uid, d["id"], "claude", True)

# 建一个 agent,运行时是这台电脑上的 claude;以后 update_agent 切换运行时,agent 不变
a = agentuse.create_agent(uid, "写代码", d["id"], "claude", id="code-helper")

# 用这个 agent 发起任务，等它回完
s = agentuse.create_session(uid, a["id"], prompt="修一下 README 的错别字")["session"]
detail = agentuse.wait_session(uid, s["id"], interval=2, timeout=600)
print(detail["messages"][-1]["content"])

# 接着回复;harness 还在跑时是插话(返回 steer),会在它下一步之前送达
r = agentuse.send_message(uid, s["id"], prompt="再把 CHANGELOG 也改一下")
if "status" in r:
    print("插话:", r["status"])
```

返回值都是接口里的 JSON 对象(dict / list),字段见 [apis](../../docs/v2/apis/README.md)。`timeout` 是单次 HTTP 请求的超时(秒),建客户端时给。
