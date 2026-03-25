# mesh-proxy 公开频道 API 接入文档

本文档描述 `mesh-proxy` 当前已经实现的**去中心化公开频道 v1** 本地 HTTP API，供前端应用、桌面端、移动端或其它本地 app 接入使用。

这份文档面向的是 **Local API** 使用方，不要求调用方直接处理 libp2p stream / PubSub / DHT。

## 1. 基本说明

- 基底地址：由配置 `api.listen` 决定，默认通常是 `http://127.0.0.1:19080`
- 路由前缀：`/api/v1/public-channels`
- 编码：JSON 使用 `UTF-8`
- CORS：已开启，浏览器可直接调用
- 认证：当前 Local API 没有额外 API Key，建议只暴露在本机或受信任网络

如果节点未启用公开频道服务，接口会返回：

- `404 Not Found`
- body 可能是纯文本：`public channel service not available`

## 2. 共通约定

### 2.1 错误返回

当前大多数错误返回为：

- HTTP `4xx/5xx`
- body 为纯文本错误信息

成功返回通常为：

- HTTP `200 OK`
- `Content-Type: application/json`

### 2.2 时间字段

公开频道内部时间字段统一是：

- Unix 秒级时间戳

例如：

```json
{
  "created_at": 1710010000,
  "updated_at": 1710010100
}
```

### 2.3 标识字段

- `channel_id`：UUIDv7 字符串
- `message_id`：频道内从 `1` 开始递增
- `seq`：频道级增量同步游标
- `version`：单条消息版本号

## 3. 消息类型

当前实现的公开频道 `message_type` 为：

- `text`
- `image`
- `video`
- `audio`
- `file`
- `system`
- `deleted`

说明：

- `text`：纯文本消息
- `image` / `video` / `audio` / `file`：带附件消息
- `system`：无附件系统文本消息
- `deleted`：墓碑消息

如果消息带附件但没有显式传 `message_type`，服务端会按附件 `mime_type` 自动识别：

- `image/*` => `image`
- `video/*` => `video`
- `audio/*` => `audio`
- 其它 => `file`

## 4. 主要数据结构

### 4.1 ChannelSummary

```json
{
  "profile": {
    "channel_id": "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
    "owner_peer_id": "12D3KooW...",
    "owner_version": 1,
    "name": "公开频道",
    "avatar": {
      "file_name": "avatar.png",
      "mime_type": "image/png",
      "size": 12345,
      "sha256": "abc...",
      "blob_id": "bafy...",
      "url": "/ipfs/bafy.../avatar.png"
    },
    "bio": "频道简介",
    "profile_version": 2,
    "created_at": 1710010000,
    "updated_at": 1710010100,
    "signature": "base64..."
  },
  "head": {
    "channel_id": "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
    "owner_peer_id": "12D3KooW...",
    "owner_version": 1,
    "last_message_id": 12,
    "profile_version": 2,
    "last_seq": 18,
    "updated_at": 1710010100,
    "signature": "base64..."
  },
  "sync": {
    "channel_id": "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
    "last_seen_seq": 18,
    "last_synced_seq": 18,
    "latest_loaded_message_id": 12,
    "oldest_loaded_message_id": 1,
    "subscribed": true,
    "updated_at": 1710010100
  }
}
```

### 4.2 ChannelMessage

```json
{
  "channel_id": "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
  "message_id": 12,
  "version": 1,
  "seq": 18,
  "owner_version": 1,
  "creator_peer_id": "12D3KooW...",
  "author_peer_id": "12D3KooW...",
  "created_at": 1710010000,
  "updated_at": 1710010000,
  "is_deleted": false,
  "message_type": "image",
  "content": {
    "text": "一张图片",
    "files": [
      {
        "file_id": "bafy...",
        "file_name": "demo.png",
        "mime_type": "image/png",
        "size": 12345,
        "sha256": "abc...",
        "blob_id": "bafy...",
        "url": "/ipfs/bafy.../demo.png"
      }
    ]
  },
  "signature": "base64..."
}
```

### 4.3 ChannelChange

```json
{
  "channel_id": "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
  "seq": 18,
  "change_type": "message",
  "message_id": 12,
  "version": 1,
  "is_deleted": false,
  "profile_version": null,
  "created_at": 1710010000,
  "provider_peer_id": "12D3KooW..."
}
```

## 5. 频道接口

### 5.1 创建频道

#### JSON 创建

`POST /api/v1/public-channels`

请求体：

```json
{
  "name": "我的频道",
  "bio": "频道简介",
  "avatar": {
    "file_name": "avatar.png",
    "mime_type": "image/png",
    "size": 12345,
    "sha256": "abc...",
    "blob_id": "bafy...",
    "url": "/ipfs/bafy.../avatar.png"
  }
}
```

返回：

- `ChannelSummary`
- 另外会额外在响应顶层返回 `channel_id`，方便前端在创建成功后直接跳转或继续请求
- 当前节点自己创建的频道会默认进入已订阅状态

#### multipart 创建（推荐：创建时直接上传头像）

`POST /api/v1/public-channels`

`Content-Type: multipart/form-data`

表单字段：

- `name`：频道名，必填
- `bio`：频道简介，可选
- `avatar`：头像文件，必填
- `mime_type`：可选；不传时服务端自动检测

返回：

- `ChannelSummary`
- 另外会额外在响应顶层返回 `channel_id`

说明：

- 头像会直接走当前节点的嵌入式 IPFS 落地链路
- 返回中的 `profile.avatar.blob_id/url/sha256` 会被自动填充

### 5.2 按 owner 列出频道

`GET /api/v1/public-channels?owner_peer_id={peer_id}`

也兼容：

`GET /api/v1/public-channels?owner={peer_id}`

返回：

```json
[
  {
    "profile": {},
    "head": {},
    "sync": {}
  }
]
```

### 5.3 列出本地已订阅频道

`GET /api/v1/public-channels/subscriptions`

语义：

- 返回本地已知且 `sync.subscribed=true` 的频道
- 同时也返回当前节点自己创建的公开频道，即使该频道当前未处于 `subscribed=true`
- 新创建的自有频道默认就是 `sync.subscribed=true`
- 当前优先按订阅更新时间排序；如果是当前节点自己创建但未订阅的频道，则回退按频道自身 `updated_at` 排序，最后再按 `public_channels.id DESC`
- 返回 `ChannelSummary[]`

### 5.4 获取频道详情

`GET /api/v1/public-channels/{channel_id}`

返回：

- `ChannelSummary`

### 5.5 获取频道 head

`GET /api/v1/public-channels/{channel_id}/head`

返回：

- `ChannelHead`

### 5.6 更新频道资料

#### JSON 更新

`PUT /api/v1/public-channels/{channel_id}`

请求体：

```json
{
  "name": "新的频道名",
  "bio": "新的简介",
  "avatar": {
    "file_name": "avatar.png",
    "mime_type": "image/png",
    "size": 12345,
    "sha256": "abc...",
    "blob_id": "bafy...",
    "url": "/ipfs/bafy.../avatar.png"
  }
}
```

返回：

- `ChannelSummary`

#### multipart 更新头像

`PUT /api/v1/public-channels/{channel_id}`

`Content-Type: multipart/form-data`

表单字段：

- `name`：可选；为空时沿用原值
- `bio`：可选
- `avatar`：头像文件，必填
- `mime_type`：可选

返回：

- `ChannelSummary`

## 6. 订阅与同步接口

### 6.1 订阅频道

`POST /api/v1/public-channels/{channel_id}/subscribe`

请求体：

```json
{
  "peer_id": "12D3KooW...",
  "peer_ids": ["12D3KooW..."],
  "last_seen_seq": 0
}
```

字段说明：

- `peer_id`：单个 seed provider，可选
- `peer_ids`：多个 seed provider，可选
- `last_seen_seq`：本地已看到的最大 `seq`

返回：

```json
{
  "profile": {},
  "head": {},
  "messages": [],
  "providers": []
}
```

实现行为：

- 首次只拉最新一页消息
- 建立 PubSub 订阅
- 之后通过 `channel_changes` 做增量补齐

### 6.2 取消订阅

`POST /api/v1/public-channels/{channel_id}/unsubscribe`

返回：

```json
{
  "ok": true,
  "channel_id": "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123"
}
```

### 6.3 查看增量同步结果

`GET /api/v1/public-channels/{channel_id}/sync?after_seq=0&limit=100`

也支持：

`POST /api/v1/public-channels/{channel_id}/sync`

说明：

- `POST` 会先触发一次实际同步
- 然后返回当前本地已知的 change 列表

返回：

```json
{
  "channel_id": "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
  "current_last_seq": 18,
  "has_more": false,
  "next_after_seq": 18,
  "items": [
    {
      "seq": 18,
      "change_type": "message",
      "message_id": 12,
      "version": 1,
      "is_deleted": false,
      "created_at": 1710010000,
      "provider_peer_id": "12D3KooW..."
    }
  ]
}
```

### 6.4 查看 provider 列表

`GET /api/v1/public-channels/{channel_id}/providers`

返回：

```json
[
  {
    "channel_id": "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
    "peer_id": "12D3KooW...",
    "source": "seed",
    "updated_at": 1710010100
  }
]
```

`source` 可能值：

- `local`
- `seed`
- `pubsub`
- `dht`

## 7. 消息读取接口

### 7.1 获取最新一页消息

`GET /api/v1/public-channels/{channel_id}/messages?limit=20`

返回：

```json
{
  "channel_id": "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
  "items": [
    {
      "message_id": 12,
      "message_type": "text"
    }
  ]
}
```

排序规则固定为：

- `message_id DESC`

读取语义：

- 当前请求只返回本地已缓存的消息
- 如果本地最新一页为空，服务端会在后台异步尝试向 provider 补拉消息
- 后台补拉失败不会把这次请求变成错误；前端可稍后再次请求同一路径获取新落地的数据

### 7.2 向上翻页拉更早消息

`GET /api/v1/public-channels/{channel_id}/messages?before_message_id=181&limit=20`

说明：

- `before_message_id` 表示取更早的消息
- 当前请求仍只返回本地已有的更早消息
- 如果本地历史不足一页，服务端会在后台异步尝试从 provider 补拉更早历史
- 前端应以当前响应先更新界面，再按需重复请求同一路径获取补拉后的数据

### 7.3 获取单条消息

`GET /api/v1/public-channels/{channel_id}/messages/{message_id}`

返回：

- `ChannelMessage`

## 8. 消息写入接口

### 8.1 发送文本 / 系统消息

`POST /api/v1/public-channels/{channel_id}/messages`

请求体：

```json
{
  "message_type": "text",
  "text": "hello world",
  "files": []
}
```

也可以发系统消息：

```json
{
  "message_type": "system",
  "text": "频道公告",
  "files": []
}
```

返回：

- `ChannelMessage`

说明：

- 无附件时，`message_type` 可传 `text` 或 `system`
- 不传时默认按 `text` 处理

### 8.2 发送文件 / 图片 / 视频 / 语音消息

`POST /api/v1/public-channels/{channel_id}/messages/file`

`Content-Type: multipart/form-data`

表单字段：

- `file`：必填
- `text`：可选
- `mime_type`：可选，不传时自动检测

返回：

- `ChannelMessage`

说明：

- 文件会先 pin 到当前节点嵌入式 IPFS
- 返回中的 `content.files[0]` 会自动包含：
  - `file_id`
  - `blob_id`
  - `url`
  - `sha256`
  - `size`
  - `mime_type`
- `message_type` 会自动识别成：
  - `image`
  - `video`
  - `audio`
  - `file`

### 8.3 更新消息

`PUT /api/v1/public-channels/{channel_id}/messages/{message_id}`

请求体：

```json
{
  "message_type": "text",
  "text": "编辑后的内容",
  "files": []
}
```

或：

```json
{
  "message_type": "system",
  "text": "新的系统提示",
  "files": []
}
```

返回：

- `ChannelMessage`

说明：

- 目前更新接口走 JSON
- 如果更新带附件，需要调用方自己带完整 `files[]` 元数据
- owner 修改旧消息前，服务端会先确保拉到该消息最新版本，避免盲改

### 8.4 删除消息

`DELETE /api/v1/public-channels/{channel_id}/messages/{message_id}`

返回：

- `ChannelMessage`

说明：

- 删除不是物理删除
- 会写成墓碑消息：
  - `is_deleted=true`
  - `message_type=deleted`
  - `content={}`

## 9. 前端推荐接入顺序

### 9.1 首次进入频道

推荐顺序：

1. 调 `POST /subscribe`
2. 用返回里的 `profile/head/messages` 初始化界面
3. 需要时调用 `GET /sync`
4. 用户上滑时调用 `GET /messages?before_message_id=...`

### 9.2 创建频道

推荐：

1. 纯文字资料：直接 `POST /api/v1/public-channels` JSON
2. 带头像：用 `multipart/form-data`

### 9.3 发消息

推荐：

1. 纯文本或系统消息：`POST /messages`
2. 图片/视频/语音/文件：`POST /messages/file`

前端不需要自己先把文件传 IPFS；本地节点会负责 pin 并回填元数据。

## 10. 当前边界

当前已实现：

- 单 owner
- 频道资料签名
- 频道 head 签名
- 消息签名
- `message_type` 纳入签名与验签
- 墓碑删除
- 最新一页加载
- 历史翻页
- `channel_changes` 增量同步
- provider 发现
- 频道头像 IPFS 落地
- 附件 IPFS 落地

当前未实现：

- owner 转移
- 多管理员
- 评论
- 点赞
- 完整事件日志
- 完整编辑历史
- HTTP 直传更新附件消息

## 11. 额外说明

### 11.1 附件大小限制

当前公开频道附件上传复用了聊天文件校验逻辑：

- 单文件最大约 `64 MiB`

频道头像当前也复用了头像大小限制：

- 头像最大约 `512 KiB`

### 11.2 内部 P2P 协议

一般前端 / app 不需要直接使用，但为了联调方便，当前底层协议如下：

- PubSub topic：
  `/meshchat/public-channel/{channel_id}/v1`
- RPC stream protocol：
  `/meshchat/public-channel/rpc/1.0.0`

---

如果后续公开频道接口继续扩展，这份文档应以 `internal/api/local_api.go` 与 `internal/publicchannel/*` 的实际实现为准同步更新。
