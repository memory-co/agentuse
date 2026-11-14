# agentuse 网页

React、TypeScript、Vite、Tailwind CSS、TanStack Query，UI 使用 shadcn/ui、Radix、Lucide、Sonner。构建产物 `dist/` 由 `embed.go` 嵌进 agentuse-server，在 `/` 托管。

## 页面

左边是导航和 conversation / Work 列表，中间是对话，右边是当前 conversation 的文件树和预览。文件树支持多级路径，手机端使用抽屉。路由在 hash 里：`#/` 创建对话、`#/conversations/<id>` 对话、`#/works/new` 创建 Work、`#/w/<id>` Work，以及 `#/agents` 等管理页。

- **导航**：普通用户可管理 Agents、Skills、Devices；管理员还可查看设备上报的 Harness 和管理 Users。
- **列表**：conversation 与 Work 分开显示；管理员可通过身份选择器代用户操作，请求带 `X-Agentuse-User`，见 [用户身份](../../designs/v3/server-user.md)。
- **对话**：发送时明确选择 Agent / 用户收件人和本次附件；消息显示各自附件。用户上传进入 `user_input/`，AI 输出保留自己的相对路径。Agent 回答使用 Markdown，原生事件和控制在 run 详情中查看。

≥1024 桌面侧栏可展开或收为图标栏；768–1023 默认图标栏；<768 手机以整屏导航和主内容横向切换。输入框 Enter 发送，Shift+Enter 换行，输入法选词时的 Enter 不发送。

中英文切换在侧栏底部(登录页右上角也有),记在 localStorage,没选过跟浏览器语言;文案都在 `src/lib/i18n.ts`。中文里 device 叫「连接设备」,harness 叫「执行器」。服务端返回的错误信息不翻译。

| 页面 | 功能 |
| --- | --- |
| Agents | 创建、发布不可变配置版本、选择固定 Skill 版本、查看和复制历史版本、软删除 |
| Skills | 发布 ZIP 版本、查看历史版本、下载和软删除 |
| Devices | 添加 device、复制安装命令(setup.sh)、看在线状态、给每台 device 开关 harness、重新生成安装命令、删除 |
| Harness | (仅 admin)查看设备上报的可用 harness 标识；接入包由 Client 管理 |
| Users | (仅 admin)建用户，拿登录链接 |

启动先查询 `GET /api/me`；401 显示登录页，通过 `POST /api/login` 写入用户 cookie。浏览器 WebSocket 只接收资源变更通知，随后使相关 HTTP 查询失效；重连和轮询补齐状态。API 见 [conversation 接口](../../docs/server/conversations.md)，通知约定见 [浏览器设计](../../designs/v3/server-browser.md)。

## 开发与构建

先在仓库根启动服务，再起前端开发服务器:

```bash
make build && bin/agentuse-server start
cd server/frontend
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
├── App.tsx         # 登录门 + 壳(侧栏 + 主区域)
├── pages/          # Login / Conversations / Works / Agents / Skills / Devices / Harnesses / Users
├── components/     # Shared(状态徽章、复制块、确认框…)
│   └── ui/         # shadcn/ui 组件，见 ui/README.md
├── hooks/          # use-mobile 等(sidebar 用)
└── lib/            # api、actas(管理员代为用户)、i18n(中英文案)、query、types、utils
```
