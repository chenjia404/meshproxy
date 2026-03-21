## 消息确认

我已经把这个机制加进去：**`SessionAccept` 被对方成功处理后，会回一个确认封包 `SessionAcceptAck`**；发送方收到后会**停止**之前为 `SessionAccept` 等待确认而存在的重试任务，这样就能更确定“双方都已建立 conversation/session”。

具体实现点（都在 `internal/chat`）：

- 新增回执协议类型
  - `MessageTypeSessionAcceptAck = "session_accept_ack"`
  - `type SessionAcceptAck struct { request_id / conversation_id / from_peer_id / to_peer_id ... }`
  - `protocolForEnvelope()` 里把它也归到 `ProtocolChatRequest`

- 接收方发送回执
  - 在 `processEnvelopeBytes` 里处理到 `MessageTypeSessionAccept` 且**成功更新为 accepted / 创建 conversation**后，会向 `accept.FromPeerID` 发送：
    - `SessionAcceptAck{ RequestID: ..., ConversationID: ..., ToPeerID: accept.FromPeerID }`

- 发送方收到回执后停止重试
  - 在 `processEnvelopeBytes` 新增 case `MessageTypeSessionAcceptAck`：
    - 仅当 `ack.ToPeerID == s.localPeer` 时
    - 调用 `store.DeleteFriendRequestJob(ack.RequestID)`，终止等待确认的重试

- 调整“等待确认”的重试语义
  - `AcceptRequest()`：即使 `sendEnvelope(SessionAccept)` 成功了，也会调用 `scheduleFriendRequestRetry(...)` 创建“等待 ack”的 job（用于兜底）
  - `retryFriendRequestJob()`：在 `RequestStateAccepted` 分支中，**send 成功不再直接删除 job**，而是继续保留并重排等待（直到收到 `SessionAcceptAck` 才删除）


根因基本找到了，在直连好友控制流和 relay 控制流处理不一致。

直连入口 service.go (line 1204) 之前收到 SessionAccept 时，如果这个 peer 已经有旧 conversation，只会把请求标成 accepted，但不会像 relay 分支那样真正恢复好友关系。缺的主要是这几步：

* 把旧 conversation 恢复成 active
* 补建 session_states
* 发送 SessionAcceptAck
* 通知前端好友关系已建立
而发消息和收消息都依赖 session_states，见 service.go (line 950) 和 service.go (line 2018)。所以就会出现“看起来加好友成功了，但实际上还是发不出去”的状态，尤其容易出现在这个 peer 之前有旧会话、被拒绝过、或本地残留脏数据的场景。

我已经把这块统一了：直连和 relay 现在都会走同一套共享处理，见 service.go (line 1272)、service.go (line 1312)、service.go (line 1823)。这样旧会话会被重新激活，session_states 会补齐，AcceptAck 也能正常回收重试任务。




## 中繼（relay）控制面簽名

- 經中繼隧道轉發的私聊控制/訊息封包（`sendViaRelay`）在發送前會用 **本地 libp2p 身份私鑰**對「不含 signature 的規範 JSON」簽名，並寫入 `signature`；類型前綴為 `mesh-proxy/chat/relay/v1:<type>\n` 以防跨類型重放。
- 中繼入口 `processEnvelopeBytes` 在處理非群組封包前會用 **聲稱發件人（通常為 `from_peer_id`）**的公鑰驗簽；群組封包仍沿用既有群組簽名，不重複加 relay 簽。
- 直連流不經此驗簽（仍以 RemotePeer 綁定身份）；**升級後舊節點若只發無簽名中繼封包，會被對端拒收**。

## 私聊 E2EE HKDF 與 hop 分離

- 私聊會話金鑰改由 **`protocol.DeriveChatSessionKeys`** 派生，HKDF info 為 `mesh-proxy/chat/e2ee/v1/hkdf-0` / `hkdf-1`，**不再**使用 `meshproxy-hop-fwd` / `meshproxy-hop-bwd`。
- **相容性**：已用舊 HKDF 寫入 `session_states` 的節點，升級後與對端金鑰不一致，無法解密舊訊息；需刪除該會話並重新走好友/接受流程以重建 session，或清空相關本地 DB。