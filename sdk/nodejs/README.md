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
await agentuse.setDaemonAgent(uid, d.id, "claude", true);

// 发起任务，等它回完
const { session } = await agentuse.createSession(uid, { agent: "claude", prompt: "修一下 README 的错别字", cwd: "~/code/x" });
const detail = await agentuse.waitSession(uid, session.id, { interval: 2000, timeout: 600_000 });
console.log(detail.messages.at(-1).content);

// 接着回复
try {
  await agentuse.sendMessage(uid, session.id, { prompt: "再把 CHANGELOG 也改一下" });
} catch (e) {
  if (e instanceof AgentuseError && e.status === 409) {
    // agent 还没回完
  }
}
```

返回值都是接口里的 JSON 对象，字段见 [apis](../../docs/v2/apis/README.md) 和 `index.d.ts`。需要自定义请求时可以传 `fetch`。
