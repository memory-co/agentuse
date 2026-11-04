# agentService：不可变 Agent 版本与 Skill 版本关联

> 版本化链路已实现，使用数据库 schema 3 和 harnessctl-events-v2。表之间不声明数据库外键。当前 HTTP/DTO 仍使用 daemon_id 表示 device；agent_versions 的物理列为 device_id。

## 职责与对象关系

agentService 只管理 agent 身份、不可变配置版本及其固定的 skill 版本引用。共享技能由独立的 [skillService](server-skill.md) 管理；agentService 不接管技能包上传、发布、下载或存储清理。

| 对象 | 含义 |
|---|---|
| agent | 稳定的角色 ID、所有者和当前版本指针，供消息 @ 选择 |
| agent_version | 一份不可变的完整配置：名称、说明、device、harness、model、指令与固定的 skill 版本列表 |
| agent_version_skills | 某一 agent 版本具体使用哪些 skill 版本，及其顺序 |

**Agent 和 Skill 各自版本化，通过具体版本关联。发布新 skill 版本不会改变任何已有 agent 版本。** Agent 不拥有独立的 server 对话历史；原生上下文仍由 client session 承载。

![Agent、Skill 版本和初始化](../../images/agent-versions.svg)

## Agent 的版本规则

创建 agent 时同时创建 v1。后续每次保存有效配置变更，都新增一份完整 agent_version，并在同一 SQLite 事务中推进 agents.current_version_id；不 UPDATE 已发布版本。名称、说明、设备、harness、模型、指令及 skill 集合/顺序都属于版本内容。完全相同的配置不新增版本。

更新请求带 base_version_id，必须等于当前版本；并发编辑冲突返回 409，避免后保存的内容覆盖别人刚发布的配置。它是版本冲突检查，不是消息发送的幂等键。版本号在事务中递增；UUID 用于稳定引用，数字 version 用于显示 v1、v2 等。

每份配置必须引用具体 skill_version_id，不能保存 latest。回退也复制旧配置形成新的 vN+1，不修改旧版本、不把当前版本指针退回去。已发布版本始终可用于解释历史任务。

agent 的用户归属和稳定 ID 不变；删除是身份的软删除，停止未来选择和输入分配，不修改历史版本。已经固定版本的排队输入和实际执行继续按原版本处理，取消须走显式控制链路。被历史引用的版本和 skill 包不因软删除而移除。

## 与 skillService 的关联边界

agentService 拥有 agents、agent_versions、agent_version_skills；skillService 拥有 skills、skill_versions 和技能包生命周期。关联方向是 **agent_version → skill_version**，不是两个实体的 current_version 相互跟随。

发布 agent 版本时，在同一发布事务内调用 skillService 的版本校验/读取接口，确认每项 skill_version_id 存在、属于声明的 skill_id、用户可访问且允许用于新配置。skillService 返回不可变版本描述；agentService 检查组合后没有同名技能，并保存有序的关联行。skillService 不另起提交事务，也不反向修改 agent 配置。

同一个 agent_version 对同一个 skill 只能选择一个版本。不同 skill 的原生名称不能冲突，名称比较规则与 harnessctl init 一致。新增、移除、升级、降级或调整技能顺序都产生新的 agent_version；已有版本引用不变。同一 skill_version 可以被多个 agent_version 共享。

例如：reviewer v2 与 coder v5 都引用 review skill v3。skillService 发布 review v4 后，这两个 agent 版本仍使用 v3；用户给 reviewer 升级依赖，agentService 发布 reviewer v3，才能引用 review v4。

Skill 包的格式、上传 API、权限和表结构统一见 [server-skill.md](server-skill.md)。文件统一通过 fileService 的 skills 命名空间存储，agentService 不为自己再创建一套技能包目录。

## 从输入分配到 harnessctl init

1. 用户消息 @ agent，conversationService/runService 在保存消息和输入分配的同一事务内，读取并固定该 agent 的当前 agent_version_id。
2. 已入队输入不跟随之后的 agent 更新。领取时按固定版本读取执行选择、指令和 skill 依赖，组织完整 dispatch 快照。
3. Prompt 携带 agent_version_id、model、agent_instructions 和具体 skill 版本的下载清单。Server 不发送原生命令、argv 或 YAML。
4. Client 按 endpoint/conversation/agent/version/适配器 revision 选择 session。同版本可复用；新版本必须创建新的 session 目录和 events.jsonl，不能对旧事件地址重配 init。
5. Client 下载清单中的 ZIP，校验版本、大小与 SHA-256，在 workspace 之外解包并写指令文件，调用 harnessctl init --cwd ... --model ... --agents-md ... --skills-dir ...。未指定模型时省略 --model。
6. init 成功后提交本次输入；后续同版本输入走 send，是否插话由适配器决定。accepted 决定实际 run UUID，server 再将 run 关联到该输入固定的 agent 版本。

“每次变更做一次 init”是**下一次使用该新版本时建立新的初始化绑定**，不是每编辑一次就向所有 client 推配置。旧版本 run 可继续完成；新版本消息不进入旧版本的原生上下文。两者仍使用同一 conversation 的 workspace 和共享消息历史。

Agent 指令是版本固定的 init 资源。Work 说明、协调者规则、共享历史和本次任务仍由 server 按业务输入组织到 prompt.content，不因持有 WorkToken 就由 client 推断角色，也不随每次 send 重写 agent 的指令快照。

## API 设计

| 方法 | 接口 | 行为 |
|---|---|---|
| POST | /api/agents | 创建稳定身份和完整 v1 |
| GET | /api/agents、/api/agents/{id} | 返回身份及当前版本；默认不列软删除身份 |
| POST | /api/agents/{id}/versions | 提交完整配置和 base_version_id，创建下一版本 |
| GET | /api/agents/{id}/versions | 列出历史版本 |
| GET | /api/agents/{id}/versions/{version_id} | 读取不可变配置和固定 skill 清单 |
| DELETE | /api/agents/{id} | 软删除身份，保留版本和历史引用 |

原来的直接 PATCH agent 配置模式已移除。Owner/admin 可发布 agent 版本；引用技能的可用性由 skillService 校验，技能包的下载授权也归 skillService，见其独立设计。

## 表结构

时间为 Unix 秒，指令为 UTF-8 TEXT。关联均由 service 在事务内检查，不声明 REFERENCES。设备删除不能把历史 agent_versions.device_id 改空；该版本后续不可执行，用户需选择新设备并发布新版本。

### agents 与 agent_versions

```sql
CREATE TABLE agents (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    current_version_id TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    deleted_at INTEGER
);
CREATE INDEX agents_user ON agents(user_id, updated_at);

CREATE TABLE agent_versions (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK(version > 0),
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    device_id TEXT NOT NULL,
    harness TEXT NOT NULL,
    model TEXT,
    instructions TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    UNIQUE(agent_id, version)
);
CREATE INDEX agent_versions_agent ON agent_versions(agent_id, version);
```

model 为 NULL 时固定的是“采用原生默认配置”这一选择；需要锁定具体模型时必须显式填写 model，不把外部默认配置视为被冻结。

名称只存在版本配置中，读取当前名称通过 agents.current_version_id 关联。活动 agent 的当前名称仍要求在同一用户下唯一，由 agentService 在发布事务中检查。历史配置中的同名快照不妨碍当前角色改名。

### agent_version_skills

```sql
CREATE TABLE agent_version_skills (
    agent_version_id TEXT NOT NULL,
    skill_id TEXT NOT NULL,
    skill_version_id TEXT NOT NULL,
    position INTEGER NOT NULL CHECK(position >= 0),
    PRIMARY KEY(agent_version_id, skill_id),
    UNIQUE(agent_version_id, position)
);
CREATE INDEX agent_version_skills_version ON agent_version_skills(skill_version_id);
```

agentService 通过 skillService 检查版本归属、权限和发布状态；软删除技能不能用于新的 agent 版本，已有 agent 版本的固定引用仍可读取。更改依赖必须发布完整的新 agent_version 及新的关系行，旧关系不更新。

## 其他 Service 的关系

- conversationService 保存稳定的 @ agent_id；runService 在输入分配上固定 agent_version_id，不让名字或 latest 参与执行归属判断。
- deviceService 维护设备可用性；版本里的 device/harness 是固定选择，不随设备记录被删除而改写。
- skillService 管技能身份、版本与包的可读性；通过 fileService 存取 skills 空间，agentService 不直接写技能包。
- client sessionService 固定 agent_version_id 与事件地址；harnessService 只接收已准备好的资源路径并调用 init。

[Server 总览](server-main.md) · [skillService](server-skill.md) · [fileService](server-file.md) · [runService](server-run.md) · [Client sessionService](client-session.md) · [通信协议](protocol.md)

## 管理入口与发布请求

Server 前端的 Agent 页面提供完整配置编辑、固定 skill 版本多选、历史版本查看，以及“以此配置发布新版本”。回退也是一次新发布。

`POST /api/agents/{id}/versions` 接收完整配置，返回 AgentVersion；`GET /api/agents/{id}` 返回当前版本的扁平投影和 current_version_id，字段不再作为可直接 PATCH 的可变配置。

```json
{
  "base_version_id": "当前版本 UUID",
  "name": "Reviewer",
  "description": "代码评审",
  "daemon_id": "device-id",
  "harness": "codex",
  "model": "可选模型",
  "instructions": "AGENTS.md 内容",
  "skills": [{"skill_id": "技能 UUID", "skill_version_id": "技能版本 UUID"}]
}
```

同一配置最多选 64 个 skill。同名校验不区分大小写。同内容且 base 正确时以 HTTP 200 返回已有版本（新发布为 201）；base 过期返回 409，调用方读取最新配置后再决定是否发布。
