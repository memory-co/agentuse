# agentService：不可變 Agent 版本與 Skill 版本關聯

> 版本化鏈路已實現，使用數據庫 schema 3 和 harnessctl-events-v2。表之間不聲明數據庫外鍵。當前 HTTP/DTO 仍使用 daemon_id 表示 device；agent_versions 的物理列爲 device_id。

## 職責與對象關係

agentService 只管理 agent 身份、不可變配置版本及其固定的 skill 版本引用。共享技能由獨立的 [skillService](server-skill.md) 管理；agentService 不接管技能包上傳、發佈、下載或存儲清理。

| 對象 | 含義 |
|---|---|
| agent | 穩定的角色 ID、所有者和當前版本指針，供消息 @ 選擇 |
| agent_version | 一份不可變的完整配置：名稱、說明、device、harness、model、指令與固定的 skill 版本列表 |
| agent_version_skills | 某一 agent 版本具體使用哪些 skill 版本，及其順序 |

**Agent 和 Skill 各自版本化，通過具體版本關聯。發佈新 skill 版本不會改變任何已有 agent 版本。** Agent 不擁有獨立的 server 對話歷史；原生上下文仍由 client session 承載。

![Agent、Skill 版本和初始化](../../images/agent-versions.svg)

## Agent 的版本規則

創建 agent 時同時創建 v1。後續每次保存有效配置變更，都新增一份完整 agent_version，並在同一 SQLite 事務中推進 agents.current_version_id；不 UPDATE 已發佈版本。名稱、說明、設備、harness、模型、指令及 skill 集合/順序都屬於版本內容。完全相同的配置不新增版本。

更新請求帶 base_version_id，必須等於當前版本；併發編輯衝突返回 409，避免後保存的內容覆蓋別人剛發佈的配置。它是版本衝突檢查，不是消息發送的冪等鍵。版本號在事務中遞增；UUID 用於穩定引用，數字 version 用於顯示 v1、v2 等。

每份配置必須引用具體 skill_version_id，不能保存 latest。回退也複製舊配置形成新的 vN+1，不修改舊版本、不把當前版本指針退回去。已發佈版本始終可用於解釋歷史任務。

agent 的用戶歸屬和穩定 ID 不變；刪除是身份的軟刪除，停止未來選擇和輸入分配，不修改歷史版本。已經固定版本的排隊輸入和實際執行繼續按原版本處理，取消須走顯式控制鏈路。被歷史引用的版本和 skill 包不因軟刪除而移除。

## 與 skillService 的關聯邊界

agentService 擁有 agents、agent_versions、agent_version_skills；skillService 擁有 skills、skill_versions 和技能包生命週期。關聯方向是 **agent_version → skill_version**，不是兩個實體的 current_version 相互跟隨。

發佈 agent 版本時，在同一發佈事務內調用 skillService 的版本校驗/讀取接口，確認每項 skill_version_id 存在、屬於聲明的 skill_id、用戶可訪問且允許用於新配置。skillService 返回不可變版本描述；agentService 檢查組合後沒有同名技能，並保存有序的關聯行。skillService 不另起提交事務，也不反向修改 agent 配置。

同一個 agent_version 對同一個 skill 只能選擇一個版本。不同 skill 的原生名稱不能衝突，名稱比較規則與 harnessctl init 一致。新增、移除、升級、降級或調整技能順序都產生新的 agent_version；已有版本引用不變。同一 skill_version 可以被多個 agent_version 共享。

例如：reviewer v2 與 coder v5 都引用 review skill v3。skillService 發佈 review v4 後，這兩個 agent 版本仍使用 v3；用戶給 reviewer 升級依賴，agentService 發佈 reviewer v3，才能引用 review v4。

Skill 包的格式、上傳 API、權限和表結構統一見 [server-skill.md](server-skill.md)。文件統一通過 fileService 的 skills 命名空間存儲，agentService 不爲自己再創建一套技能包目錄。

## 從輸入分配到 harnessctl init

1. 用戶消息 @ agent，conversationService/runService 在保存消息和輸入分配的同一事務內，讀取並固定該 agent 的當前 agent_version_id。
2. 已入隊輸入不跟隨之後的 agent 更新。領取時按固定版本讀取執行選擇、指令和 skill 依賴，組織完整 dispatch 快照。
3. Prompt 攜帶 agent_version_id、model、agent_instructions 和具體 skill 版本的下載清單。Server 不發送原生命令、argv 或 YAML。
4. Client 按 endpoint/conversation/agent/version/適配器 revision 選擇 session。同版本可複用；新版本必須創建新的 session 目錄和 events.jsonl，不能對舊事件地址重配 init。
5. Client 下載清單中的 ZIP，校驗版本、大小與 SHA-256，在 workspace 之外解包並寫指令文件，調用 harnessctl init --cwd ... --model ... --agents-md ... --skills-dir ...。未指定模型時省略 --model。
6. init 成功後提交本次輸入；後續同版本輸入走 send，是否插話由適配器決定。accepted 決定實際 run UUID，server 再將 run 關聯到該輸入固定的 agent 版本。

“每次變更做一次 init”是**下一次使用該新版本時建立新的初始化綁定**，不是每編輯一次就向所有 client 推配置。舊版本 run 可繼續完成；新版本消息不進入舊版本的原生上下文。兩者仍使用同一 conversation 的 workspace 和共享消息歷史。

Agent 指令是版本固定的 init 資源。Work 說明、協調者規則、共享歷史和本次任務仍由 server 按業務輸入組織到 prompt.content，不因持有 WorkToken 就由 client 推斷角色，也不隨每次 send 重寫 agent 的指令快照。

## API 設計

| 方法 | 接口 | 行爲 |
|---|---|---|
| POST | /api/agents | 創建穩定身份和完整 v1 |
| GET | /api/agents、/api/agents/{id} | 返回身份及當前版本；默認不列軟刪除身份 |
| POST | /api/agents/{id}/versions | 提交完整配置和 base_version_id，創建下一版本 |
| GET | /api/agents/{id}/versions | 列出歷史版本 |
| GET | /api/agents/{id}/versions/{version_id} | 讀取不可變配置和固定 skill 清單 |
| DELETE | /api/agents/{id} | 軟刪除身份，保留版本和歷史引用 |

原來的直接 PATCH agent 配置模式已移除。Owner/admin 可發佈 agent 版本；引用技能的可用性由 skillService 校驗，技能包的下載授權也歸 skillService，見其獨立設計。

## 表結構

時間爲 Unix 秒，指令爲 UTF-8 TEXT。關聯均由 service 在事務內檢查，不聲明 REFERENCES。設備刪除不能把歷史 agent_versions.device_id 改空；該版本後續不可執行，用戶需選擇新設備併發布新版本。

### agents 與 agent_versions

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

model 爲 NULL 時固定的是“採用原生默認配置”這一選擇；需要鎖定具體模型時必須顯式填寫 model，不把外部默認配置視爲被凍結。

名稱只存在版本配置中，讀取當前名稱通過 agents.current_version_id 關聯。活動 agent 的當前名稱仍要求在同一用戶下唯一，由 agentService 在發佈事務中檢查。歷史配置中的同名快照不妨礙當前角色改名。

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

agentService 通過 skillService 檢查版本歸屬、權限和發佈狀態；軟刪除技能不能用於新的 agent 版本，已有 agent 版本的固定引用仍可讀取。更改依賴必須發佈完整的新 agent_version 及新的關係行，舊關係不更新。

## 其他 Service 的關係

- conversationService 保存穩定的 @ agent_id；runService 在輸入分配上固定 agent_version_id，不讓名字或 latest 參與執行歸屬判斷。
- deviceService 維護設備可用性；版本里的 device/harness 是固定選擇，不隨設備記錄被刪除而改寫。
- skillService 管技能身份、版本與包的可讀性；通過 fileService 存取 skills 空間，agentService 不直接寫技能包。
- client sessionService 固定 agent_version_id 與事件地址；harnessService 只接收已準備好的資源路徑並調用 init。

[Server 總覽](server-main.md) · [skillService](server-skill.md) · [fileService](server-file.md) · [runService](server-run.md) · [Client sessionService](client-session.md) · [通信協議](protocol.md)

## 管理入口與發佈請求

Server 前端的 Agent 頁面提供完整配置編輯、固定 skill 版本多選、歷史版本查看，以及“以此配置發佈新版本”。回退也是一次新發布。

`POST /api/agents/{id}/versions` 接收完整配置，返回 AgentVersion；`GET /api/agents/{id}` 返回當前版本的扁平投影和 current_version_id，字段不再作爲可直接 PATCH 的可變配置。

```json
{
  "base_version_id": "當前版本 UUID",
  "name": "Reviewer",
  "description": "代碼評審",
  "daemon_id": "device-id",
  "harness": "codex",
  "model": "可選模型",
  "instructions": "AGENTS.md 內容",
  "skills": [{"skill_id": "技能 UUID", "skill_version_id": "技能版本 UUID"}]
}
```

同一配置最多選 64 個 skill。同名校驗不區分大小寫。同內容且 base 正確時以 HTTP 200 返回已有版本（新發布爲 201）；base 過期返回 409，調用方讀取最新配置後再決定是否發佈。
