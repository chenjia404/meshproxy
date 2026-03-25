# MeshChat 节点身份绑定功能设计文档（mesh-proxy 项目落地版）

## 1. 目标

为 `mesh-proxy` 的聊天模块增加“`PeerID` 绑定 Ethereum 地址”能力，用于：

1. 本机聊天资料中保存当前绑定关系。
2. 与已建立聊天关系的对端同步该绑定关系。
3. 允许 UI / 外部钱包完成 ETH 签名后，把完整绑定记录回写到节点。
4. 允许空投服务或其他业务服务基于双签名记录独立验签。

本版文档不是抽象协议稿，而是按当前 `mesh-proxy` 代码结构整理后的落地方案。

---

## 2. 先说结论

结合本项目现状，推荐这样落地：

1. 不新建独立的“peer profile”表。
2. 本机绑定信息落在聊天 SQLite 的 `profile` 表。
3. 远端绑定缓存落在聊天 SQLite 的 `peers` 表。
4. 不直接改现有 `ProfileSync` 结构体加 `binding` 字段。
5. 新增一个独立的 `BindingSync` 消息类型，在现有 `ProtocolChatRequest` 通道里同步绑定。
6. 本期不改 `requests` 表，不把绑定塞进好友请求 / 接受消息里，先控制改动范围。

这样做的原因是：当前项目并没有一个通用的“PeerProfile wire object”，实际的资料同步是 `internal/chat/types.go` 里的 `ProfileSync`，而它又参与了 `internal/chat/relay_sign.go` 的 canonical 重建。直接往 `ProfileSync` 里加字段，会让旧版本节点因为签名重建不一致而验签失败。

---

## 3. 项目现状映射

### 3.1 数据库和表在哪里

聊天数据库默认路径：

```text
{DataDir}/chat.db
```

创建位置：

- `internal/app/app.go`
- `chat.NewService(ctx, filepath.Join(cfg.DataDir, "chat.db"), ...)`

表结构和迁移逻辑在：

- `internal/chat/store.go`

当前与 profile 直接相关的表有两张：

1. `profile`
   - 本机聊天资料
   - 每个库通常只有一行，`peer_id` 为本机节点 ID
2. `peers`
   - 对端联系人缓存
   - 存储对端昵称、bio、头像、拉黑状态等

也就是说，本项目里的“profile”不是单独的账号系统表，而是聊天模块自己的本地 SQLite 状态。

### 3.2 当前代码里的 profile 入口

数据结构：

- `internal/chat/types.go`
  - `type Profile struct`
  - `type Contact struct`
  - `type ProfileSync struct`

服务层：

- `internal/chat/service.go`
  - `GetProfile()`
  - `UpdateProfile()`
  - `UpdateProfileAvatar()`
  - `ListContacts()`

HTTP API：

- `internal/api/local_api.go`
  - `GET /api/v1/chat/me`
  - `GET /api/v1/chat/profile`
  - `POST /api/v1/chat/profile`
  - `POST /api/v1/chat/profile/avatar`
  - `GET /api/v1/chat/contacts`

### 3.3 当前 profile 同步方式

当前项目里，聊天资料同步依赖：

- 消息类型：`MessageTypeProfileSync`
- 协议通道：`p2p.ProtocolChatRequest`
- 发送逻辑：`Service.maybeSyncProfile`
- 接收逻辑：`Service.handleProfileSync`
- relay 签名 canonical：`marshalProfileSyncForRelaySigning`

现有 `ProfileSync` 只包含：

- `nickname`
- `bio`
- `avatar_name`

所以本期不能把“抽象设计稿中的 `PeerProfile.binding`”原样照搬到 wire 层。

---

## 4. 本期范围

### 4.1 要做

1. 本机保存绑定记录。
2. 提供创建绑定草稿、提交 ETH 签名、清除本地绑定的本地 API。
3. 已建立会话的节点之间同步绑定记录。
4. 接收端保存远端绑定缓存，并记录校验结果。
5. `GET /api/v1/chat/me`、`GET /api/v1/chat/profile`、`GET /api/v1/chat/contacts` 可以带出绑定信息。

### 4.2 本期不做

1. 节点内置 ETH 钱包。
2. 链上余额、质押、空投资格校验。
3. 多地址绑定。
4. 正式的协议级解绑动作。
5. 在 `SessionRequest` / `SessionAccept` 里携带 binding。
6. DHT 全网广播绑定状态。
7. UI 实现细节。

---

## 5. 数据结构设计

## 5.1 BindingPayload

```go
type BindingPayload struct {
	Version    int    `json:"version"`
	Action     string `json:"action"`
	PeerID     string `json:"peer_id"`
	EthAddress string `json:"eth_address"`
	ChainID    uint64 `json:"chain_id"`
	Domain     string `json:"domain"`
	Nonce      string `json:"nonce"`
	Seq        uint64 `json:"seq"`
	IssuedAt   int64  `json:"issued_at"`
	ExpireAt   int64  `json:"expire_at"`
}
```

字段约束：

- `version` 固定为 `1`
- `action` 固定为 `bind_eth_address`
- `domain` 固定为 `meshchat`
- `eth_address` 入库前统一转小写
- `nonce` 建议为 16 字节随机数的 hex 字符串
- `seq` 为本机单调递增版本号

### 关于过期时间

原始草稿里示例是几分钟级别，但对本项目不合适，因为：

1. 绑定记录会作为资料长期缓存。
2. 本期又不做协议级解绑。

因此建议默认：

- `expire_at = issued_at + 30 天`

并允许 API 指定更短 TTL，但应限制最大值，例如：

- 最大 `90 天`

---

## 5.2 BindingRecord

```go
type SignatureEnvelope struct {
	Algo  string `json:"algo"`
	Value string `json:"value"`
}

type BindingRecord struct {
	Payload    BindingPayload `json:"payload"`
	Signatures struct {
		Peer     SignatureEnvelope `json:"peer"`
		Ethereum SignatureEnvelope `json:"ethereum"`
	} `json:"signatures"`
}
```

约定：

- `signatures.peer.algo = "libp2p"`
- `signatures.ethereum.algo = "eip191"`

---

## 5.3 BindingDraft

用于 UI 调钱包签名。

```go
type BindingDraft struct {
	Payload          BindingPayload `json:"payload"`
	CanonicalMessage string         `json:"canonical_message"`
	PeerSignature    string         `json:"peer_signature"`
}
```

---

## 5.4 API 输出结构扩展

当前项目不是 `PeerProfile` 结构，而是 `Profile` / `Contact`。

建议改为：

```go
type Profile struct {
	PeerID           string         `json:"peer_id"`
	Nickname         string         `json:"nickname"`
	Bio              string         `json:"bio"`
	Avatar           string         `json:"avatar"`
	AvatarCID        string         `json:"avatar_cid,omitempty"`
	ChatKexPub       string         `json:"chat_kex_pub"`
	CreatedAt        time.Time      `json:"created_at"`
	Binding          *BindingRecord `json:"binding,omitempty"`
	BindingUpdatedAt time.Time      `json:"binding_updated_at,omitempty"`
}

type Contact struct {
	PeerID             string         `json:"peer_id"`
	Nickname           string         `json:"nickname"`
	Bio                string         `json:"bio"`
	Avatar             string         `json:"avatar"`
	CID                string         `json:"cid,omitempty"`
	RemoteNickname     string         `json:"remote_nickname,omitempty"`
	RetentionMinutes   int            `json:"retention_minutes"`
	Blocked            bool           `json:"blocked"`
	LastSeenAt         time.Time      `json:"last_seen_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	Binding            *BindingRecord `json:"binding,omitempty"`
	BindingStatus      string         `json:"binding_status,omitempty"`
	BindingValidatedAt time.Time      `json:"binding_validated_at,omitempty"`
	BindingError       string         `json:"binding_error,omitempty"`
}
```

说明：

- 本机 `Profile` 直接返回当前 binding record。
- 远端 `Contact` 返回缓存的 binding record 和校验状态。
- `Request` 本期不扩展。

---

## 5.5 新增 BindingSync 消息

### 为什么要新增消息，而不是改 ProfileSync

因为当前 `ProfileSync` 会参与 relay canonical 签名重建，直接加字段会破坏旧版本兼容性。

所以推荐新增一个独立消息：

```go
const MessageTypeBindingSync = "binding_sync"

type BindingSync struct {
	Type       string         `json:"type"`
	FromPeerID string         `json:"from_peer_id"`
	ToPeerID   string         `json:"to_peer_id"`
	Binding    *BindingRecord `json:"binding,omitempty"`
	SentAtUnix int64          `json:"sent_at_unix"`
	Signature  []byte         `json:"signature,omitempty"`
}
```

说明：

1. 走现有 `p2p.ProtocolChatRequest` 通道。
2. 老版本节点收到未知 `type` 会忽略，不影响核心聊天。
3. 当前发送路径仍建议沿用 `connected-only`，不要把 binding 同步失败当成聊天失败。
4. `Signature` 字段建议保留，便于以后需要 relay 时接入现有 `relay_sign.go` 习惯用法。

---

## 6. 数据库存储设计

## 6.1 `profile` 表要加哪些字段

当前表在 `internal/chat/store.go` 中创建：

```sql
CREATE TABLE IF NOT EXISTS profile (
    peer_id TEXT PRIMARY KEY,
    nickname TEXT NOT NULL,
    bio TEXT NOT NULL DEFAULT '',
    avatar TEXT NOT NULL DEFAULT '',
    chat_kex_priv TEXT NOT NULL,
    chat_kex_pub TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
```

建议新增：

| 字段 | 类型 | 说明 |
|------|------|------|
| `binding_seq` | INTEGER NOT NULL DEFAULT `0` | 本机最后一次成功保存的 binding 序号 |
| `binding_record_json` | TEXT NOT NULL DEFAULT `''` | 完整 `BindingRecord` JSON |
| `binding_eth_address` | TEXT NOT NULL DEFAULT `''` | 冗余保存当前绑定地址，便于列表展示和查询 |
| `binding_chain_id` | INTEGER NOT NULL DEFAULT `0` | 当前绑定的链 ID |
| `binding_expire_at` | INTEGER NOT NULL DEFAULT `0` | 当前绑定的 payload 过期时间，Unix 秒 |
| `binding_updated_at` | TEXT NOT NULL DEFAULT `''` | 本机最后一次写入 binding 的时间，RFC3339Nano |

说明：

1. `binding_record_json` 为空字符串表示当前无绑定。
2. `binding_seq` 不能在清除本地绑定时重置，否则会破坏“序号单调递增”。
3. 本机下一次创建 draft 时使用 `binding_seq + 1`。

### 本机为什么不单独建新表

因为当前聊天模块的设计就是“本机当前状态放 `profile` 一行里”，头像 CID 也是直接加列处理，沿用现有模式最省改动。

---

## 6.2 `peers` 表要加哪些字段

当前表在 `internal/chat/store.go` 中创建：

```sql
CREATE TABLE IF NOT EXISTS peers (
    peer_id TEXT PRIMARY KEY,
    nickname TEXT NOT NULL,
    bio TEXT NOT NULL DEFAULT '',
    avatar TEXT NOT NULL DEFAULT '',
    blocked INTEGER NOT NULL DEFAULT 0,
    last_seen_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
);
```

建议新增：

| 字段 | 类型 | 说明 |
|------|------|------|
| `binding_record_json` | TEXT NOT NULL DEFAULT `''` | 对端完整 `BindingRecord` JSON |
| `binding_eth_address` | TEXT NOT NULL DEFAULT `''` | 对端当前声明绑定的地址 |
| `binding_chain_id` | INTEGER NOT NULL DEFAULT `0` | 对端绑定链 ID |
| `binding_seq` | INTEGER NOT NULL DEFAULT `0` | 最近一次接受的绑定序号 |
| `binding_expire_at` | INTEGER NOT NULL DEFAULT `0` | 对端 binding 过期时间，Unix 秒 |
| `binding_status` | TEXT NOT NULL DEFAULT `''` | `valid` / `expired` / `peer_sig_invalid` / `eth_sig_invalid` / `parse_error` / `stale` |
| `binding_validated_at` | TEXT NOT NULL DEFAULT `''` | 最近一次校验时间 |
| `binding_updated_at` | TEXT NOT NULL DEFAULT `''` | 最近一次成功写入 binding 缓存时间 |
| `binding_error` | TEXT NOT NULL DEFAULT `''` | 最近一次校验失败原因，便于调试和 UI 展示 |

### 为什么远端也放 `peers` 表

因为当前联系人昵称、头像、bio 的缓存就已经在 `peers` 表里，绑定信息本质上也是对端资料缓存的一部分，放同一张表最符合现有结构。

---

## 6.3 `requests` 表本期不改

原因：

1. 当前好友请求链路已经比较复杂。
2. `SessionRequest` / `SessionAccept` 也有自己的签名 canonical。
3. 把 binding 塞进去会扩大协议和数据库改动面。

所以本期只在：

- 本机 profile API
- 已建立会话后的 `BindingSync`
- 联系人缓存

这三条线上实现。

---

## 7. 迁移与 Store 层改动

建议在 `internal/chat/store.go` 中新增迁移函数：

```go
func (s *Store) ensureProfileBindingColumns() error
func (s *Store) ensurePeerBindingColumns() error
```

并在 `NewStore()` 里接到现有这些迁移之后：

- `ensureProfileAvatarCIDColumn()`
- `ensurePeerAvatarCIDColumn()`

再继续执行新的 binding 列检查。

### 建议新增的 Store 方法

```go
func (s *Store) NextBindingSeq(localPeerID string) (uint64, error)
func (s *Store) SaveLocalBinding(localPeerID string, record BindingRecord) (Profile, error)
func (s *Store) ClearLocalBinding(localPeerID string) (Profile, error)
func (s *Store) SavePeerBinding(peerID string, record BindingRecord, status, errMsg string, validatedAt time.Time) error
func (s *Store) ClearPeerBinding(peerID string) error
```

实现建议：

1. `SaveLocalBinding` 负责同时更新：
   - `binding_seq`
   - `binding_record_json`
   - `binding_eth_address`
   - `binding_chain_id`
   - `binding_expire_at`
   - `binding_updated_at`
2. `ClearLocalBinding` 只清空当前 record 相关列，不清空 `binding_seq`。
3. `SavePeerBinding` 要在入库前做序号比较。

### 远端序号处理规则

假设本地已缓存 `peers.binding_seq = 8`：

1. 收到 `seq = 7`
   - 直接丢弃
   - 可记录 `binding_status = stale`
2. 收到 `seq = 8`
   - 若 record 内容完全相同，可仅刷新 `binding_validated_at`
   - 若内容不同，记录错误并丢弃
3. 收到 `seq = 9`
   - 按正常新记录处理

---

## 8. 服务层设计

实现位置建议放在：

- `internal/chat/service.go`
- 可新建 `internal/chat/binding.go`

### 建议新增的 Service 方法

```go
func (s *Service) CreateBindingDraft(ethAddress string, chainID uint64, ttlSeconds int64) (BindingDraft, error)
func (s *Service) FinalizeBindingRecord(payload BindingPayload, peerSignature, ethSignature string) (Profile, error)
func (s *Service) ClearLocalBinding() (Profile, error)
func (s *Service) maybeSyncBinding(peerID string)
func (s *Service) handleBindingSync(msg BindingSync) error
func (s *Service) validateBindingRecord(record BindingRecord, expectedPeerID string) (status string, err error)
```

### 行为要求

#### `CreateBindingDraft`

1. 读取本机 `PeerID`
2. 读取 `profile.binding_seq`
3. 生成 `seq = binding_seq + 1`
4. 规范化 ETH 地址为小写
5. 生成 canonical message
6. 使用本机 libp2p 私钥生成 `peer_signature`
7. 返回 `BindingDraft`

#### `FinalizeBindingRecord`

1. 校验 `payload.peer_id == localPeerID`
2. 重新生成 canonical message
3. 校验 `peerSignature` 是否能被本机 `PeerID` 验证通过
4. 校验 `payload.seq == 当前 binding_seq + 1`
5. 基础校验 `ethSignature` 非空
6. 可选：本地做一次 EIP-191 恢复地址校验
7. 写入 `profile`
8. 向已建立会话的对端异步触发 `maybeSyncBinding`

#### `ClearLocalBinding`

1. 清空 `profile.binding_record_json` 等当前绑定字段
2. 保留 `binding_seq`
3. 本期不做正式 unbind 广播

---

## 9. 签名规范

## 9.1 binding 内部 canonical message

仍建议使用原方案：

```text
MeshChat Binding v1
action: {action}
peer_id: {peer_id}
eth_address: {eth_address}
chain_id: {chain_id}
domain: {domain}
nonce: {nonce}
seq: {seq}
issued_at: {issued_at}
expire_at: {expire_at}
```

要求：

1. 第一行固定为 `MeshChat Binding v1`
2. 换行固定 `\n`
3. 字段顺序固定
4. 验签时必须根据 `payload` 本地重建

## 9.2 BindingSync 外层签名

如果本期要把 `BindingSync` 也纳入现有 relay 签名体系，则需要在：

- `internal/chat/relay_sign.go`

新增：

```go
func marshalBindingSyncForRelaySigning(m BindingSync) ([]byte, error)
func (s *Service) signRelayBindingSync(m BindingSync) (BindingSync, error)
```

注意：

1. `binding` 是嵌套对象，不能依赖 map 序列化顺序。
2. 最稳妥的方式是先把 `BindingRecord` 组织成固定字段顺序的匿名 struct，再 `json.Marshal`。
3. 如果本期仍然只走 `sendEnvelopeConnectedOnly`，则外层 relay 签名可以先留接口，不必马上启用发送。

---

## 10. P2P 同步设计

## 10.1 发送时机

建议在以下时机异步触发 `maybeSyncBinding(peerID)`：

1. 本机完成 `FinalizeBindingRecord` 后
2. 已建立会话的 peer 重新连接后
3. `maybeSyncProfile(peerID)` 成功后
4. 节点启动后发现已有活跃会话时

### 发送前置条件

1. `peerID` 非空，且不等于本机
2. 与对端有活跃 conversation
3. 对端支持 `p2p.ProtocolChatRequest`
4. 本机当前存在 `binding_record_json`

## 10.2 接收逻辑

`handleBindingSync` 建议做：

1. 校验 `ToPeerID` 为空或等于本机
2. 校验 `FromPeerID` 非空且不等于本机
3. 校验该 peer 已存在 conversation
4. 校验 `msg.Binding != nil`
5. 调 `validateBindingRecord(record, msg.FromPeerID)`
6. 按 `seq` 规则决定是否写入 `peers`

## 10.3 为什么不复用 handleProfileSync

因为当前 `ProfileSync` 已经承担昵称、bio、头像同步，直接把 binding 合并进去会带来：

1. 旧版本兼容问题
2. `marshalProfileSyncForRelaySigning` 需要同步改
3. `peers` 资料更新和 binding 校验逻辑耦合过深

拆成 `BindingSync` 后更容易控制失败范围。

---

## 11. 本地 HTTP API 设计

改动位置：

- `internal/api/local_api.go`
- `ChatProvider` interface

## 11.1 现有接口返回结构增加 binding

以下接口返回的 `Profile` / `Contact` 结构应自动带出新增字段：

1. `GET /api/v1/chat/me`
2. `GET /api/v1/chat/profile`
3. `POST /api/v1/chat/profile`
4. `POST /api/v1/chat/profile/avatar`
5. `GET /api/v1/chat/contacts`

## 11.2 新增接口一：创建绑定草稿

```text
POST /api/v1/chat/profile/binding/draft
```

请求：

```json
{
  "eth_address": "0xabc...",
  "chain_id": 1,
  "ttl_seconds": 2592000
}
```

返回：

```json
{
  "payload": { ... },
  "canonical_message": "MeshChat Binding v1\n...",
  "peer_signature": "base64..."
}
```

## 11.3 新增接口二：提交最终 binding

```text
POST /api/v1/chat/profile/binding
```

请求：

```json
{
  "payload": { ... },
  "peer_signature": "base64...",
  "eth_signature": "0x..."
}
```

返回：

- 更新后的 `Profile`

## 11.4 新增接口三：清除本地 binding

```text
DELETE /api/v1/chat/profile/binding
```

返回：

```json
{
  "ok": true
}
```

### 为什么不单独加 GET /binding

因为：

1. `GET /api/v1/chat/profile`
2. `GET /api/v1/chat/me`

本来就会返回完整 profile，没有必要再拆一个只读接口。

---

## 12. ChatProvider 接口建议扩展

当前接口在 `internal/api/local_api.go`：

```go
type ChatProvider interface { ... }
```

建议新增：

```go
CreateBindingDraft(ethAddress string, chainID uint64, ttlSeconds int64) (chat.BindingDraft, error)
FinalizeBindingRecord(payload chat.BindingPayload, peerSignature, ethSignature string) (chat.Profile, error)
ClearLocalBinding() (chat.Profile, error)
```

---

## 13. 远端验证规则

## 13.1 最低要求

接收到远端 `BindingRecord` 后，至少做：

1. `record.payload.peer_id == fromPeerID`
2. `record.payload.version == 1`
3. `record.payload.action == "bind_eth_address"`
4. `record.payload.domain == "meshchat"`
5. `record.signatures.peer.algo == "libp2p"`
6. `record.signatures.ethereum.algo == "eip191"`
7. `peer` 签名可被 `fromPeerID` 验证通过

## 13.2 建议增加

1. `eth_address` 非空且格式合法
2. `chain_id > 0`
3. `expire_at > now`
4. 可选做一次 ETH 地址恢复校验

### 关于 ETH 验签

本项目当前没有 `go-ethereum` 相关直接依赖，因此建议分两档：

1. `v1 最小实现`
   - 本地只做 `peer` 签名强校验
   - `eth_signature` 只做格式与非空检查
   - 最终以业务服务端验签为准
2. `v1.1 可选增强`
   - 引入 EIP-191 地址恢复校验
   - 把 `binding_status` 区分为 `valid` / `eth_sig_invalid`

如果要控制本期复杂度，建议先按第 1 档实现。

---

## 14. 与现有消息的关系

本期建议：

1. `ProfileSync` 不改字段。
2. `SessionRequest` 不加 binding。
3. `SessionAccept` 不加 binding。
4. `requests` 表不加 binding 列。

这样可以保证：

1. 核心聊天握手不受影响
2. 老节点仍可建立会话
3. 新节点之间可以额外同步 binding

---

## 15. 需要改动的主要文件

### 必改

1. `internal/chat/types.go`
   - 新增 `BindingPayload`
   - 新增 `BindingRecord`
   - 新增 `BindingDraft`
   - 新增 `BindingSync`
   - 扩展 `Profile`
   - 扩展 `Contact`
2. `internal/chat/store.go`
   - 增加 `profile` / `peers` 的 binding 列迁移
   - 查询 / 更新逻辑支持 binding
3. `internal/chat/service.go`
   - 新增 draft / finalize / clear / sync / validate 逻辑
   - 新增 `BindingSync` 收发处理
4. `internal/api/local_api.go`
   - 扩展 `ChatProvider`
   - 增加三个 binding API 路由与 handler

### 视实现方式可选改

5. `internal/chat/relay_sign.go`
   - 若 `BindingSync` 也接入 relay 外层签名，则补 canonical
6. `chat-api.md`
   - 实现后补充 HTTP API 文档
7. `聊天数据库数据字典.md`
   - 实现后补充新增字段说明

---

## 16. 建议的实现顺序

1. 先补 `types.go` 和 `store.go`
2. 让 `GET /api/v1/chat/me` 能返回本机 binding
3. 再做 `CreateBindingDraft` / `FinalizeBindingRecord` / `ClearLocalBinding`
4. 最后做 `BindingSync` 的 P2P 收发

这样好处是：

1. 每一步都能单独验证
2. 先打通本地 API，再接远端同步
3. 不会一上来就同时改数据库、HTTP、P2P 三层

---

## 17. 建议测试项

至少补以下测试：

1. `binding` canonical message 生成测试
2. `peer` 签名生成与验签测试
3. `profile` 表迁移测试
4. `peers` 表迁移测试
5. `CreateBindingDraft` 序号递增测试
6. `FinalizeBindingRecord` 拒绝过期 / 错序号 payload 的测试
7. `BindingSync` 收到旧序号时丢弃的测试
8. `GET /api/v1/chat/profile` 返回 binding 的 handler 测试

---

## 18. 一个符合本项目的完整流程

### 18.1 本机绑定

1. UI 调 `POST /api/v1/chat/profile/binding/draft`
2. 节点返回：
   - `payload`
   - `canonical_message`
   - `peer_signature`
3. UI 用外部钱包做 `personal_sign`
4. UI 调 `POST /api/v1/chat/profile/binding`
5. 节点保存到 `profile`
6. 节点异步向已建立会话的 peer 发 `BindingSync`

### 18.2 对端接收

1. 收到 `BindingSync`
2. 本地校验 `peer` 签名和基础字段
3. 把结果写入 `peers`
4. `GET /api/v1/chat/contacts` 能看到该 peer 的 binding 信息

### 18.3 业务服务使用

1. 客户端从本地 API 读出 `Profile.binding`
2. 提交给空投服务
3. 空投服务独立验证双签名

---

## 19. 前端备注

本期文档只覆盖后端和协议设计，但如果后续要补 UI：

1. 聊天前端源码不在本仓库内，而在：
   - `E:\\code\\project-im-ui\\react-chat`
2. `web/chat/index.html` 只是编译产物
3. 控制台模板在：
   - `web/console/index.html`
4. 如果有控制台 HTML 改动，需要同步复制到：
   - `internal/api/console/index.html`

当前 binding 功能更适合先走聊天前端源码仓库接 API，而不是直接手改编译后的 `web/chat/index.html`。

---

## 20. 最终建议

如果只看“本项目最合适的最小实现”，建议以这组决策为准：

1. 本机绑定状态放 `profile`
2. 远端绑定缓存放 `peers`
3. 新增 `BindingSync`，不要直接改 `ProfileSync`
4. 本期只新增 3 个本地 HTTP API
5. 本期不碰 `requests` / `SessionRequest` / `SessionAccept`

这样改动最小，兼容性风险也最低。
