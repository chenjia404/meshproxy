meshproxy
=========

meshproxy 是一個使用 Go 編寫的「去中心化匿名代理網路 + 本地 SOCKS5 Agent」原型實現，目前處於早期 MVP 階段。

架構與模組
--------

- **`cmd/node/main.go`**：節點主程序入口，負責讀取配置、初始化 `App` 並處理信號退出。
- **`internal/app`**：聚合配置、身份、P2P、發現、Circuit、SOCKS5、本地 API 等組件的主應用層。
- **`internal/config`**：YAML 配置載入、預設值與校驗，支援 `relay` / `relay+exit` 兩種模式。
- **`internal/identity`**：Ed25519 私鑰生成與持久化，供 libp2p host 使用。
- **`internal/p2p`**：libp2p host、DHT、Gossip、bootstrap 以及協議 ID 定義。
- **`internal/discovery`**：節點 descriptor 定義、簽名驗簽、gossip 廣播與快取管理。
- **`internal/protocol`**：電路協議常量、結構與統一 frame 編碼（CREATE/EXTEND/BEGIN_TCP/DATA/END 等），以及多跳分層加密（X25519 + AEAD 洋蔥封裝）。
- **`internal/client`**：本地 SOCKS5、路徑選擇器（PathSelector）、CircuitManager、StreamManager。
- **`internal/relay` / `internal/exit`**：relay / exit 服務實作，relay 僅解一層洋蔥並轉發，exit 解最後一層並連線目標 TCP。
- **`internal/api`**：本地管理 HTTP API 與內嵌 Web 控制台（`/console/`），提供節點狀態、已知節點、電路、stream、錯誤與指標等資訊。
- **`internal/store`**：記憶體 KV store 與 circuit store。
- **`proto/meshproxy.proto`**：後續用於生成 Protobuf 型別。
- **`configs/config.example.yaml`**：示例配置。

編譯與執行
--------

```bash
go mod tidy
go build -o bin/meshproxy ./cmd/node
./bin/meshproxy -config configs/config.example.yaml
```

啟動後可透過 **Web 控制台** 或 **本地 API** 管理與查詢狀態（API 預設 `http://127.0.0.1:19080`）：

- **Web 控制台**：在瀏覽器開啟 `http://127.0.0.1:19080/console/`，可查看節點狀態、已知 Relay/Exit、Circuit/Stream 列表、即時流量彙總、日誌面板等，無需直接查看 JSON API。

- **GET /api/v1/status**：節點狀態（`peer_id`, `mode`, `socks5_listen`, `p2p_listen_addrs`, `uptime_seconds`, `relays_known`, `exits_known`, `circuit_pool`）。
- **GET /api/v1/nodes**：已知節點 descriptor 快取。
- **GET /api/v1/relays**：已知 relay 節點列表。
- **GET /api/v1/exits**：已知 exit 節點列表。
- **GET /api/v1/circuits**：當前電路列表（含每條電路路徑 `plan`、`relay_peer_id`、`exit_peer_id`、`hop_count`、`stream_count`、`created_at`、`updated_at`）。
- **GET /api/v1/streams**：當前 stream 列表（含目標與狀態，以及對應電路的 `relay_peer_id`、`exit_peer_id`、`hop_count`）。
- **GET /api/v1/scores**：peer 評分（預留，目前回傳空）。
- **GET /api/v1/errors/recent**：最近錯誤紀錄。
- **GET /api/v1/metrics/summary**：彙總指標（circuits_total, streams_active, relays_known, exits_known, errors_recent_count, pool_status 等）。

配置說明
--------

示例配置位於 `configs/config.example.yaml`，主要字段：

- **`mode`**：`relay` 或 `relay+exit`。  
  - `relay`：僅作為中繼節點，不直接作為出口。  
  - `relay+exit`：同時具備 relay 與 exit 能力。
- **`data_dir`**：用於存放身份密鑰等持久化資料。
- **`identity_key_path`**：如為空，預設為 `${data_dir}/identity.key`。
- **`p2p.listen_addrs`**：libp2p 監聽 multiaddr 列表，例如 `"/ip4/0.0.0.0/tcp/0"`。
- **`p2p.bootstrap_peers`**：其他節點的 multiaddr，用於啟動時連線引導。
- **`socks5.listen`**：本地 SOCKS5 監聽位址，例如 `127.0.0.1:1080`。
- **`api.listen`**：本地 HTTP API 監聽位址，例如 `127.0.0.1:19080`。

雙節點測試（示意）
--------------

假設有兩個節點：

- **節點 A**：`mode: relay+exit`，同時作為 relay 與出口。  
- **節點 B**：`mode: relay`，僅作為中繼，瀏覽器連到 B 的本地 SOCKS5。

基本步驟：

1. 在節點 A 上啟動 meshproxy，記錄其 `PeerId` 與實際 `p2p_listen_addrs`。  
2. 在節點 B 的 `configs/config.example.yaml` 中，將 A 的 multiaddr 寫入 `p2p.bootstrap_peers`。  
3. 在節點 B 上啟動 meshproxy。  
4. 使用瀏覽器將 HTTP/HTTPS 代理設定為 B 的 SOCKS5 位址（預設 `127.0.0.1:1080`）。  
5. 嘗試訪問網站，並通過兩端日誌及未來的 `circuits` / `streams` 查詢電路路徑。

瀏覽器 SOCKS5 配置提示
------------------

- 瀏覽器需設定為 **SOCKS5** 代理，指向本地節點的 `socks5.listen`。  
- 建議關閉瀏覽器自帶 DNS 快取或啟用「透過 SOCKS 解析 DNS」選項，確保域名解析委託給 exit 節點（remote DNS）。

本地 API 示例
-----------

- **節點狀態（含 relay/exit 數量與電路池）**：`curl http://127.0.0.1:19080/api/v1/status | jq .`
- **已知節點**：`curl http://127.0.0.1:19080/api/v1/nodes | jq .`
- **已知 relay / exit**：`curl http://127.0.0.1:19080/api/v1/relays | jq .`、`curl http://127.0.0.1:19080/api/v1/exits | jq .`
- **電路列表（含路徑）**：`curl http://127.0.0.1:19080/api/v1/circuits | jq .`
- **Stream 列表（目標與狀態）**：`curl http://127.0.0.1:19080/api/v1/streams | jq .`
- **最近錯誤**：`curl http://127.0.0.1:19080/api/v1/errors/recent | jq .`
- **彙總指標**：`curl http://127.0.0.1:19080/api/v1/metrics/summary | jq .`

後續演進方向
--------

- 補全 SOCKS5 協議狀態機與流量轉發。
- 基於 libp2p stream 建立 relay / relay+exit 數據通道。
- 利用 DHT/Gossip 做節點發現、路由公告與健康檢查。
- 擴展本地 API，支持連線狀態、路由表與 debug 訊息查詢。

已知限制
------

- 目前僅支援 TCP 流量的多跳分層加密，且主要針對 `Local -> Relay -> Exit` 單 relay 拓撲做 MVP 驗證。
- 尚未支援 UDP、DNS over circuit 等進階功能。
- DHT / Gossip 僅用於最小節點 descriptor 發現與快取，尚未設計完整路由/信譽機制。
- Circuit / Stream 管理仍為記憶體內部狀態，尚未考慮持久化與全網健康度評估。

