# 工作(work)

工作 = 一个**协调者会话** + 若干**线程会话** + 一份共用**说明**，照 Claude Projects 的样子(就像这里：项目聊天里有一个协调者，具体的活各开一个线程)。比如「重构 daemon」：用户只跟协调者说要做什么，协调者开几个线程分别改代码、写测试、写文档，线程做完结果回到协调者，协调者再汇总给用户。

表见 [tables/works](../tables/works.md),接口见 [apis/works](../apis/works.md)。

## 三种会话

| | 怎么区分 | 在哪显示 |
|---|---|---|
| 普通会话 | `work_id` 为空 | 侧边栏「会话」tab |
| 协调者 | `works.coordinator_id` 指向它(`work_id` 也是这个工作) | 工作页的主对话 |
| 线程 | `work_id` 有值，又不是协调者 | 「工作」tab 里该工作下面、工作页的线程列表 |

- 协调者用 `works.coordinator_id` 认，不在 sessions 上加类型字段：一个工作正好一个协调者，放在 works 上天然唯一。接口里的 session 对象多一个派生字段 `work_role`:`coordinator` / `thread`,不在工作里时不出现。
- **进了工作的会话不在「会话」tab 里显示**,只在工作下面看。「会话」tab 只列 `work_id` 为空的。

## 规则

- **工作是个人的**:属于建它的用户，只有主人(和 admin)能看、改、删。
- **建工作时选一个 agent 给协调者**:同时建出协调者会话(`idle`,还没有消息)。之后用户在工作页发的话就是发给协调者的消息，接口和普通会话一样。
- **线程谁都能开**:协调者通过 `agentuse work` 命令开(见下);用户也可以在工作页手动开。线程可以用不同的 agent、跑在不同 daemon 上。
- **`work_id` 建了就定死**:会话建的时候属于哪个工作就是哪个，之后不能移进、移出、换工作;普通会话也不能后来放进工作。协调者不能单独删。
- **删除工作**:连协调者和所有线程一起删(和 Claude Projects 一样);有会话在跑时返回 409。
- **文件(知识库)**:Claude Projects 还能挂文件，先不做。

## 协调者怎么开线程、怎么收结果

**开线程 / 发消息 / 看结果**:协调者跑的时候,daemon 给 harness 进程加环境变量:

| 变量 | 值 |
|---|---|
| `AGENTUSE_SERVER` | 服务地址 |
| `AGENTUSE_WORK_TOKEN` | `wt_…`,服务在派活消息里给，只在这条消息跑着时有效，只能动这个工作的线程 |

协调者用本机的 `agentuse work` 命令(读这两个变量调接口,[cli](../cli/agentuse.md#work)):

```
agentuse work agents                         # 能用哪些 agent
agentuse work threads                        # 线程列表和状态
agentuse work new --agent code-helper "改 daemon 的重连逻辑"   # 开线程，打印线程 id
agentuse work send ss_xxx "再补个测试"        # 给线程发消息(在跑就是插话)
agentuse work read ss_xxx                    # 看线程的消息
```

协调者的会话目录里多拷一个 `agentuse-work` skill,教它这些命令和「开完线程就结束这一轮，等结果回来」。

**结果回到协调者**:线程每回完一条(`done` / `failed`),服务给协调者追加一条消息，内容是:

```json
{ "prompt": "[线程 ss_xxx「改重连逻辑」回复了]\n…回复正文，最长 4000 字…", "from_session": "ss_xxx" }
```

协调者 `idle` 时它就是下一条消息，会叫醒协调者;协调者在跑时作为[插话](steer.md)送进去。页面上 `from_session` 的消息不显示成用户气泡，显示成一张「线程 xxx 回复了」的小卡片，点了进线程。

## 说明怎么交给 harness

派活时，会话在工作里(协调者和线程都算),服务就把工作的 `instructions` 放进 daemon 收到的 `message` 里([apis/daemon](../apis/daemon.md)),没有就不带。daemon 每次起进程前在会话目录里写好:

| harness | 写到哪 |
|---|---|
| `claude` | `~/.agentuse/work/<session_id>/CLAUDE.md` |
| `codex` | `~/.agentuse/work/<session_id>/AGENTS.md` |
| 自定义 | 不写文件,stdin 里多一个 `"instructions": "…"` 字段 |

- 写哪个文件由 `harness/<name>/harness.yaml` 的 `instructions: CLAUDE.md` 一项指定([harness](harness.md))。
- **每条消息都重写**:说明改了，下一条就用新的;没有说明就删掉这个文件。这个文件归 agentuse 管,harness 自己改了也会被覆盖。

## 网页

侧边栏原来的会话列表上方加两个 tab:**会话** / **工作**(英文 Sessions / Works),选中哪个记在 localStorage。

- **会话** tab:只列普通会话(`work_id` 为空)。
- **工作** tab:列出我的工作，按 `updated_at` 新的在前，每个工作可以展开，下面列它的线程(带状态点)。顶部一个「+ 新建工作」;一个都没有时显示虚线框空状态(「让协调者帮你拆活、开线程」+「新建工作」按钮)。点工作名进 `/works/:id`,点线程进那个会话。
- **新建工作**:弹框填名字(必填)、选协调者的 agent(必填)、说明(可空),建好进工作页。
- **工作页**(右侧主区)就是和协调者的对话，跟会话页一样(消息、进度、底部输入框);线程回来的消息显示成小卡片。顶部：工作名(点了能改)、「说明」按钮(弹框编辑)、「线程」按钮(右侧抽屉列出线程，可以手动开一个)、菜单里删除工作。
- **线程页**:就是会话页，顶部标题前面显示「工作名 /」,点了回工作页。
- 手机：同样的 tab 放在侧边栏那一屏里。
