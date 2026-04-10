# meshserver 中心化私聊（本地 API）

Android 僅透過 mesh-proxy 的 HTTP 與 WebSocket 接入，路徑前綴：`/api/v1/meshserver/dm/`。

## 通用參數

- `connection`：與既有 [meshserver-api.md](meshserver-api.md) 相同；僅連一個 meshserver 時可省略。

## REST

| 方法 | 路徑 | 說明 |
|------|------|------|
| GET | `/api/v1/meshserver/dm/conversations` | 本地快取會話列表（會先嘗試從伺服端同步列表） |
| POST | `/api/v1/meshserver/dm/conversations` | 開啟會話：`{"peer_user_id":"user_xxx"}` |
| GET | `/api/v1/meshserver/dm/conversations/{conversation_id}/messages?limit=` | 本地訊息 |
| POST | `/api/v1/meshserver/dm/conversations/{conversation_id}/messages` | 發送：`client_msg_id`, `text`，可選 `to_user_id` |
| POST | `/api/v1/meshserver/dm/conversations/{conversation_id}/sync?after_seq=&limit=` | 向 meshserver 拉取待處理訊息並落庫 |
| POST | `/api/v1/meshserver/dm/messages/{message_id}/ack` | ACK（接收方） |

## WebSocket

- `GET /api/v1/meshserver/dm/ws`
- 事件：`type` 為 `dm_message` 或 `dm_message_state`（含 `message_id`、`conversation_id` 等）。

資料先寫入本地 SQLite（`data_dir/meshserver_dm.db`），再推送事件。
