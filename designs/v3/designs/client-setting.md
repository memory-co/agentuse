# Client settingService：一次加載的本機設置

settingService 讀取 ~/.agentuse/setting.yaml，校驗後提供不可變的啓動快照。文件是唯一配置來源，不使用 SQLite，也不回寫運行狀態。

```yaml
local_http:
  enabled: true
  host: 127.0.0.1
  port: 7801
session:
  work_root: ~/.agentuse/workspace
  quota:
    max_bytes: 21474836480
```

省略 quota 表示不限容量，不因容量回收。聲明 quota 即啓用自動清理，max_bytes 必須爲正整數；沒有 enabled、cleanup 或 max_running_runs 配置。

## 生命週期

啓動時文件不存在則生成默認文件。路徑擴展 ~ 並規範化；非法端口、地址、quota、危險根目錄或與配置/原生全局目錄重疊時明確啓動報錯。

運行中不監聽、不周期重讀、不比較差異，也不保存待應用字段。Save 只校驗並原子寫文件，不改變當前快照。端口、host、enabled、根目錄和 quota 的任何變化都要重啓。

API 只返回當前進程加載的一份配置，不返回 active_work_root、restart_required 或“配置值/生效值”兩套字段。頁面只讀，不是配置熱更新入口。

## 職責邊界

- sessionService 決定 conversation、workspace 和 session 的佈局，settingService 不保存目錄映射。
- runService 使用固定的 quota 觸發檢查，不自行修改容量設置。
- harnessService 使用啓動時固定的包，原生控制通道不依賴頁面監聽端口。
- endpoint 列表與憑據保存在 endpoints.yaml，不混進本機設置。

同一 conversation 的 Codex、Claude 和自定義 harness 共用 workspace。更換 work_root 前先停止 client，需要保留狀態時完整遷移目錄，再按新配置啓動。Client 不掃描舊根目錄，不自動搬遷，也不維護 active/present/missing 目錄綁定表。

quota 統計受管 conversation 目錄，清理整個空閒目錄；運行、等待回答和未確認清理等佔用受保護。它是清理和准入策略，不是執行中的文件系統硬配額。工作目錄組織及保護規則以 client-session.md 爲準。

settingService 直接讀取、校驗和保存 setting.yaml，使用 fileutil 完成原子文件寫入；沒有配置 repository 或數據庫層。進程仍只使用啓動快照。
