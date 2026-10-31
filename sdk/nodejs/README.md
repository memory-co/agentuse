# agentuse(Node.js)

agentuse 的 Node.js 客户端，用 Node 18+ 自带的 `fetch`,没有依赖，带 TypeScript 类型。总体说明见 [sdk](../README.md)。

```bash
# 还没发到 npm,先从仓库里装(或者直接把 index.js / index.d.ts 拷进项目)
npm install /path/to/agentuse/sdk/nodejs
```

```js
import { Client, AgentuseError } from "agentuse";

const agentuse = new Client({ baseUrl: "http://agentuse.internal:8080", token: process.env.AGENTUSE_TOKEN }); // admin 令牌，全局一个

const uid = "u_10086"; // 网站自己的 user id,每次调用直接传

// 连接我的电脑：把命令给用户复制
const d = await agentuse.createDaemon(uid, "my-laptop");
console.log(d.setup_command);

// 电脑上线后打开 claude
await agentuse.setDaemonHarness(uid, d.id, "claude", true);

// 建一个 agent,运行时是这台电脑上的 claude;以后 updateAgent 切换运行时,agent 不变
const agent = await agentuse.createAgent(uid, { id: "code-helper", name: "写代码", daemon_id: d.id, harness: "claude" });

// 用这个 agent 发起任务，等它回完
const { session } = await agentuse.createSession(uid, { target_agent_ids: [agent.id], prompt: "修一下 README 的错别字" });
const detail = await agentuse.waitSession(uid, session.id, { interval: 2000, timeout: 600_000 });
console.log(detail.messages.at(-1).content);

// harness 向用户提问时会话是 waiting,waitSession 也会返回;回答后它在同一轮里接着做
if (detail.session.status === "waiting") {
  const q = detail.questions.at(-1);
  await agentuse.answerQuestion(uid, session.id, q.id, { [q.questions[0].question]: q.questions[0].options[0].label });
}

// 接着回复;harness 还在跑时是插话(返回 steer),会在它下一步之前送达
const r = await agentuse.sendMessage(uid, session.id, { prompt: "再把 CHANGELOG 也改一下" });
if ("status" in r) console.log("插话:", r.status);
```

返回值都是接口里的 JSON 对象，字段见 [apis](../../docs/v2/apis/README.md) 和 `index.d.ts`。需要自定义请求时可以传 `fetch`。

## 附件

```js
import { createReadStream } from 'node:fs';
const { session } = await c.createSession(user, { defer_start: true });
const file = await c.uploadAttachment(user, session.id, 'input.csv', createReadStream('./input.csv'));
await c.sendMessage(user, session.id, { prompt: '分析附件' });
const response = await c.downloadAttachment(user, session.id, file.id);
// response 是 fetch Response，使用 response.body 流式读取，完成后关闭或取消流。
```

还支持 `listAttachments(user, sessionId, {limit, cursor})`、`getAttachment`、`deleteAttachment`；列表返回 `{attachments, next_cursor}`。上传接受异步字节流、ReadableStream 或 Blob，不预读整个文件。完整约定见 [attachments API](../../docs/v2/apis/attachments.md)。

## 多 agent

```js
const {session} = await c.createSession(user, {mode: 'multi_agent'});
const reply = await c.sendMessage(user, session.id, {
  prompt: '分别看看实现与风险', target_agent_ids: ['coder', 'reviewer'],
}, {idempotencyKey: 'turn-1'});
const detail = await c.waitMessageRuns(user, session.id, reply.message.id, {interval: 500, timeout: 600000});
```

支持 getRun、waitRun、cancelRun、answerRunQuestion。reply.deliveries 表示自动路由到新执行或插话的状态；新模式 waitSession 默认等待全部终态，可设置 returnOnWaiting。
