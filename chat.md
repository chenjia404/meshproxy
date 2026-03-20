# 《基于 meshproxy 现有去中心化代理网络的聊天/语音承载方案》

版本：V2.0  
定位：复用现有 meshproxy 网络层，不再设计一套平行的独立 P2P 聊天网络  
适用：当前 Go Core + libp2p Host + relay/relay+exit + peer exchange + 本地 API/控制台

---

## 1. 设计目标

本方案的目标不是“重新发明一套聊天网络”，而是让聊天能力直接兼容并复用当前 meshproxy 已经存在的去中心化代理系统。

当前项目已经具备：

* 基于 libp2p 的设备身份与 Peer ID
* bootstrap / gossip / peer exchange / 可选 DHT discovery
* relay 节点与 relay+exit 节点
* raw multihop e2e 隧道思路
* 本地 API / 控制台 / 错误记录 / 基础观测

因此聊天层应该建立在这些能力之上，而不是再做一套独立的 direct + relay + mailbox 基础设施。

### 1.1 V1 聊天目标

V1 先支持：

* 单聊文本消息
* 安装即用，继续使用现有 Peer ID 作为身份
* 优先直连，失败时走 relay 多跳转发
* 应用层端到端加密
* 请求箱（陌生人先请求，再进入正式会话）
* 本地消息落库、ACK、重试
* 预留语音信令层，但不实现媒体流

### 1.2 V1 非目标

V1 不做：

* 独立 mailbox 离线消息系统
* 群聊
* 文件传输
* 多设备同一用户 ID
* WebRTC 媒体层
* 与现有代理流量混用同一 SOCKS5 数据面

---

## 2. 与当前 meshproxy 的兼容原则

### 2.1 身份复用

继续复用现有身份模型：

* `libp2p identity key`
* `Peer ID`

也就是：

* 一台设备 = 一个身份
* 聊天系统的公开地址仍然是 `peer_id`

这样不需要引入钱包、手机号、中心化账号系统。

### 2.2 发现能力复用

聊天层不重新设计 discovery，而直接复用 meshproxy 已有发现路径：

* `bootstrap_peers`
* `p2p.nodisc=false` 时的 rendezvous DHT discovery
* gossip descriptor 广播
* peer exchange
* 本地缓存的 peer 地址 / relay 地址

说明：

* 如果部署环境用了 `-nodisc` 或 `p2p.nodisc: true`，聊天层也必须接受“只靠 bootstrap + gossip + peer exchange”的现实，不假设 DHT 一定可用。

### 2.3 中继能力复用

聊天层不再把 “relay” 理解成一个独立新组件，而是直接复用当前 meshproxy 的 relay 节点能力。

也就是说：

* `mode: relay` 节点可以承担聊天中继
* `mode: relay+exit` 节点同样可以承担聊天中继

但是：

* 聊天消息不应该走现有 SOCKS5 / exit 代理语义
* 聊天应使用独立协议 ID 和独立 handler

### 2.4 传输层与代理层隔离

聊天层应该复用底层 libp2p host / stream / relay path / e2e 加密模型，但不能直接复用：

* SOCKS5 入口
* `BEGIN_TCP / CONNECTED / DATA / END`
* exit 1081 直接出网代理

原因很简单：

* 代理层解决的是“把 TCP/UDP 送到公网目标”
* 聊天层解决的是“把应用消息送到另一个 peer”

这两套协议语义不同，只能共用网络底座，不能混用业务帧。

---

## 3. 新的聊天承载模型

为了兼容当前系统，聊天层采用两种传输模式：

### 3.1 Direct Chat Stream

如果目标 peer 可直连：

* 直接打开聊天协议 stream
* 不经过 relay

### 3.2 Relay E2E Chat Tunnel

如果目标 peer 不可直连：

* 通过一个或多个 relay 节点转发
* 中间 relay 不理解业务内容
* 只有发送端和接收端能解密

这部分设计应复用当前 `raw multihop e2e` 隧道的核心思想：

* path header
* hop_index
* client/server 握手
* data / close / ping / pong

但最终目标不再是 exit 的 `1081` SOCKS5，而是目标 peer 自己的聊天 handler。

### 3.3 不再把 mailbox 作为 V1 核心前提

旧文档里把 mailbox 作为第三层兜底，这是和当前系统不兼容的。

当前 meshproxy 里并没有：

* 稳定的 mailbox 服务
* 独立离线存储节点
* mailbox 拉取与 ACK 协议

所以在兼容当前系统的前提下，V1 应改成：

* 在线：direct 或 relay-e2e
* 不在线：本地 outbox 排队 + 周期重试

也就是说：

* 离线消息“暂存本地”
* 不立即依赖网络中的第三方 mailbox 节点

Mailbox 可以保留为未来扩展，但不能作为 V1 基础假设。

---

## 4. 聊天协议家族

建议新增独立协议族，不复用代理协议：

* `/meshproxy/chat/request/1.0.0`
* `/meshproxy/chat/msg/1.0.0`
* `/meshproxy/chat/ack/1.0.0`
* `/meshproxy/chat/presence/1.0.0`
* `/meshproxy/chat/relay-e2e/1.0.0`
* `/meshproxy/chat/voice-signal/1.0.0`（预留）

### 4.1 为什么不用一个协议承载全部

原因不是“不能”，而是拆开更利于演进：

* request / request box 逻辑独立
* 文本消息可以单独演进
* ACK 和 presence 更轻
* relay-e2e 是专门的中继承载协议
* 未来语音信令可以独立升级

### 4.2 direct 与 relay-e2e 的关系

建议：

* direct 模式下，`request/msg/ack/presence` 直接走点对点 stream
* relay 模式下，`request/msg/ack/presence` 被封装到 `/meshproxy/chat/relay-e2e/1.0.0` 的加密 payload 中

这样上层消息结构可以统一，底层 transport 根据 direct / relay 自动切换。

---

## 5. Direct 模式设计

### 5.1 Direct 使用条件

满足以下条件时优先直连：

* 本地 peerstore 中已有对方可拨地址
* `host.Connect()` 成功
* 打开聊天协议 stream 成功

### 5.2 Direct 不应强依赖 DHT

当前系统中 DHT discovery 在部分环境下并不稳定，尤其 Windows 上你已经遇到过 `go-libp2p-kad-dht` 崩溃问题。

因此 direct 模式的地址来源优先级应是：

1. 当前已有连接
2. peerstore 已知地址
3. gossip / peer exchange 学到的地址
4. 若 `nodisc=false` 再尝试 DHT

也就是说：

* 聊天系统必须在 `-nodisc` 场景下仍能工作

### 5.3 Direct 协议

Direct 模式下可以直接使用：

* `/meshproxy/chat/request/1.0.0`
* `/meshproxy/chat/msg/1.0.0`
* `/meshproxy/chat/ack/1.0.0`
* `/meshproxy/chat/presence/1.0.0`

这和普通 libp2p 应用一致，没有必要先绕一层 SOCKS5。

---

## 6. Relay E2E Chat Tunnel 设计

这是兼容当前 meshproxy 的关键部分。

### 6.1 设计原则

聊天中继必须满足：

* 中间 relay 只转发，不看消息明文
* 发送端与接收端之间应用层 E2EE
* 支持 1 跳或多跳 relay
* 不依赖 exit 节点的 SOCKS5 语义

### 6.2 推荐做法

基于当前已经实现的 `raw multihop e2e` 思路，新建聊天专用协议：

* `/meshproxy/chat/relay-e2e/1.0.0`

它应复用这些概念：

* `RouteHeader`
* `TunnelID`
* `Path`
* `HopIndex`
* `ClientHello`
* `ServerHello`
* `EncryptedFrame { data / close / ping / pong }`

但最终的最后一跳不再是 exit SOCKS5，而是目标 peer 的聊天接收器。

### 6.3 路由头建议

可直接沿用当前 raw tunnel 的 header 结构思路：

```json
{
  "version": 1,
  "tunnel_id": "uuid",
  "path": ["relayPeer1", "relayPeer2", "targetPeer"],
  "hop_index": 0,
  "target_exit": "targetPeer"
}
```

这里建议将字段名后续改得更通用，例如 `target_peer`，但为了兼容当前 `internal/tunnel` 结构，第一版可以先沿用已有字段，再在聊天层语义上解释为“最终目标 peer”。

### 6.4 relay 节点行为

relay 节点只做：

1. 读取 route header
2. 判断自己是不是中间 hop
3. 打开到下一跳的同协议 stream
4. 转发更新后的 header
5. 后续 `io.Copy`

relay 不做：

* 聊天消息解析
* 会话状态解析
* ACK 语义处理
* 明文加解密

### 6.5 接收端行为

最终目标 peer 收到 `relay-e2e` stream 后：

1. 读取 route header
2. 完成与发送端的 e2e 握手
3. 解密出聊天 payload
4. 将 payload 投递给 request / message / ack / presence 处理器

### 6.6 为什么这比旧的 direct/relay/mailbox 模型更适配当前项目

因为当前项目已经有：

* libp2p host
* relay 节点
* raw tunnel 路由头
* e2e 握手与 data frame 经验

聊天如果沿用这条思路，新增成本最小，也更符合你当前系统的稳定性优化方向。

---

## 7. 会话与端到端加密

### 7.1 不能只依赖 libp2p 连接加密

即使 direct 模式下 libp2p 自带 Noise/TLS，聊天层仍应保留应用层 E2EE。

原因：

* relay 模式下必须防中间节点窥探
* direct 与 relay 两种模式要统一安全模型
* 后续群聊、文件、语音信令都要复用

### 7.2 推荐密钥模型

建议在现有 identity 之外新增：

* `chat_sign_key`
* `chat_kex_key`

用途：

* `libp2p identity key`
  * 网络身份 / Peer ID
* `chat_sign_key`
  * 请求箱、控制消息签名
* `chat_kex_key`
  * 会话建立和消息加密

### 7.3 V1 可以先做的简化版本

如果想尽快落地，V1 可以先简化为：

* 仍然使用现有 libp2p identity 做身份绑定
* 会话密钥交换用单独的 X25519 临时密钥
* 消息层用 AEAD

也就是说：

* 身份绑定复用现有 Peer ID
* 消息密钥不直接复用 libp2p 传输层密钥

### 7.4 会话建立时机

请求箱被接受后，创建 conversation：

* `conversation_id`
* `peer_id`
* `session_state`
* `last_transport_mode`
* `send_counter`
* `recv_counter`

### 7.5 消息帧建议

聊天层 payload 可以定义为：

* `session_request`
* `session_accept`
* `chat_text`
* `delivery_ack`
* `read_ack`
* `presence_update`
* `typing`
* `voice_signal_*`（预留）

这些 message 在 direct 模式下直接发，在 relay 模式下放进 `relay-e2e` 加密帧中。

---

## 8. Request Box 机制

请求箱依然成立，而且应保留。

### 8.1 目的

防止知道 Peer ID 后直接骚扰。

### 8.2 基本规则

首次联系必须先发送：

* `session_request`

被接受之前：

* 不进入正式 conversation
* 不允许持续发送普通消息

### 8.3 传输路径

请求箱同样遵循：

* 优先 direct
* 不通时走 relay-e2e
* 都不可用则本地 outbox 排队

注意：

* V1 不做 mailbox，所以“离线请求”只是本地待发，不是网络中持久投递

### 8.4 请求状态

* `pending`
* `accepted`
* `rejected`
* `blocked`
* `expired`

---

## 9. 消息发送与接收

### 9.1 发送优先级

```text
已有 direct chat stream
-> direct
-> direct 不可用则尝试拨号
-> 仍失败则走 relay-e2e
-> relay 也不可用则写入本地 outbox
```

### 9.2 本地状态机

建议保留：

* `local_only`
* `sending`
* `sent_to_transport`
* `delivered_remote`
* `read_remote`
* `failed`
* `queued_for_retry`

注意：

* 这里不再强依赖 `queued_for_mailbox`

### 9.3 ACK

V1 至少实现：

* `delivery_ack`

可选：

* `read_ack`

ACK 路径应与原消息保持一致：

* direct 过来的，优先 direct 回
* relay 过来的，优先 relay 回

---

## 10. Presence 与在线状态

### 10.1 当前系统下的在线状态来源

当前 meshproxy 兼容的 presence 来源不应再依赖 mailbox，而应基于：

* 当前活跃 direct stream
* 当前活跃 relay-e2e stream
* 最近一次成功连接时间
* peerstore 中是否有可拨地址
* discovery store / peer exchange 中是否仍能看到该 peer

### 10.2 建议状态

* `online_direct`
* `online_relay`
* `reachable_recently`
* `offline`
* `unknown`

---

## 11. 存储模型（兼容当前系统）

建议继续使用本地 SQLite，但字段要贴近当前 transport 模型。

### 11.1 peers

* `peer_id`
* `nickname`
* `avatar`
* `notes`
* `first_seen_at`
* `last_seen_at`
* `trust_state`
* `blocked`

### 11.2 peer_addresses

* `peer_id`
* `multiaddr`
* `source`
* `last_success_at`
* `last_failure_at`
* `score`

### 11.3 requests

* `request_id`
* `from_peer_id`
* `to_peer_id`
* `state`
* `intro_text`
* `created_at`
* `updated_at`
* `last_transport_mode`

### 11.4 conversations

* `conversation_id`
* `peer_id`
* `state`
* `last_message_at`
* `last_transport_mode`
* `unread_count`
* `created_at`
* `updated_at`

### 11.5 messages

* `msg_id`
* `conversation_id`
* `sender_peer_id`
* `receiver_peer_id`
* `direction`
* `msg_type`
* `plaintext_preview`
* `ciphertext_blob`
* `transport_mode`
* `state`
* `created_at`
* `delivered_at`
* `read_at`

### 11.6 session_states

* `conversation_id`
* `root_key`
* `send_chain_key`
* `recv_chain_key`
* `send_counter`
* `recv_counter`
* `last_remote_msg_id`

### 11.7 outbox_jobs

替代 mailbox_jobs：

* `job_id`
* `peer_id`
* `msg_id`
* `status`
* `retry_count`
* `next_retry_at`
* `last_transport_attempt`

---

## 12. 语音预留

语音 V1 仍不实现媒体层，但保留信令层。

### 12.1 语音未来应复用同一网络底座

未来语音也应遵循：

* direct 优先
* relay-e2e 兜底
* 不依赖 SOCKS5 代理协议

### 12.2 预留消息

* `voice_invite`
* `voice_accept`
* `voice_reject`
* `voice_hangup`
* `voice_busy`
* `voice_transport_update`

这些消息可以直接作为 chat payload 的扩展类型存在。

---

## 13. 与当前代码结构的映射建议

建议不要另起一套全新 `core/` 目录，而是沿着当前项目结构扩展。

推荐新增：

```text
internal/chat/
  protocol/
  session/
  transport/
  requestbox/
  storage/
  presence/
  service/
```

### 13.1 可以直接复用的现有模块

* `internal/identity`
  * 继续生成 Peer ID 和基础身份
* `internal/p2p`
  * 继续复用 host / gossip / peer exchange / bootstrap
* `internal/discovery`
  * 继续复用 descriptor store
* `internal/tunnel`
  * 复用 route header / frame / handshake 思路
* `internal/api`
  * 继续挂本地 HTTP API

### 13.2 不建议复用的模块

不建议把聊天直接做在：

* `internal/client/socks5.go`
* `internal/exit/service.go` 的 SOCKS5 / `BEGIN_TCP`
* 现有 circuit 的代理语义 handler

原因：

* 聊天并不是公网出口代理
* 业务帧语义完全不同
* 勉强复用只会把协议边界搞乱

---

## 14. API 设计建议（兼容当前本地 API 风格）

可以在现有本地 API 上新增：

### 14.1 身份与联系人

* `GET /api/v1/chat/me`
* `POST /api/v1/chat/profile`
* `GET /api/v1/chat/contacts`
* `DELETE /api/v1/chat/contacts/{peer_id}` — 刪除本地聯絡人與雙方好友請求記錄，並刪除與該 peer 的會話與本地訊息（`peer_id` 路徑需 URL 編碼）
* `POST /api/v1/chat/contacts/{peer_id}/nickname`
* `POST /api/v1/chat/contacts/{peer_id}/block`

### 14.2 请求箱

* `GET /api/v1/chat/requests`
* `POST /api/v1/chat/requests/send`
* `POST /api/v1/chat/requests/{id}/accept`
* `POST /api/v1/chat/requests/{id}/reject`

### 14.3 会话与消息

* `GET /api/v1/chat/conversations`
* `DELETE /api/v1/chat/conversations/{conversation_id}` — 僅刪除本地該會話與其中訊息（不刪 `peers` 聯絡人列；`conversation_id` 路徑需 URL 編碼）
* `GET /api/v1/chat/conversations/{id}/messages`（無查詢參數時回傳訊息 JSON 陣列；帶 `limit` 和/或 `offset` 時回傳 `{ messages, total, limit, offset, has_more }`，`limit` 預設 100、範圍 1–500）
* `POST /api/v1/chat/conversations/{id}/messages/text`

### 14.4 网络与传输

* `GET /api/v1/chat/network/status`
* `GET /api/v1/chat/peers/{peer_id}/status`
* `POST /api/v1/chat/peers/{peer_id}/connect`

### 14.5 语音信令预留

* `POST /api/v1/chat/conversations/{id}/voice/invite`
* `POST /api/v1/chat/conversations/{id}/voice/accept`
* `POST /api/v1/chat/conversations/{id}/voice/reject`
* `POST /api/v1/chat/conversations/{id}/voice/hangup`

---

## 15. 实施顺序建议

为了兼容当前系统，建议按下面顺序落地：

### 阶段 1：文本 direct 聊天

先做：

* `request`
* `session_accept`
* `chat_text`
* `delivery_ack`

只用 direct stream。

### 阶段 2：relay-e2e 聊天

再加：

* `/meshproxy/chat/relay-e2e/1.0.0`
* route header
* e2e 握手
* relay 只转发

### 阶段 3：本地 outbox 与重试

再补：

* 本地排队
* 指数退避
* presence
* 最近连接模式记录

### 阶段 4：语音信令与控制台

最后再做：

* voice signal
* 聊天状态页
* 联系人/请求箱 UI

---

## 16. 结论

旧版 `chat.md` 的问题在于：

* 它默认聊天系统拥有自己完整的 `direct / relay / mailbox` 基础设施
* 这和当前 meshproxy 的真实能力不一致

兼容当前系统的正确思路应该是：

1. 复用现有 `Peer ID` 身份  
2. 复用现有 discovery / gossip / peer exchange / relay 节点  
3. direct 可用时直接聊天  
4. direct 不可用时走新的 `chat relay-e2e` 多跳中继  
5. 中间 relay 不看明文  
6. V1 不把 mailbox 作为必须前提，而改成本地 outbox + 重试

这样做的好处是：

* 与当前代码现实一致
* 不会再维护两套平行网络
* 能复用你已经做过的稳定性优化方向
* 后续文本、语音、文件、群聊都还能继续往上叠

---

## 17. 协议结构草案

这一节的目标不是最终定稿，而是给实现阶段一个明确的起点，避免“知道要做聊天，但不知道第一版帧长什么样”。

### 17.1 Direct 聊天协议建议

Direct 模式建议使用简单的 length-prefixed JSON frame。

可以定义统一外层：

```json
{
  "type": "chat_text",
  "conversation_id": "conv_xxx",
  "msg_id": "msg_xxx",
  "from_peer_id": "12D3KooW...",
  "to_peer_id": "12D3KooW...",
  "sent_at": 1770000000,
  "payload": { ... },
  "sig": "base64..."
}
```

### 17.2 Request Box 消息

#### `session_request`

```json
{
  "type": "session_request",
  "request_id": "req_xxx",
  "from_peer_id": "12D3KooW...",
  "to_peer_id": "12D3KooW...",
  "nickname": "Alice",
  "intro_text": "你好，我想加你为联系人",
  "chat_sign_pub": "base64...",
  "chat_kex_pub": "base64...",
  "sent_at": 1770000000,
  "sig": "base64..."
}
```

#### `session_accept`

```json
{
  "type": "session_accept",
  "request_id": "req_xxx",
  "conversation_id": "conv_xxx",
  "from_peer_id": "12D3KooW...",
  "to_peer_id": "12D3KooW...",
  "chat_sign_pub": "base64...",
  "chat_kex_pub": "base64...",
  "sent_at": 1770000001,
  "sig": "base64..."
}
```

### 17.3 文本消息

#### `chat_text`

```json
{
  "type": "chat_text",
  "conversation_id": "conv_xxx",
  "msg_id": "msg_xxx",
  "from_peer_id": "12D3KooW...",
  "to_peer_id": "12D3KooW...",
  "ciphertext": "base64...",
  "nonce": "base64...",
  "counter": 12,
  "sent_at": 1770000020
}
```

#### `delivery_ack`

```json
{
  "type": "delivery_ack",
  "conversation_id": "conv_xxx",
  "msg_id": "msg_xxx",
  "from_peer_id": "12D3KooW...",
  "to_peer_id": "12D3KooW...",
  "acked_at": 1770000022
}
```

### 17.4 Presence 消息

```json
{
  "type": "presence_update",
  "peer_id": "12D3KooW...",
  "state": "online_direct",
  "sent_at": 1770000030
}
```

### 17.5 relay-e2e 内层 payload 建议

在 `/meshproxy/chat/relay-e2e/1.0.0` 中，建议加密后的 `data` 明文仍然是统一的聊天 envelope。

也就是：

* direct 时：`ChatEnvelope` 直接发
* relay-e2e 时：`ChatEnvelope` 先加密，再放进 `EncryptedFrame{type:data}`

这样上层处理器不需要区分 direct / relay 两套业务结构。

---

## 18. 代码落点建议

为了让实现不发散，建议明确落到当前仓库已有目录结构。

### 18.1 新增目录

建议新增：

```text
internal/chat/
  types.go
  service.go
  direct.go
  relay_tunnel.go
  requestbox.go
  session.go
  store.go
  api.go
```

### 18.2 各文件职责建议

#### `internal/chat/types.go`

定义：

* `ChatEnvelope`
* `SessionRequest`
* `SessionAccept`
* `ChatText`
* `DeliveryAck`
* `PresenceUpdate`

#### `internal/chat/session.go`

负责：

* conversation 建立
* chat_kex
* 会话密钥派生
* send / recv counter

#### `internal/chat/direct.go`

负责：

* direct stream handler
* direct 发送
* direct 接收

#### `internal/chat/relay_tunnel.go`

负责：

* `/meshproxy/chat/relay-e2e/1.0.0`
* route header 适配
* relay-e2e 的 direct/relay 两端 glue 代码

这里建议尽量复用：

* `internal/tunnel/`

而不是再重新写一套 path + handshake。

#### `internal/chat/requestbox.go`

负责：

* 请求箱状态机
* 接受 / 拒绝 / 拉黑

#### `internal/chat/store.go`

负责：

* SQLite 读写
* conversations / messages / requests / outbox

#### `internal/chat/service.go`

负责：

* 对外统一接口
* transport 决策
* 发送重试
* presence

---

## 19. 与现有文件的接线建议

### 19.1 `internal/p2p/protocols.go`

需要新增：

* `ProtocolChatRequest`
* `ProtocolChatMsg`
* `ProtocolChatAck`
* `ProtocolChatPresence`
* `ProtocolChatRelayE2E`
* `ProtocolChatVoiceSignal`

### 19.2 `internal/app/app.go`

需要新增：

* 初始化 `chat.Service`
* 根据节点模式注册 chat stream handler
* 将 discovery / host / selector 注入 chat service
* 将 chat API 接到本地 HTTP API

### 19.3 `internal/api/local_api.go`

需要新增：

* `/api/v1/chat/*`

并继续沿用当前模式：

* 本地 HTTP API
* 同源控制台
* 不引入额外服务进程

### 19.4 控制台

后续可以在统一后的控制台里加：

* 联系人页
* 请求箱页
* 会话列表页
* 当前聊天 transport 状态

---

## 20. 第一阶段最小实现任务清单

下面这部分是最适合直接开工的内容。

### 20.1 第一阶段目标

先实现：

* 单聊文本
* direct 模式
* request box
* delivery_ack
* 本地 SQLite

不做：

* relay-e2e
* outbox 重试
* presence
* 控制台 UI

### 20.2 第一阶段具体任务

#### 任务 1：定义聊天协议类型

修改或新增：

* `internal/p2p/protocols.go`
* `internal/chat/types.go`

完成标准：

* 有 request / msg / ack 三个协议 ID
* 有统一 envelope

#### 任务 2：建立 chat service

新增：

* `internal/chat/service.go`

完成标准：

* 能注册 direct chat stream handler
* 能暴露 `SendText(peerID, text)` 入口

#### 任务 3：请求箱与会话落库

新增：

* `internal/chat/store.go`
* SQLite schema migration

完成标准：

* 能保存 request
* 能接受 request 后创建 conversation

#### 任务 4：direct 文本消息

新增：

* `internal/chat/direct.go`

完成标准：

* 发送方能通过 direct stream 发 `chat_text`
* 接收方能落库并回 `delivery_ack`

#### 任务 5：本地 API

修改：

* `internal/api/local_api.go`

完成标准：

* 前端能查看 requests / conversations / messages
* 前端能发 accept / reject / sendText

### 20.3 第一阶段完成判定

第一阶段完成后，应满足：

1. 两个在线节点已知彼此 `peer_id`
2. A 可以向 B 发 `session_request`
3. B 可以 accept
4. A/B 可以 direct 发文本
5. 消息本地落库
6. 接收后有 `delivery_ack`

如果这 6 点没齐，就不应该进入第二阶段 relay-e2e。

---

## 21. 第二阶段任务清单

第二阶段才接入 relay-e2e。

### 21.1 目标

在不能直连时仍可聊天。

### 21.2 具体任务

#### 任务 1：新增 `ProtocolChatRelayE2E`

在：

* `internal/p2p/protocols.go`

#### 任务 2：复用 `internal/tunnel`

尽量复用：

* `RouteHeader`
* `ClientHello`
* `ServerHello`
* `EncryptedFrame`

避免单独再发明一套中继加密结构。

#### 任务 3：relay 侧 chat handler

建议：

* 不直接改现有代理协议
* 新增聊天专用 relay handler

#### 任务 4：接收端 chat relay handler

最终 peer 负责：

* 解密
* 投递给 chat service

### 21.3 第二阶段完成判定

第二阶段完成后，应满足：

1. 两个不能直连的节点
2. 通过至少一个 relay 节点
3. 仍可完成 request / accept / text / ack
4. relay 看不到明文

---

## 22. 第三阶段任务清单

### 22.1 目标

补齐产品可用性，而不是只停留在“消息能通”。

### 22.2 具体任务

* outbox 重试
* presence
* 最近 direct / relay 模式展示
* 错误记录
* 聊天控制台页

### 22.3 这一阶段才值得做的 UI

因为没有 direct + relay + 重试这些基础能力前，UI 只是空壳。

建议到第三阶段再做：

* 会话列表
* 联系人页
* 请求箱页
* 当前 transport 状态徽标

---

## 23. 风险与取舍

### 23.1 最大风险

最大风险不是聊天协议本身，而是：

* direct 发现不稳定
* relay-e2e 在多跳下的边界条件
* 当前项目的 DHT 在部分平台上不稳定

所以实现时必须接受：

* `-nodisc` 场景要能工作
* relay 不能假设永远在线
* direct 不是永远优先成功

### 23.2 关键取舍

V1 为了兼容当前系统，必须做这些取舍：

* 先不要 mailbox
* 先不要群聊
* 先不要媒体层
* 先把 direct + relay-e2e 文本做稳

这是唯一现实的落地路径。

