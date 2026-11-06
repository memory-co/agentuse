# Client endpointService：啓動時固定的輸入來源

endpointService 管理 endpoints.yaml，向 client 提供已註冊來源的身份。它不創建連接、不領取任務、不選擇 native run_id，也不維護在線狀態或執行嘗試。

## 文件

```yaml
endpoints:
  - name: company
    server_url: https://company.example.com
    enabled: true
    device_id: registered-device
    device_key: device-credential
```

endpoint_id 從規範化地址推導，不寫入 YAML：例如 https://company.example.com 對應 https---company.example.com。:// 使用 ---，其他不安全路徑字節編碼，端口、路徑等均參與標識，不能只按 host 區分。

來源地址和設備憑據在啓動時校驗並固定。運行中修改、註冊、啓停、移除或換憑據不改變當前進程；必須重啓。不發送重讀通知或定期掃描，不根據磁盤差異動態創建/銷燬連接。

註冊 CLI 可以原子更新文件，但不會修改已運行 client 的快照。文件缺失表示沒有遠端註冊，仍可使用 local。保存前檢查地址合法、憑據完整及規範化地址不重複。

## 內置 local

local 始終存在，不聲明在 endpoints.yaml 中，不連接中心。它提供本機輸入入口，使用同一 sessionService、harnessService、公共事件和 native run 查詢；不創建 local 專用的 run/attempt、結果賬本或執行協議。

local conversation 的任意標題保存在 conversation.yaml，目錄使用 UUID；標題不能作爲文件路徑。遠端 conversation 標識來自其來源，namespace 保持隔離。

## 查詢與邊界

| 接口 | 行爲 |
|---|---|
| List / Views | 返回啓動快照及 local；Views 不含 device_key |
| GetByID | 解析本機來源，不切換執行中任務的身份 |
| 註冊 / 保存 / 移除 | 更新 YAML，重啓後生效 |

同一來源下的實際 run UUID仍由 harnessctl 分配，不能用 server 派發 ID 替代。不同 endpoint 的工作目錄、session 和公開查詢互相隔離；憑據只傳給該來源的調用，不通過修改進程全局環境切換。

中心輸入分配、公共事件確認和業務 CLI 憑據屬於 run/server_connection.go 的傳輸邊界；endpointService 不解釋這些業務狀態。刪除來源配置不刪除本機歷史，不代表授權重新執行舊輸入。

endpointService 直接讀寫 endpoints.yaml，並在本包內負責 URL 規範化與 endpoint_id 推導。身份規則不放入通用文件工具；YAML 原子寫入和路徑檢查使用 fileutil。
