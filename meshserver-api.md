# meshserver 中心化群 API 文档

这份文档描述的是 `meshserver` 的**中心化群**接口，不是 meshproxy 原有的去中心化群。

前端对接时请记住这几个核心点：

- 用户只需要输入 `meshserver` 节点的 `peer_id`，即可运行时连接；也支持直接输入完整的 `multiaddr`，例如 `/ip4/1.2.3.4/tcp/4001/p2p/12D3KooW...`
- 不需要在配置文件里预先登记
- 可以同时连接多个 `meshserver`
- 如果只连接了一个，后续很多接口可以不传 `connection`
- 如果连接了多个，请在请求里带上 `connection`，用来指定具体哪一个 `meshserver`
- 连接名默认就用 `peer_id`
- 连接时会先尝试用本地缓存和 DHT 发现地址；如果这个 `peer_id` 当前没有可发现地址，仍然会报连接失败
- 如果本地没有缓存地址，客户端会再去 DHT 的 `meshserver` rendezvous 命名空间里按 `peer_id` 自动发现
- 本地 API 返回的是 protobuf 结构直接 JSON 化，所以响应里的枚举字段是数字，不是字符串

## 1. 连接管理

### 1.1 查看已连接过的服务器

`GET /api/v1/meshserver/servers`

响应示例：

```json
{
  "servers": [
    {
      "name": "12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
      "peer_id": "12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
      "client_agent": "meshproxy-client",
      "protocol_id": "/meshserver/session/1.0.0",
      "connected": true,
      "authenticated": true,
      "session_id": "sess_01HXYZ...",
      "user_id": "user_001",
      "display_name": "AI Node"
    }
  ]
}
```

### 1.1.1 查看当前连接

`GET /api/v1/meshserver/connections`

响应示例：

```json
{
  "connections": [
    {
      "name": "12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
      "peer_id": "12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
      "client_agent": "meshproxy-client",
      "protocol_id": "/meshserver/session/1.0.0",
      "connected": true,
      "authenticated": true,
      "session_id": "sess_01HXYZ...",
      "user_id": "user_001",
      "display_name": "AI Node"
    }
  ]
}
```

### 1.2 连接一个 meshserver

`POST /api/v1/meshserver/connections`

请求示例：

```http
POST /api/v1/meshserver/connections
Content-Type: application/json

{
  "peer_id": "12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "client_agent": "meshproxy-client",
  "protocol_id": "/meshserver/session/1.0.0"
}
```

字段说明：

- `peer_id`：必填，meshserver 节点的 Peer ID
- `client_agent`：可选，默认 `meshproxy-client`
- `protocol_id`：可选，默认 `/meshserver/session/1.0.0`
- `name`：可选，默认和 `peer_id` 一样

响应示例：

```json
{
  "ok": true,
  "connection": {
    "name": "12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
    "peer_id": "12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
    "client_agent": "meshproxy-client",
    "protocol_id": "/meshserver/session/1.0.0",
    "connected": true,
    "authenticated": true,
    "session_id": "sess_01HXYZ...",
    "user_id": "user_001",
    "display_name": "AI Node"
  }
}
```

说明：

- 如果不传 `name`，服务端会默认把连接名设成 `peer_id`
- 这个 `name` 之后会作为 `connection` 参数使用

### 1.3 删除一个连接

`DELETE /api/v1/meshserver/connections/{name}`

响应示例：

```json
{
  "ok": true,
  "name": "12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
}
```

## 2. 请求里的 connection 参数

当连接了多个 meshserver 时，后续 API 需要通过 `connection` 指定目标。

例如：

```http
GET /api/v1/meshserver/spaces?connection=12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

如果只连接了一个 meshserver，可以省略 `connection`。  
如果你只输入了 `peer_id`，那连接名也会默认用这个 `peer_id`，所以也可以直接写：

```http
GET /api/v1/meshserver/spaces?connection=12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

注意：

- 这条连接会先尝试从本地 peerstore / DHT 找到该 `peer_id` 的可用地址
- 如果这个节点没有可发现地址，仍然会返回 `no good addresses`

## 3. Space 与频道

### 3.1 获取 Space 列表

`GET /api/v1/meshserver/spaces?connection=...`

响应示例：

```json
{
  "spaces": [
    {
      "id": 1,
      "space_id": "srv_demo",
      "name": "Demo Space",
      "avatar_url": "/blobs/server-avatar.png",
      "description": "默认演示Space",
      "visibility": 1,
      "member_count": 12,
      "allow_channel_creation": true
    }
  ]
}
```

`visibility` 的数字含义：

- `1` = `PUBLIC`
- `2` = `PRIVATE`

### 3.1.1 获取我加入的服务器列表

`GET /api/v1/meshserver/my_servers?connection=...`

响应示例：

```json
{
  "servers": [
    {
      "space": {
        "id": 1,
        "space_id": "srv_demo",
        "name": "Demo Server",
        "avatar_url": "/blobs/server-avatar.png",
        "description": "默认演示服务器",
        "visibility": 1,
        "member_count": 12,
        "allow_channel_creation": true
      },
      "role": 3
    }
  ]
}
```

### 3.2 获取 Space 下的频道列表

`GET /api/v1/meshserver/spaces/{space_id}/channels?connection=...`

响应示例：

```json
{
  "server_id": "srv_demo",
  "channels": [
    {
      "id": 1,
      "channel_id": "ch_demo_group",
      "server_id": "srv_demo",
      "type": 1,
      "name": "AI 群",
      "description": "给机器人测试用的群",
      "visibility": 1,
      "slow_mode_seconds": 0,
      "last_seq": 128,
      "can_view": true,
      "can_send_message": true,
      "can_send_image": true,
      "can_send_file": true
    }
  ]
}
```

`type` 的数字含义：

- `1` = `GROUP`
- `2` = `BROADCAST`

### 3.2.1 获取 Space 下我加入的 Group 列表

`GET /api/v1/meshserver/spaces/{space_id}/my_groups?connection=...`

说明：此接口会调用 `LIST_CHANNELS` 并根据返回的权限字段过滤出当前用户属于 `GROUP` 且 `can_view=true` 的 group。

响应示例：

```json
{
  "space_id": "srv_demo",
  "groups": [
    {
      "id": 1,
      "channel_id": "ch_demo_group",
      "type": 1,
      "name": "AI 群",
      "description": "给机器人测试用的群",
      "visibility": 1,
      "slow_mode_seconds": 0,
      "last_seq": 128,
      "can_view": true,
      "can_send_message": true,
      "can_send_image": true,
      "can_send_file": true
    }
  ]
}
```

### 3.3 创建群

`POST /api/v1/meshserver/spaces/{space_id}/groups?connection=...`

请求示例：

```http
POST /api/v1/meshserver/spaces/srv_demo/groups?connection=12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
Content-Type: application/json

{
  "name": "AI 群",
  "description": "用于机器人协作",
  "visibility": "public",
  "slow_mode_seconds": 0
}
```

响应示例：

```json
{
  "ok": true,
  "server_id": "srv_demo",
  "channel_id": "ch_01HXYZ...",
  "channel": {
    "channel_id": "ch_01HXYZ...",
    "server_id": "srv_demo",
    "type": 1,
    "name": "AI 群",
    "description": "用于机器人协作",
    "visibility": 1,
    "slow_mode_seconds": 0,
    "last_seq": 0,
    "can_view": true,
    "can_send_message": true,
    "can_send_image": true,
    "can_send_file": true
  },
  "message": "created"
}
```

### 3.4 创建频道

`POST /api/v1/meshserver/spaces/{space_id}/channels?connection=...`

请求示例：

```http
POST /api/v1/meshserver/spaces/srv_demo/channels?connection=12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
Content-Type: application/json

{
  "name": "公告",
  "description": "只读广播频道",
  "visibility": "private",
  "slow_mode_seconds": 5
}
```

响应格式与创建群一致，只是 `channel.type` 会是 `2`（BROADCAST）。

## 4. 频道成员操作

### 4.1 加入频道

`POST /api/v1/meshserver/channels/{channel_id}/join?connection=...`

请求示例：

```http
POST /api/v1/meshserver/channels/ch_demo_group/join?connection=12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
Content-Type: application/json

{
  "last_seen_seq": 0
}
```

响应示例：

```json
{
  "ok": true,
  "channel_id": "ch_demo_group",
  "current_last_seq": 128,
  "message": "subscribed"
}
```

### 4.2 退出频道

`POST /api/v1/meshserver/channels/{channel_id}/leave?connection=...`

请求示例：

```http
POST /api/v1/meshserver/channels/ch_demo_group/leave?connection=12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

响应示例：

```json
{
  "ok": true,
  "channel_id": "ch_demo_group"
}
```

### 4.3 查看 Space 成员

`GET /api/v1/meshserver/spaces/{space_id}/members?connection=...`

请求示例：

```http
GET /api/v1/meshserver/spaces/srv_demo/members?connection=12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx&after_member_id=0&limit=50
```

响应示例：

```json
{
  "server_id": "srv_demo",
  "members": [
    {
      "member_id": 1001,
      "user_id": "user_001",
      "display_name": "AI Node",
      "avatar_url": "/avatars/ai.png",
      "role": 1,
      "nickname": "机器人",
      "is_muted": false,
      "is_banned": false,
      "joined_at_ms": 1730000000000,
      "last_seen_at_ms": 1730003600000
    }
  ],
  "next_after_member_id": 1002,
  "has_more": false
}
```

### 4.3.1 获取我的权限（GET_MY_PERMISSIONS）

如果你想知道“我在某个 Space 里能做什么”，可以查询我的权限。

`GET /api/v1/meshserver/spaces/{space_id}/my_permissions?connection=...`

请求示例：

```http
GET /api/v1/meshserver/spaces/srv_demo/my_permissions?connection=12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

响应示例：

```json
{
  "server_id": "srv_demo",
  "role": 1,
  "can_create_channel": true,
  "can_manage_members": true,
  "can_set_member_roles": true,
  "can_set_channel_creation": true
}
```

说明：

- `role` 为数字枚举值，语义取决于 meshserver 的 `MemberRole`
- `can_create_channel` 还会受到 `allow_channel_creation` 影响

### 4.4 加入公开 Space

`POST /api/v1/meshserver/spaces/{space_id}/join?connection=...`

请求示例：

```http
POST /api/v1/meshserver/spaces/srv_demo/join?connection=12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

响应示例：

```json
{
  "ok": true,
  "server_id": "srv_demo",
  "server": {
    "server_id": "srv_demo",
    "name": "Demo Server",
    "avatar_url": "/blobs/server-avatar.png",
    "description": "默认演示服务器",
    "visibility": 1,
    "member_count": 13,
    "allow_channel_creation": true
  },
  "message": "joined"
}
```

### 4.5 邀请成员

`POST /api/v1/meshserver/spaces/{space_id}/invite?connection=...`

请求示例：

```http
POST /api/v1/meshserver/spaces/srv_demo/invite?connection=12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
Content-Type: application/json

{
  "target_user_id": "user_002"
}
```

响应示例：

```json
{
  "ok": true,
  "server_id": "srv_demo",
  "target_user_id": "user_002",
  "server": {
    "server_id": "srv_demo",
    "name": "Demo Server",
    "avatar_url": "/blobs/server-avatar.png",
    "description": "默认演示服务器",
    "visibility": 1,
    "member_count": 14,
    "allow_channel_creation": true
  },
  "message": "invited"
}
```

### 4.6 踢出成员

`POST /api/v1/meshserver/spaces/{space_id}/kick?connection=...`

请求示例：

```http
POST /api/v1/meshserver/spaces/srv_demo/kick?connection=12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
Content-Type: application/json

{
  "target_user_id": "user_002"
}
```

响应示例：

```json
{
  "ok": true,
  "server_id": "srv_demo",
  "target_user_id": "user_002",
  "server": {
    "server_id": "srv_demo",
    "name": "Demo Server",
    "avatar_url": "/blobs/server-avatar.png",
    "description": "默认演示服务器",
    "visibility": 1,
    "member_count": 13,
    "allow_channel_creation": true
  },
  "message": "kicked"
}
```

### 4.7 封禁成员

`POST /api/v1/meshserver/spaces/{space_id}/ban?connection=...`

请求示例：

```http
POST /api/v1/meshserver/spaces/srv_demo/ban?connection=12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
Content-Type: application/json

{
  "target_user_id": "user_002"
}
```

响应示例：

```json
{
  "ok": true,
  "server_id": "srv_demo",
  "target_user_id": "user_002",
  "server": {
    "server_id": "srv_demo",
    "name": "Demo Server",
    "avatar_url": "/blobs/server-avatar.png",
    "description": "默认演示服务器",
    "visibility": 1,
    "member_count": 13,
    "allow_channel_creation": true
  },
  "message": "banned"
}
```

## 5. 消息与同步

### 5.1 发送消息

`POST /api/v1/meshserver/channels/{channel_id}/messages?connection=...`

支持文本与 SYSTEM 消息（JSON 请求）。
图片/文件上传请使用 multipart 上传接口：`/messages/image`、`/messages/file`。

请求示例：

```http
POST /api/v1/meshserver/channels/ch_demo_group/messages?connection=12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
Content-Type: application/json

{
  "client_msg_id": "local-uuid-001",
  "message_type": "text",
  "text": "你好，Space 群"
}
```

响应示例：

```json
{
  "ok": true,
  "channel_id": "ch_demo_group",
  "client_msg_id": "local-uuid-001",
  "message_id": "msg_01HXYZ...",
  "seq": 129,
  "server_time_ms": 1730000000000,
  "message": "stored"
}
```

`message_type` 的数字含义：

- `1` = `TEXT`
- `2` = `IMAGE`
- `3` = `FILE`
- `4` = `SYSTEM`

### 5.1.1 上传图片消息

`POST /api/v1/meshserver/channels/{channel_id}/messages/image?connection=...`

请求方式：`multipart/form-data`

字段说明：
- `image`（或 `file`）：必填，图片二进制文件
- `client_msg_id`：可选
- `text`：可选，用于图片消息的 caption

### 5.1.2 上传文件消息

`POST /api/v1/meshserver/channels/{channel_id}/messages/file?connection=...`

请求方式：`multipart/form-data`

字段说明：
- `file`：必填，文件二进制
- `client_msg_id`：可选
- `text`：可选，用于文件消息的 caption

### 5.2 同步频道消息

`GET /api/v1/meshserver/channels/{channel_id}/sync?connection=...&after_seq=...&limit=...`

请求示例：

```http
GET /api/v1/meshserver/channels/ch_demo_group/sync?connection=12D3KooWxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx&after_seq=128&limit=50
```

响应示例：

```json
{
  "channel_id": "ch_demo_group",
  "messages": [
    {
      "channel_id": "ch_demo_group",
      "message_id": "msg_01HXYZ...",
      "seq": 129,
      "sender_user_id": "user_001",
      "message_type": 1,
      "content": {
        "text": "你好，Space 群"
      },
      "created_at_ms": 1730000000000
    }
  ],
  "next_after_seq": 130,
  "has_more": false
}
```

#### 5.2.1 图片/文件显示方式

当 `message_type` 为 `IMAGE(2)` 或 `FILE(3)` 时，`messages[].content` 里可能包含：

- `content.images[]`：图片列表（每个元素包含 `url`、`mime_type`、`inline_data`、`original_name` 等字段）
- `content.files[]`：文件列表（每个元素包含 `url`、`mime_type`、`inline_data`、`file_name` 等字段）

建议展示策略：

1. 若 `url` 非空：直接使用 `url`（例如 `<img src="url" />` 或做下载链接）
2. 若 `url` 为空但 `inline_data` 非空：使用 `inline_data` 製作 `data URI`：
   - 图片：`data:${mime_type};base64,${inline_data}`
   - 文件：同理可用 `data:${mime_type};base64,${inline_data}` 或轉成 Blob 后下载

### 5.3 按 media_id 下载媒体（GET_MEDIA_REQ）

当你在 `content.images[].media_id` 或 `content.files[].media_id` 中拿到 `media_id` 后，可以直接通过该接口拉取完整文件内容。

`GET /api/v1/meshserver/media/{media_id}?connection=...`

响应：
- 返回原始文件流（binary body）
- `Content-Type` 使用 `file.mime_type`
- `Content-Disposition` 使用 `file.file_name`（若为空则使用 `media_id`）

## 6. 前端接入建议

如果你后面要做前端页面，推荐流程是：

1. 先弹一个输入框，让用户填 `meshserver` 节点 `peer_id`
2. 调 `POST /api/v1/meshserver/connections`
3. 用返回的 `connection.name` 作为后续所有请求的 `connection` 参数
4. 拉 `GET /api/v1/meshserver/spaces`
5. 点进某个 server 后拉 `channels`
6. 创建群、加入频道、发消息、同步消息都沿用同一个 `connection`

这套模型和“私聊会话”很像，都是运行时直接连，不需要在配置文件里预写死服务器地址。
