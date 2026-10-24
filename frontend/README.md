# agentuse 网页

技术栈和 memory.talk 的前端一致:React 18、TypeScript、Vite、Tailwind CSS、TanStack Query,UI 统一用 shadcn/ui(New York / Radix)、Lucide、Sonner。构建产物 `dist/` 由 `embed.go` 嵌进 agentuse-server,在 `/` 托管。

## 页面

一个壳加四个 tab,当前 tab 放在 hash 里(`#/sessions`),刷新不丢。

| tab | 功能 |
| --- | --- |
| 机器 | 添加 daemon、复制安装命令(setup.sh)、看在线状态、给每台 daemon 开关 harness、重新生成安装命令、删除 |
| 会话 | 选 harness 开会话，看消息和进度，空闲时接着回复 |
| Harness | 全部 harness,注册 / 删除自定义 harness |
| 用户 | (仅 admin)建用户，拿登录链接 |

进门先问 `GET /api/me`:401 显示登录页(输 `ut_` 令牌，`POST /api/login` 种 cookie),否则进壳。任何请求 401 都回登录页。列表每 3 秒轮询。接口见 [docs/v2/apis](../docs/v2/apis/README.md)。

## 开发与构建

先在仓库根启动服务，再起前端开发服务器:

```bash
make build && bin/agentuse-server start
cd frontend
npm ci
npm run dev
```

Vite 把 `/api`、`/login`、`/setup.sh`、`/download` 代理到 `http://127.0.0.1:8080`,别的地址用 `AGENTUSE_API=http://127.0.0.1:8123 npm run dev`。

生产构建 `npm run build`(仓库根 `make web` / `make build` 会自动跑):先 TypeScript 检查，再生成 `dist/`,然后要重新编译 agentuse-server 才会嵌进去。`dist/` 只提交了 `.gitkeep`;没构建网页时服务照常跑 API,`/` 返回一句提示。

## 代码结构

```text
embed.go            # Go 包 frontend:把 dist/ 嵌进 agentuse-server
src/
├── main.tsx        # 入口：主题、QueryClient、Toaster
├── App.tsx         # 登录门 + 壳(tab)
├── pages/          # Login / Daemons / Sessions / Harnesses / Users
├── components/     # Shared(状态徽章、复制块、确认框…)
│   └── ui/         # shadcn/ui 组件，见 ui/README.md
└── lib/            # api、query、types、utils
```
