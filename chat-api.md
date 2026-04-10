# mesh-proxy 聊天 HTTP / WebSocket API

本文件描述內建 Local API 上與**聊天**相關的介面，供第三方程式透過標準 **HTTP** 與 **WebSocket** 接入。

- **傳輸**：一般 REST 呼叫為 **HTTP/1.1**；即時推送為 **WebSocket**（在 HTTP 上升級）。
- **基底 URL**：由設定檔 `api.listen` 決定，預設為 `http://127.0.0.1:19080`。下文路徑均相對於此基底（例如 `GET /api/v1/chat/me` 即 `http://127.0.0.1:19080/api/v1/chat/me`）。
- **字元編碼**：JSON 請求/回應為 **UTF-8**；`Content-Type` 通常為 `application/json`（下載檔案、頭像等除外）。

若節點未啟用聊天服務，相關路由會回傳 **404**，內文可能為純文字 `chat service not available`。

### 可选上游 relay / offline store（meshchat-server）

- 在配置 `chat.meshchat_server_url` 填入 **meshchat-server** 的 HTTP 根地址（如 `http://127.0.0.1:8581`）后，mesh-proxy 会用本机 libp2p 身份登录上游，并在**不改变 Android 接入方式**的前提下，将文本私聊额外上送到上游、拉取补全、转发 ACK。
- 私聊主模型仍在本地 SQLite；单聊 REST/WebSocket 仍为 `/api/v1/chat/...`。消息 JSON 可多出可选字段：`transport_kind`、`relay_status`、`upstream_message_id`、`client_msg_id`、`ack_pending`、`last_relay_at`（见实现与数据字典）。

---

## CORS 與 OPTIONS

API 外層會對所有請求套用 CORS（`Access-Control-Allow-Origin: *` 等）。瀏覽器發起 **OPTIONS** 預檢時會回 **204 No Content**。

---

## 共通約定

| 項目 | 說明 |
|------|------|
| JSON | 請求體使用 `Content-Type: application/json`（`POST`/`DELETE` 等帶 body 時）。 |
| 錯誤 | 多數錯誤為純文字 body；成功時多為 JSON（`writeJSON`）。 |
| 認證 | 目前 Local API **無**獨立 API Key；請在受信任網路或本機使用。 |
| 時間欄位 | `time.Time` 序列化為 **RFC3339** 字串（如 `2006-01-02T15:04:05Z07:00`）。 |
| `[]byte` | JSON 中通常為 **Base64 字串**（若該欄位出現在回應中）。 |

### 檔案大小上限（實作常數）

- 聊天附件（單聊/群組）：單檔最大 **64 MiB**（`MaxChatFileBytes`）。
- 個人頭像：`multipart` 欄位 `avatar`，最大 **512 KiB**。

### 單聊 `conversation_id` 產生規則

先前版次的 API 說明**未**單獨列出；實作上由 `deriveStableConversationID` 決定（`internal/chat/direct_store.go`）。第三方若要在本地預先推算「與某 Peer 的會話 ID」，可依下列步驟（與節點內部一致）：

1. 令 `A` = 本節點 PeerID 字串、`B` = 對端 PeerID 字串。
2. 將 `A`、`B` **依字典序排序**（升序），得到 `parts[0]`、`parts[1]`。
3. 計算位元組序列的 **SHA-256**：輸入為 UTF-8 字串  
   `stable_v1:` + `parts[0]` + **單一字節 NUL**（`\x00`）+ `parts[1]`。
4. 會話 ID = 字串 **`stable_v1:`** + 上述雜湊的 **64 位小寫十六進位**（共 32 bytes 雜湊 → 64 個 hex 字元）。

因此同一對 Peer 在兩端會得到**相同**的 `conversation_id`（與誰先發起無關）。若資料庫曾遷移舊 ID，節點可能會把會話遷移到上述目標 ID（仍以 API 回傳為準）。

**群組**路徑使用的 `group_id` 另有產生規則，與單聊不同；請以 **GET /api/v1/groups** 等 API 回傳值為準。

---

## 個人資料與頭像

### `GET /api/v1/chat/me`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：單一 **`Profile`** 物件，見下表。

| 欄位 | 類型 | 說明 |
|------|------|------|
| `peer_id` | string | 本節點 libp2p PeerID |
| `nickname` | string | 暱稱 |
| `bio` | string | 簡介 |
| `avatar` | string | 頭像檔名（搭配 `GET /api/v1/chat/avatars/{name}`） |
| `avatar_cid` | string | 頭像在嵌入式 IPFS 中的 CID（未啟用 IPFS 或匯入失敗時為空） |
| `chat_kex_pub` | string | 聊天金鑰交換公鑰（展示用） |
| `created_at` | string | 建立時間（RFC3339） |
| `binding_eth_address` | string | 當前已綁定之 **Ethereum** 位址（小寫 `0x` 前綴）；無綁定時省略 |

---

### `GET /api/v1/chat/profile` / `POST /api/v1/chat/profile`

- **GET**：同 `GET /api/v1/chat/me`，回應欄位同上 **`Profile`**（含 `binding_eth_address`，見「**PeerID 綁定 Ethereum 地址**」）。
- **POST**：更新暱稱與簡介；回應 **`Profile`** 同樣可含 `binding_eth_address`。

**請求 body（POST）**：

```json
{
  "nickname": "string",
  "bio": "string"
}
```

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：單一 **`Profile`** 物件，欄位同 **GET /api/v1/chat/me** 一節之表格。

---

### `POST /api/v1/chat/profile/avatar`

`multipart/form-data`，欄位名 **`avatar`**（檔案）。

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：單一 **`Profile`** 物件，欄位同 **GET /api/v1/chat/me** 一節之表格（含 `binding_eth_address` 時與該表一致）。

---

### PeerID 綁定 Ethereum 地址（`BindingRecord`）

以下端點用於**本機**建立綁定草稿、提交外部錢包簽名後的完整記錄、或清除本機綁定展示。**不**在節點內部產生 ETH 簽名；libp2p 端簽名使用節點身份私鑰。  
**說明**：`GET /api/v1/chat/me`、`GET /api/v1/chat/profile`、更新頭像／暱稱等回傳之 **`Profile`** 僅包含 **`binding_eth_address`**，不會在 JSON 中回傳完整 **`BindingRecord`**；完整雙簽結構仍存於本機並用於同步與校驗。

#### 共用型別說明

**`BindingPayload`**（待簽內容 / 持久化欄位）：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `version` | number | 固定為 **1** |
| `action` | string | 固定為 **`bind_eth_address`** |
| `peer_id` | string | 本節點 libp2p PeerID（與提交時本機身份一致） |
| `eth_address` | string | **小寫** `0x` 開頭的 20-byte 位址（42 字元） |
| `chain_id` | number | EVM 鏈 ID（**必須 &gt; 0**） |
| `domain` | string | 固定為 **`meshchat`** |
| `nonce` | string | 隨機字串（實作為 16 位元組之 hex） |
| `seq` | number | 本機單調遞增序號；每次建立新草稿為 **當前 `binding_seq` + 1**（清除綁定**不**重置 `binding_seq`） |
| `issued_at` | number | 簽發時間，**Unix 秒**（UTC） |
| `expire_at` | number | 過期時間，**Unix 秒**（UTC） |

**`SignatureEnvelope`**（單一演算法之簽名）：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `algo` | string | 演算法標識：`libp2p`（Peer 端）或 `eip191`（Ethereum 端） |
| `value` | string | 簽名內容：`libp2p` 為 **Base64**（原始位元組）；`eip191` 為錢包回傳之 **hex 字串**（通常含 `0x` 前綴） |

**`BindingRecord`**（完整雙簽記錄，存於本機 `profile` 表並可同步給聯絡人）：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `payload` | object | 同上 **`BindingPayload`** |
| `signatures` | object | 兩段簽名 |
| `signatures.peer` | object | **`SignatureEnvelope`**：`algo`=`libp2p`，`value` 為對 **canonical message** 的 libp2p 簽名（Base64） |
| `signatures.ethereum` | object | **`SignatureEnvelope`**：`algo`=`eip191`，`value` 為對同一 canonical 的 EIP-191 簽名（hex） |

**Canonical message**（供錢包與驗簽邏輯重建，換行為 `\n`）：

```text
MeshChat Binding v1
action: bind_eth_address
peer_id: {peer_id}
eth_address: {eth_address}
chain_id: {chain_id}
domain: meshchat
nonce: {nonce}
seq: {seq}
issued_at: {issued_at}
expire_at: {expire_at}
```

（`eth_address` 在字串中為小寫；欄位順序固定。）

---

### `POST /api/v1/chat/profile/binding/draft`

建立綁定草稿：產生 **`payload`**、**`canonical_message`**，並以本機 **libp2p 身份私鑰**簽出 **`peer_signature`**（Base64）。客戶端再用外部錢包對 **`canonical_message`** 做 EIP-191 簽名，於下一步提交。

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json`（請求與回應） |

**請求 body**：

```json
{
  "eth_address": "0x...",
  "chain_id": 1,
  "ttl_seconds": 2592000
}
```

| 欄位 | 類型 | 說明 |
|------|------|------|
| `eth_address` | string | 要綁定之 Ethereum 位址（可混合大小寫；伺服器會規範為小寫） |
| `chain_id` | number | 鏈 ID（**必須 &gt; 0**） |
| `ttl_seconds` | number | 自簽發時起有效秒數；**省略或 ≤0** 時預設為 **365 天**；**上限 3650 天**（超出會被截斷） |

**回應欄位**：單一 **`BindingDraft`** 物件。

| 欄位 | 類型 | 說明 |
|------|------|------|
| `payload` | object | 本次草稿的 **`BindingPayload`**（含 `nonce`、`seq`、`issued_at`/`expire_at` 等） |
| `canonical_message` | string | 待送錢包簽名之多行 UTF-8 字串（規則見上） |
| `peer_signature` | string | 本機 libp2p 私鑰對 canonical 位元組之簽名，**Base64** 編碼 |

---

### `POST /api/v1/chat/profile/binding`

提交完整綁定：請求體須包含與草稿一致的 **`payload`**、**`peer_signature`**，以及錢包產生之 **`eth_signature`**。伺服器會驗證 `peer_id`、序號、`seq` 遞增、未過期、libp2p 驗簽與 ETH 簽名格式後寫入本機 profile。

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**請求 body**：

```json
{
  "payload": { },
  "peer_signature": "base64...",
  "eth_signature": "0x..."
}
```

| 欄位 | 類型 | 說明 |
|------|------|------|
| `payload` | object | 與草稿一致 **`BindingPayload`**（欄位需已規範化，例如 `eth_address` 小寫） |
| `peer_signature` | string | 與草稿相同之 libp2p 簽名（Base64） |
| `eth_signature` | string | 錢包對 **`canonical_message`** 的 EIP-191 簽名（hex，通常 `0x` 開頭；節點僅做格式與非空校驗，**不**在內建邏輯中從簽名恢復地址） |

**回應欄位**：單一 **`Profile`** 物件，欄位同 **GET /api/v1/chat/me**（含 `binding_eth_address`）。

---

### `DELETE /api/v1/chat/profile/binding`

清除本機當前綁定展示欄位（**不**重置 `binding_seq`，也不發送解綁廣播）。

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `ok` | boolean | 固定為 **`true`** |

---

### `GET /api/v1/chat/avatars/{name}`

取得頭像檔（二進位，非 JSON）。

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | 依檔案而定（由 `ServeFile` 決定） |
| 標頭 | `Cache-Control: public, max-age=31536000, immutable` |

**回應體**：圖檔位元組（無 JSON 欄位）。

---

## 好友請求（Friend Request）

### `GET /api/v1/chat/requests`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：JSON **陣列**，元素為 **`Request`** 物件。

| 欄位 | 類型 | 說明 |
|------|------|------|
| `request_id` | string | 請求 ID |
| `from_peer_id` | string | 發起方 PeerID |
| `to_peer_id` | string | 接收方 PeerID |
| `state` | string | 如 `pending`、`accepted`、`rejected`、`blocked` |
| `intro_text` | string | 附言 |
| `nickname` | string | 對方暱稱（展示） |
| `bio` | string | 對方簡介 |
| `avatar` | string | 對方頭像名 |
| `retention_minutes` | number | 保留時間（分鐘） |
| `remote_chat_kex_pub` | string | 對方聊天 KEX 公鑰 |
| `conversation_id` | string | 若已有會話則出現；否則可能省略 |
| `last_transport_mode` | string | 最近傳輸模式（如 `direct`） |
| `retry_count` | number | 重試次數（可省略） |
| `next_retry_at` | string | 下次重試時間 RFC3339（可省略） |
| `retry_job_status` | string | 重試工作狀態（可省略） |
| `created_at` | string | 建立時間（RFC3339） |
| `updated_at` | string | 更新時間（RFC3339） |

---

### `POST /api/v1/chat/requests`

**請求 body**：

```json
{
  "to_peer_id": "string",
  "intro_text": "string"
}
```

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：單一 **`Request`** 物件，欄位同 **GET /api/v1/chat/requests** 一節之陣列元素表。

---

### `POST /api/v1/chat/requests/{request_id}/accept`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：單一 **`Conversation`** 物件。

| 欄位 | 類型 | 說明 |
|------|------|------|
| `conversation_id` | string | 會話 ID |
| `peer_id` | string | 對端 PeerID |
| `state` | string | 如 `active`、`no_friend` |
| `last_message_at` | string | 最後訊息時間（RFC3339） |
| `last_message` | string | 最後一則訊息預覽（最多 20 個 Unicode 字元，UTF-8；無訊息或舊資料可省略或為空字串） |
| `last_transport_mode` | string | 最近傳輸模式 |
| `unread_count` | number | 未讀數 |
| `retention_minutes` | number | 保留時間（分鐘） |
| `retention_sync_state` | string | 保留策略同步狀態 |
| `retention_synced_at` | string | 最後同步時間 RFC3339（可省略） |
| `created_at` | string | 建立時間（RFC3339） |
| `updated_at` | string | 更新時間（RFC3339） |

---

### `POST /api/v1/chat/requests/{request_id}/reject`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `status` | string | 固定為 `rejected` |

---

## 聯絡人

### `GET /api/v1/chat/contacts`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：JSON **陣列**，元素為 **`Contact`** 物件。

| 欄位 | 類型 | 說明 |
|------|------|------|
| `peer_id` | string | 聯絡人 PeerID |
| `nickname` | string | 顯示暱稱 |
| `bio` | string | 簡介 |
| `avatar` | string | 頭像檔名 |
| `cid` | string | 對方頭像內容在嵌入式 IPFS 中的 CID（已拉取並成功 pin 後才有；否則省略或空字串） |
| `remote_nickname` | string | 對方原始暱稱（可省略） |
| `retention_minutes` | number | 保留時間（分鐘） |
| `blocked` | boolean | 是否封鎖 |
| `last_seen_at` | string | 最後上線（RFC3339） |
| `updated_at` | string | 更新時間（RFC3339） |
| `binding_eth_address` | string | 對端已同步之 **Ethereum** 位址（小寫 `0x` 前綴）；無則省略 |

---

### `GET /api/v1/chat/contacts/{peer_id}/binding`

讀取指定聯絡人在本機快取之完整 **`BindingRecord`**（經 **`binding_sync`** 寫入），以及本機校驗狀態。  
**前提**：須已與該 `peer_id` 建立單聊會話（與列表聯絡人之條件一致）；否則回 **404**。

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**（**`ContactBindingDetails`**）：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `peer_id` | string | 聯絡人 PeerID |
| `binding` | object | 完整 **`BindingRecord`**（`payload` + `signatures`）；本機尚無快取記錄時省略 |
| `binding_status` | string | 本機校驗狀態（可省略）：`valid`、`expired`、`peer_sig_invalid`、`eth_sig_invalid`、`parse_error`、`stale` 等 |
| `binding_validated_at` | number | 最近一次校驗時間，**Unix 毫秒時間戳**（UTC；無則省略或為 0） |
| `binding_error` | string | 最近一次校驗失敗之簡短說明（可省略） |

**錯誤**：無對應會話或無聯絡人資料時 **404 Not Found**。

---

### `DELETE /api/v1/chat/contacts/{peer_id}`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `ok` | boolean | 固定為 `true` |
| `peer_id` | string | 被刪除的 PeerID |

---

### `POST /api/v1/chat/contacts/{peer_id}/nickname`

**請求 body**：`{"nickname":"string"}`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：單一 **`Contact`** 物件，欄位同 **GET /api/v1/chat/contacts** 一節之陣列元素表。

---

### `POST /api/v1/chat/contacts/{peer_id}/block`

**請求 body**：`{"blocked":true}`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：單一 **`Contact`** 物件，欄位同 **GET /api/v1/chat/contacts** 一節之陣列元素表。

---

## 單聊會話與訊息

### `GET /api/v1/chat/conversations`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：JSON **陣列**，元素為 **`Conversation`** 物件，欄位同 **POST /api/v1/chat/requests/{request_id}/accept** 一節之 `Conversation` 表。

---

### `DELETE /api/v1/chat/conversations/{conversation_id}`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `ok` | boolean | 固定為 `true` |
| `conversation_id` | string | 被刪除的會話 ID |

---

### `POST /api/v1/chat/conversations/{conversation_id}/sync`

向對端觸發同步（背景執行）。

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **202 Accepted** |
| Content-Type | `application/json` |

**回應欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `conversation_id` | string | 會話 ID |
| `status` | string | 固定為 `sync_requested` |

完成後請用 **GET messages** 或 **WebSocket** 取得更新。

---

### `POST /api/v1/chat/conversations/{conversation_id}/read`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：單一 **`Conversation`** 物件，欄位同 **POST /api/v1/chat/requests/{request_id}/accept** 一節之 `Conversation` 表。

---

### `GET /api/v1/chat/conversations/{conversation_id}/messages`

- **無查詢參數**：回傳該會話全部訊息。
- **分頁**：`?limit=1..500&offset=0..`（至少帶 `limit` 或 `offset` 其一啟用分頁）。

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位（無參數）**：JSON **陣列**，元素為 **`Message`** 物件，見下表。

**回應欄位（分頁）**：單一 JSON 物件：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `messages` | array | 元素為 **`Message`** |
| `total` | number | 總筆數 |
| `limit` | number | 本次 limit |
| `offset` | number | 本次 offset |
| `has_more` | boolean | 是否還有後續資料 |

**`Message` 物件欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `msg_id` | string | 訊息 ID |
| `conversation_id` | string | 會話 ID |
| `sender_peer_id` | string | 發送方 PeerID |
| `receiver_peer_id` | string | 接收方 PeerID |
| `direction` | string | 方向（如 `out` / `in`） |
| `msg_type` | string | 如 `chat_text`、`chat_file` |
| `plaintext` | string | 文字內容 |
| `file_name` | string | 檔名（檔案訊息；可省略） |
| `mime_type` | string | MIME（可省略） |
| `file_size` | number | 檔案大小（可省略） |
| `file_cid` | string | 檔案 CID（可省略） |
| `transport_mode` | string | 如 `direct` |
| `state` | string | 傳送狀態（見 `MessageState*`） |
| `counter` | number | 序號（uint64） |
| `created_at` | string | 建立時間（RFC3339） |
| `delivered_at` | string | 送達時間（RFC3339，可省略） |

---

### `POST /api/v1/chat/conversations/{conversation_id}/messages`

**請求 body**：`{"text":"string"}`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：單一 **`Message`** 物件，欄位同 **GET /api/v1/chat/conversations/{conversation_id}/messages** 一節之 `Message` 表。

---

### `POST /api/v1/chat/conversations/{conversation_id}/files`

`multipart/form-data`，欄位名 **`file`**（必填）。

| 欄位 | 類型 | 說明 |
|------|------|------|
| `file` | file | 上傳的檔案內容 |
| `upload_to_ipfs` | string（可選） | 是否將檔案匯入本機**嵌入式 IPFS** 並 pin（與頭像匯入參數一致）。可填 `true` / `false` / `1` / `0`；**省略時視同 `false`**。為 `true` 時須已啟用並就緒嵌入式 IPFS，否則請求失敗；為 `false` 時僅依內容計算 `file_cid`（與既有行為相同），不寫入 IPFS。 |

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：單一 **`Message`** 物件（`msg_type` 通常為 `chat_file`），欄位同 **GET /api/v1/chat/conversations/{conversation_id}/messages** 一節之 `Message` 表。

---

### `GET /api/v1/chat/conversations/{conversation_id}/messages/{msg_id}/file`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | 依訊息 `mime_type`，預設 `application/octet-stream` |
| 標頭 | `Content-Disposition: attachment; filename="..."` |

**回應體**：檔案位元組（無 JSON 欄位）。

---

### `POST /api/v1/chat/conversations/{conversation_id}/messages/{msg_id}/revoke`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `status` | string | 固定為 `revoked` |

---

### `POST /api/v1/chat/conversations/{conversation_id}/retention`

**請求 body**：`{"retention_minutes": 0}`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：單一 **`Conversation`** 物件，欄位同 **POST /api/v1/chat/requests/{request_id}/accept** 一節之 `Conversation` 表。

---

## 群組

### `GET /api/v1/groups`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：JSON **陣列**，元素為 **`Group`** 物件。

| 欄位 | 類型 | 說明 |
|------|------|------|
| `group_id` | string | 群組 ID |
| `title` | string | 標題 |
| `avatar` | string | 頭像 |
| `controller_peer_id` | string | 擁有者/控制者 PeerID |
| `current_epoch` | number | 目前 epoch |
| `retention_minutes` | number | 保留時間（分鐘） |
| `state` | string | 如 `active`、`archived` |
| `last_event_seq` | number | 最後事件序號 |
| `last_message_at` | string | 最後訊息時間（RFC3339） |
| `member_count` | number | 成員數（可省略） |
| `local_member_role` | string | 本機角色（可省略） |
| `local_member_state` | string | 本機成員狀態（可省略） |
| `created_at` | string | 建立時間（RFC3339） |
| `updated_at` | string | 更新時間（RFC3339） |

---

### `POST /api/v1/groups`

**建立請求 body**：

```json
{
  "title": "string",
  "members": ["peer_id", "..."]
}
```

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：單一 **`Group`** 物件，欄位同 **GET /api/v1/groups** 一節之陣列元素表。

---

### 建立群組時併發的私聊訊息（P2P，`group_invite_notice`）

HTTP **`POST /api/v1/groups`** 在伺服器內會建立群組，並對 `members` 內每個 PeerID 呼叫與 **`POST .../groups/{group_id}/invite`** 相同的邀請流程。因此：

- 每位被邀請者必須**已與本機存在單聊會話**（已加好友），否則建立會失敗（與「邀請成員需先有加好友」一致）。
- 除透過群組協定向其他成員廣播 **`group_control`** 事件外，對每位被邀請者還會在**既有單聊會話**上多送一則私聊訊息，類型為 **`group_invite_notice`**（常數 `MessageTypeGroupInviteNote`），與一般文字 `chat_text` 共用同一套單聊傳輸（如 `/meshproxy/chat/msg/1.0.0` 上的 JSON 訊息框）。

#### 外層：`ChatText` 封裝（與 `chat_text` 相同結構）

| 欄位 | 類型 | 說明 |
|------|------|------|
| `type` | string | 固定 **`group_invite_notice`**（勿與一般文字 `chat_text` 混淆） |
| `conversation_id` | string | 與該被邀請者之間的單聊會話 ID |
| `msg_id` | string | 實作上通常為 `group-invite-{event_id}` |
| `from_peer_id` | string | 發送方（邀請人 / 控制者）PeerID |
| `to_peer_id` | string | 接收方（被邀請者）PeerID |
| `ciphertext` | bytes | AEAD 密文（JSON 中常為 Base64） |
| `counter` | number | 單聊序號（與會話 send counter 一致） |
| `sent_at_unix` | number | 毫秒時間戳 |

#### 密文與 AEAD

- 解密金鑰與序號與一般單聊文字訊息相同（會話 E2EE `SendKey` / `RecvKey`、nonce `BuildAEADNonce("fwd", counter)`）。
- **附加資料 AAD** 字串為：

  `conversation_id + "\x00" + "group_invite_notice"`

  （第二段為字面常數 `group_invite_notice`，與 `type` 欄位取值一致。）

- 解密後為 **UTF-8 JSON**，對應 **`GroupInviteNoticePayload`**（見下表）。

#### 內層：`GroupInviteNoticePayload`（明文 JSON）

| 欄位 | 類型 | 說明 |
|------|------|------|
| `group_id` | string | 群組 ID |
| `title` | string | 群組標題 |
| `controller_peer_id` | string | 控制者 PeerID（須與 `from_peer_id` 一致） |
| `invitee_peer_id` | string | 被邀請者 PeerID（接收方本機應為自己） |
| `invite_text` | string | 邀請附言（可省略） |
| `event_id` | string | 對應群組事件 ID |
| `event_seq` | number | 事件序號 |
| `current_epoch` | number | 群組當前 epoch |
| `local_member_state` | string | 發送端打包時通常為空；接收端本地落庫時可能為 `invited` 等 |
| `invite_envelope` | object | **`GroupControlEnvelope`**，內含簽名後的 **`group_invite`** 事件（見下表） |

#### `invite_envelope`：`GroupControlEnvelope`

| 欄位 | 類型 | 說明 |
|------|------|------|
| `type` | string | 固定 **`group_control`** |
| `event_type` | string | 邀請時為 **`group_invite`** |
| `group_id` | string | 與外層 `group_id` 一致 |
| `event_id` | string | 事件 ID |
| `event_seq` | number | 事件序號 |
| `actor_peer_id` | string | 行為者 PeerID |
| `signer_peer_id` | string | 簽名者 PeerID（須與單聊發送方一致） |
| `created_at_unix` | number | 毫秒時間戳 |
| `payload` | object | 見下表 **`GroupInvitePayload`**（序列化在 JSON 中為物件；儲存時可能以原始字串形式出現在部分 API） |
| `signature` | bytes | 事件簽名（JSON 中常為 Base64） |

#### `payload`：`GroupInvitePayload`（`event_type === "group_invite"` 時）

| 欄位 | 類型 | 說明 |
|------|------|------|
| `invitee_peer_id` | string | 被邀請者 |
| `role` | string | 如 `member` / `admin` |
| `invite_text` | string | 邀請附言 |
| `epoch` | number | 群組 epoch |
| `title` | string | 群組標題 |
| `controller_peer_id` | string | 控制者 |
| `known_members` | array | 字串陣列，已知成員 PeerID 列表 |
| `wrapped_group_key` | bytes | 給被邀請者的當前 epoch 金鑰封裝（JSON 中常為 Base64） |

接收端會校驗 `InviteEnvelope` 與 `GroupInviteNoticePayload` 一致性（例如 `group_id`、`signer_peer_id` 與發送方），再套用遠端群組邀請邏輯。

#### 邊界與行為說明

| 情況 | 行為 |
|------|------|
| **`members` 為空或僅含空白** | 只建立「僅有建立者（控制者）」的群組，**不會**對任何人發送 `group_invite_notice`，也不會進入邀請迴圈。 |
| **`members` 內重複 PeerID** | 實作會去重；同一對端只邀請一次，對應**一則** `group_invite_notice`。 |
| **缺少與某成員的單聊會話** | 建立群組時會在驗證階段失敗，錯誤語意為需先有單聊；**不會**部分成功建立再邀請。 |
| **建立成功後、邀請迴圈中某次 `InviteGroupMember` 失敗** | API 會回錯，但此時群組可能**已寫入**，且**先前幾位**被邀請者的狀態可能已落庫；並非整段建立+邀請在同一資料庫交易內原子提交。第三方若需嚴格一致，應以伺服器回傳與後續 **`GET /api/v1/groups/{group_id}`** 為準。 |
| **`group_control` 廣播與「僅自己」的群** | 建立當下成員僅控制者時，對 `group_create` 事件的廣播收件者可能為空；被邀請者主要是在**後續每次 `InviteGroupMember`** 時收到群組控制流與私聊 `group_invite_notice`。 |
| **私聊 `group_invite_notice` 發送失敗** | 實作上若 `sendDirectGroupInviteNotice` 失敗**僅記錄日誌**，`InviteGroupMember` / `CreateGroup` 仍可能回傳成功；意即**群內邀請狀態已寫入，但對方單聊裡可能沒有這條通知訊息**。對端可依賴群組同步／控制事件補齊，或請發起方重試邀請。 |
| **僅透過 `POST .../groups/{group_id}/invite` 邀請** | 與建立時邀請相同：須已與對方有單聊，並同樣會嘗試發送一則 **`group_invite_notice`**（格式同上）。 |
| **本機 PeerID** | 不會邀請自己；`members` 若誤含本機會在去重時略過。 |

---

### 群組協定的離線 `store` 兜底

群組流量除原本的 **direct** / **relay** 外，現在也會在特定情況下寫入離線 **`store`**。這是**傳輸層兜底**，不是新的 HTTP API，也**不改變**本節各接口的請求 / 回應格式。

#### 目前會寫入 `store` 的群組載荷

- **群組正文訊息**：`group_chat_text`、`group_chat_file`
- **群控事件**：`group_control`
- **群組請求**：`group_join_request`、`group_leave_request`

#### 觸發條件

- 發送端先按既有邏輯嘗試 **直連** 與 **relay**
- 若該次送出失敗，會再嘗試把對應收件者的一份群組 wire 載荷寫入其離線 `store`
- 若寫入 `store` 成功，發送端會把該次投遞視為已進入傳輸層，等待對端後續拉取並處理

#### 接收端行為

- 接收端定期從已配置的離線 `store` 拉取待處理項目
- 拉取到的群組載荷會先驗證 **外層 store envelope 簽名**
- 再解析內層群組 wire，復用既有群組驗簽 / 落庫 / ACK / 事件套用流程
- 對於正文訊息，最終仍以 `GroupMessage` / `GroupDeliverySummary` 等既有資料模型對外呈現
- 對於群控事件，最終仍以本地群組狀態變化為準，例如標題更新、成員加入 / 退出、控制者變更等

#### 邊界與限制

| 情況 | 行為 |
|------|------|
| **未配置離線 `store`** | 群組仍只走 direct / relay；本節兜底能力不生效。 |
| **direct / relay 成功** | 不會因為已成功送達而額外再寫一份群組離線 `store`。 |
| **某位成員 direct / relay 失敗，但 `store` 寫入成功** | 該成員後續可在離線時段結束後由自己拉取；發送端本地投遞狀態會先視為已送入傳輸層。 |
| **某位成員 direct / relay 與 `store` 都失敗** | 仍走既有重試邏輯。 |
| **`group_invite_notice`** | 這條仍是**單聊私訊**，走單聊離線 `store` 邏輯，不屬於本節「群組協定離線載荷」。 |
| **`group_sync_request` / `group_sync_response` / `group_delivery_ack`** | 目前**不**寫入群組離線 `store`；仍依賴在線 direct / relay。 |

---

### `GET /api/v1/groups/{group_id}`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：單一 **`GroupDetails`** 物件。

| 欄位 | 類型 | 說明 |
|------|------|------|
| `group` | object | **`Group`**，欄位同 **GET /api/v1/groups** 一節之 `Group` 表 |
| `members` | array | 元素為 **`GroupMember`**，見下表 |
| `recent_events` | array | 元素為 **`GroupEventView`**（可省略），見下表 |

**`GroupMember` 欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `group_id` | string | 群組 ID |
| `peer_id` | string | 成員 PeerID |
| `role` | string | `controller` / `admin` / `member` |
| `state` | string | 如 `invited`、`active`、`left`、`removed` |
| `invited_by` | string | 邀請人 PeerID |
| `joined_epoch` | number | 加入時 epoch |
| `left_epoch` | number | 離開時 epoch |
| `updated_at` | string | 更新時間（RFC3339） |

**`GroupEventView` 欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `event_id` | string | 事件 ID |
| `group_id` | string | 群組 ID |
| `event_seq` | number | 事件序號 |
| `event_type` | string | 事件類型字串 |
| `actor_peer_id` | string | 行為者 PeerID |
| `signer_peer_id` | string | 簽名者 PeerID |
| `payload_json` | string | 事件 payload（JSON 字串） |
| `created_at` | string | 建立時間（RFC3339） |
| `delivery_summary` | object | 見下表 |

**`delivery_summary`（`GroupEventDeliverySummary`）欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `total` | number | 總數 |
| `sent_to_transport` | number | 已送傳輸層筆數 |
| `queued_for_retry` | number | 佇列重試筆數 |
| `failed` | number | 失敗筆數 |

---

### `POST /api/v1/groups/{group_id}/{action}`

路徑為 **`/api/v1/groups/{group_id}/{action}`**。實作上對帶 JSON 的動作請用 **POST**（伺服器未對每個 action 強制 Method）。

| Action | 成功時回應物件 |
|--------|----------------|
| `invite` | **`Group`**（欄位同 **GET /api/v1/groups** 之 `Group` 表） |
| `join` | **`Group`** |
| `leave` | **`Group`** |
| `remove` | **`Group`** |
| `title` | **`Group`** |
| `retention` | **`Group`** |
| `dissolve` | **`Group`** |
| `controller` | **`Group`** |

**請求 body 摘要**：`invite`：`peer_id`、`role`、`invite_text`；`leave`/`dissolve`：可選 `reason`；`remove`：`peer_id`、`reason`；`title`：`title`；`retention`：`retention_minutes`；`controller`：`peer_id`。

**補充**：

- `join`、`leave` 在非控制者路徑下，實際會發送 `group_join_request` / `group_leave_request` 給控制者；若當次 direct / relay 失敗，會再嘗試寫入群組離線 `store`。
- `invite`、`remove`、`title`、`retention`、`dissolve`、`controller` 成功落庫後，對外成員廣播的是 `group_control`；若當次廣播 direct / relay 失敗，也會再嘗試寫入群組離線 `store`。

---

### `GET` / `POST /api/v1/groups/{group_id}/messages`

- **GET**：訊息列表。
- **POST**：`{"text":"..."}` 發送文字。

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**GET 回應欄位**：JSON **陣列**，元素為 **`GroupMessage`** 物件。

| 欄位 | 類型 | 說明 |
|------|------|------|
| `msg_id` | string | 訊息 ID |
| `group_id` | string | 群組 ID |
| `epoch` | number | epoch |
| `sender_peer_id` | string | 發送者 PeerID |
| `sender_seq` | number | 發送者序號 |
| `msg_type` | string | 如 `group_chat_text`、`group_chat_file` |
| `plaintext` | string | 解密後文字 |
| `file_name` | string | 檔名（可省略） |
| `mime_type` | string | MIME（可省略） |
| `file_size` | number | 大小（可省略） |
| `file_cid` | string | CID（可省略） |
| `signature` | string | Base64（可省略） |
| `state` | string | 群組訊息狀態（見 `GroupMessageState*`） |
| `delivery_summary` | object | **`GroupDeliverySummary`**（可省略），見下表 |
| `created_at` | string | 建立時間（RFC3339） |

**`GroupDeliverySummary` 欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `total` | number | 總收件者數 |
| `pending` | number | 待處理 |
| `sent_to_transport` | number | 已送傳輸層 |
| `queued_for_retry` | number | 佇列重試 |
| `delivered_remote` | number | 遠端已送達 |
| `failed` | number | 失敗 |

**POST 回應欄位**：單一 **`GroupMessage`** 物件，欄位同上表。

**補充**：

- `POST` 發送文字時，對每位目標成員會先嘗試 direct / relay；失敗時再嘗試將該筆 `group_chat_text` 寫入該成員離線 `store`。
- 這表示 **200 OK** 僅代表「本地已建立並開始投遞」，不代表所有成員都已即時在線收到；實際送達情況請看後續 `delivery_summary` / WebSocket 狀態事件。

---

### `POST /api/v1/groups/{group_id}/files`

`multipart/form-data`，欄位名 **`file`**。

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：單一 **`GroupMessage`** 物件，欄位同本節 **GET 陣列元素**之 `GroupMessage` 表。

**補充**：

- 檔案群訊息的離線兜底與文字相同：direct / relay 失敗時，會再嘗試把 `group_chat_file` 寫入收件者離線 `store`。

---

### `GET /api/v1/groups/{group_id}/messages/{msg_id}/file`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | 依訊息 MIME，預設 `application/octet-stream` |

**回應體**：檔案位元組（無 JSON）。

---

### `POST /api/v1/groups/{group_id}/messages/{msg_id}/revoke`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `ok` | boolean | 固定為 `true` |

---

### `POST /api/v1/groups/{group_id}/sync`

**請求 body**：`{"from_peer_id":"string"}`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `ok` | boolean | 固定為 `true` |
| `group_id` | string | 群組 ID |
| `from_peer_id` | string | 同步來源 PeerID |

---

## 網路與對端

### `GET /api/v1/chat/network/status`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `local_peer_id` | string | 本機 PeerID |
| `connected_peers` | number | 目前已連線 peer 數量 |
| `connected_relays` | array | 字串陣列，已連線 relay 的 PeerID（無 discovery 時可能仍出現空陣列） |

---

### `GET /api/v1/chat/peers/{peer_id}/status`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `peer_id` | string | 查詢的 PeerID |
| `connectedness` | string | libp2p 連線狀態字串（如 `Connected`、`NotConnected`） |
| `known_addrs` | array | 字串陣列，Peerstore 中已知 multiaddr |

---

### `POST /api/v1/chat/peers/{peer_id}/connect`

| 項目 | 說明 |
|------|------|
| 成功狀態碼 | **200 OK** |
| Content-Type | `application/json` |

**回應欄位**：

| 欄位 | 類型 | 說明 |
|------|------|------|
| `status` | string | 固定為 `connected` |

---

## WebSocket：即時事件

### 連線

- **URL**：`ws://{host}:{port}/api/v1/chat/ws`（HTTPS 則 `wss://`）。
- **方法**：**GET**（WebSocket 握手）。
- **伺服器**：僅**推送** JSON 事件；可讀取客戶端訊息以維持連線，**不解析業務指令**。

---

### 每則訊息的 JSON 欄位（`ChatEvent`）

每則為一個 **`ChatEvent`** 物件（與 `internal/chat/events.go` 一致）。

| 欄位 | 類型 | 說明 |
|------|------|------|
| `type` | string | `message` / `message_state` / `friend_request` / `conversation_deleted` / `contact_deleted` |
| `kind` | string | `direct`（單聊）或 `group`（群組） |
| `conversation_id` | string | 單聊為會話 ID；群組為 `group_id` |
| `msg_id` | string | 訊息 ID（可省略） |
| `msg_type` | string | 如 `chat_text`、`chat_file`、`group_chat_text`、`group_chat_file`、`retention_update`（可省略） |
| `request_id` | string | 好友請求 ID（可省略） |
| `from_peer_id` | string | （可省略） |
| `to_peer_id` | string | （可省略） |
| `state` | string | 好友請求狀態：`pending` / `accepted` / `rejected` 等（可省略） |
| `at_unix_millis` | number | 事件時間（Unix 毫秒） |
| `plaintext` | string | 文字（可省略） |
| `file_name` | string | 檔名（可省略） |
| `mime_type` | string | MIME（可省略） |
| `file_size` | number | 檔案大小（可省略） |
| `sender_peer_id` | string | （可省略） |
| `receiver_peer_id` | string | （可省略） |
| `direction` | string | 訊息方向（可省略） |
| `counter` | number | 單聊序號（可省略） |
| `transport_mode` | string | （可省略） |
| `message_state` | string | 訊息列狀態（可省略） |
| `created_at_unix_millis` | number | 訊息建立時間毫秒（可省略） |
| `delivered_at_unix_millis` | number | 送達時間毫秒（可省略） |
| `epoch` | number | 群組 epoch（可省略） |
| `sender_seq` | number | 群組發送序號（可省略） |
| `delivery_summary` | object | 群組 **`GroupDeliverySummary`**（可省略），子欄位見 **GET/POST …/groups/{group_id}/messages** 一節之 `GroupDeliverySummary` 表 |

### `type` 取值說明

| `type` | 說明 |
|--------|------|
| `message` | 新訊息或需刷新列表 |
| `message_state` | 訊息狀態更新 |
| `friend_request` | 好友請求狀態變化 |
| `conversation_deleted` | 本地會話刪除 |
| `contact_deleted` | 聯絡人刪除 |

### 注意

- 訂閱有緩衝；客戶端過慢時**可能丟棄事件**。

---

## 靜態聊天頁（非 REST）

- **`GET /chat`**：內建聊天前端（非 JSON API）。

若第三方僅需程式接入，請以本文 **HTTP + WebSocket** 為準。
