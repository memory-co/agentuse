# Client settingService：一次加载的本机设置

settingService 读取 ~/.agentuse/setting.yaml，校验后提供不可变的启动快照。文件是唯一配置来源，不使用 SQLite，也不回写运行状态。

```yaml
local_http:
  enabled: true
  host: 127.0.0.1
  port: 7801
session:
  work_root: ~/.agentuse/tmp
  quota:
    max_bytes: 21474836480
```

省略 quota 表示不限容量，不因容量回收。声明 quota 即启用自动清理，max_bytes 必须为正整数；没有 enabled、cleanup 或 max_running_runs 配置。

## 生命周期

启动时文件不存在则生成默认文件。路径扩展 ~ 并规范化；非法端口、地址、quota、危险根目录或与配置/原生全局目录重叠时明确启动报错。

运行中不监听、不周期重读、不比较差异，也不保存待应用字段。Save 只校验并原子写文件，不改变当前快照。端口、host、enabled、根目录和 quota 的任何变化都要重启。

API 只返回当前进程加载的一份配置，不返回 active_work_root、restart_required 或“配置值/生效值”两套字段。页面只读，不是配置热更新入口。

## 职责边界

- sessionService 决定 conversation、workspace 和 session 的布局，settingService 不保存目录映射。
- runService 使用固定的 quota 触发检查，不自行修改容量设置。
- harnessService 使用启动时固定的包，原生控制通道不依赖页面监听端口。
- endpoint 列表与凭据保存在 endpoints.yaml，不混进本机设置。

同一 conversation 的 Codex、Claude 和自定义 harness 共用 workspace。更换 work_root 前先停止 client，需要保留状态时完整迁移目录，再按新配置启动。Client 不扫描旧根目录，不自动搬迁，也不维护 active/present/missing 目录绑定表。

quota 统计受管 conversation 目录，清理整个空闲目录；运行、等待回答和未确认清理等占用受保护。它是清理和准入策略，不是执行中的文件系统硬配额。工作目录组织及保护规则以 client-session.md 为准。

settingService 直接读取、校验和保存 setting.yaml，使用 fileutil 完成原子文件写入；没有配置 repository 或数据库层。进程仍只使用启动快照。
