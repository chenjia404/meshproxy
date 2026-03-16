# 《基于 meshproxy 现有架构的去中心化群聊技术方案》

版本：V1.0  
定位：在当前 `meshproxy + libp2p + direct/relay-e2e + 本地 SQLite` 基础上扩展群聊，而不是重做一套独立群组网络  
适用：当前单聊已经存在，群聊需要与现有 `Peer ID`、发现机制、中继能力、消息落库、ACK/重试机制兼容

---

## 1. 先说结论

如果目标是“尽快在现有代码上做出可用的去中心化群聊”，我建议采用下面这条路线：

1. 继续复用现有 `Peer ID` 作为设备身份
2. 群聊消息仍然通过点对点发送，只是发送对象从 1 个 peer 变成多个成员
3. 群内容不走全网 gossip 广播，而是走“发送端 fanout 到群成员”的方式
4. 群消息内容使用“每个群 epoch 一把组密钥”加密
5. 组密钥通过现有单聊安全通道或点对点加密包分发给每个成员
6. 成员变更时强制轮换群 epoch 和组密钥
7. 仍然不引入中心服务器和 mailbox；离线靠本地 outbox + 在线成员补同步

这个方案的核心特点是：

* 真正去中心化：没有中心服务器、没有中心群消息存储节点
* 和现有实现兼容：不推翻当前单聊、请求箱、relay-e2e 思路
* 工程上可落地：复杂度明显低于直接上 MLS 或 ownerless 群共识
* 适合 V1：优先支持小到中等规模群聊，例如 `3 ~ 32` 人，最多建议先控制在 `64` 人以内

---

## 2. 当前代码的现实基础

从现有实现看，项目已经具备群聊最关键的底座：

* 身份：`Peer ID`
* 发现：`bootstrap + gossip + peer exchange + 可选 DHT`
* 传输：`direct` 和 `/meshproxy/chat/relay-e2e/1.0.0`
* 单聊协议：`request / msg / ack / presence`
* 本地存储：SQLite
* 可靠性：ACK、重试、outbox
* 加密：单聊会话密钥、计数器、AEAD

但当前单聊模型有几个直接限制：

* 一个 `conversation` 只绑定一个 `peer_id`
* `session_state` 也是一对一会话
* 消息状态是“我发给对方”而不是“我发给一组成员”
* 不存在群身份、群成员、群事件日志、组密钥、成员变更 rekey

所以群聊不能只是在 `conversation` 里塞一个成员数组，而是要新增一层“群状态机”。

---

## 3. 设计目标

### 3.1 V1 目标

V1 群聊建议支持：

* 群创建
* 邀请成员加入
* 成员主动接受加入
* 群文本消息
* 群内文件消息
* 在线 direct，失败时 relay-e2e
* 端到端加密
* 成员变更后的组密钥轮换
* 本地落库
* 每成员 ACK 和重试
* 离线后向其他在线成员补同步

### 3.2 V1 非目标

V1 不建议一开始就做：

* 超大群频道
* 全网广播式群消息扩散
* 多设备同一账号
* 完整匿名群成员隐藏
* 无限历史追溯
* ownerless 多管理员并发写成员变更
* 直接引入 MLS 全套树算法

---

## 4. 为什么不建议用 gossip 做群消息广播

表面上看，meshproxy 已经有 gossip，似乎可以让群消息在网络里扩散。但这条路不适合当前项目：

* gossip 天然偏向“公开传播”，不适合群访问控制
* 很难做可靠 ACK
* 很难控制消息重复和风暴
* 很难处理“成员变更后旧成员不能再看新消息”
* relay 看不到明文，但 gossip 会放大流量特征
* 当前系统没有 mailbox，也没有长期群消息保管节点

所以更合适的方式是：

* 群消息只发给当前群成员
* 每个接收目标仍然是一个明确的 peer
* transport 复用现有 direct / relay-e2e
* 群协议只管“逻辑上的一条消息发给多个成员”

这也是最贴合当前代码结构的方案。

---

## 5. 推荐的总体架构

### 5.1 两层结构

群聊拆成两层：

1. 控制面
2. 数据面

控制面负责：

* 群创建
* 邀请
* 接受邀请
* 退群
* 移除成员
* 管理员变更
* 组密钥轮换
* 群资料更新

数据面负责：

* 发送群文本
* 发送群文件
* ACK
* 补同步

### 5.2 核心思路

推荐使用：

* “群事件日志”维护成员状态
* “群 epoch”维护组密钥版本
* “单条群消息 fanout 到每个成员”完成传输

一句话概括：

* 群状态是复制的
* 群消息是单发多送的
* 群密钥是按成员变更轮换的

---

## 6. 群身份模型

### 6.1 基本对象

新增以下逻辑对象：

#### Group

* `group_id`
* `title`
* `avatar`
* `controller_peer_id`
* `current_epoch`
* `state`
* `created_at`
* `updated_at`

#### GroupMember

* `group_id`
* `peer_id`
* `role`：`controller / admin / member`
* `state`：`invited / active / left / removed`
* `joined_epoch`
* `left_epoch`
* `invited_by`
* `updated_at`

#### GroupEvent

* `event_id`
* `group_id`
* `event_seq`
* `event_type`
* `actor_peer_id`
* `payload`
* `signature`
* `created_at`

#### GroupEpoch

* `group_id`
* `epoch`
* `key_id`
* `group_key_wrapped_for_local`
* `created_at`

### 6.2 为什么需要 controller

真正的难点不是“消息群发”，而是“成员变更的权威顺序”。

如果一开始就允许多个管理员并发改成员列表，就会立刻遇到：

* 谁的移除先发生
* 邀请和移除并发怎么办
* 不同节点看到的事件顺序不同怎么办
* rekey 谁负责发

因此我建议 V1 采用：

* 群内消息去中心化
* 群成员控制面单写者

也就是：

* `controller_peer_id` 是当前群状态的权威写入者
* 只有 controller 可以发成员变更和 epoch 轮换事件
* controller 可以转移
* 普通成员仍然可以离线重试、补同步、直接发群消息

这不是中心服务器，而只是群内权限角色。  
群消息数据面仍然完全去中心化。

如果后续一定要支持多管理员并发控制，再升级为：

* 多签控制
* DAG 事件日志
* 或 MLS / 共识化成员管理

但不建议在 V1 直接上。

### 6.3 角色权限矩阵

建议群角色先定义为：

* `controller`
* `admin`
* `member`

但 V1 的真正管理权限只交给 `controller`，`admin` 先作为兼容未来扩展的预留角色存在。

#### V1 权限建议

| 操作 | controller | admin | member |
|---|---|---|---|
| 查看群资料 | 是 | 是 | 是 |
| 发送群消息 | 是 | 是 | 是 |
| 拉取群同步 | 是 | 是 | 是 |
| 主动退群 | 是 | 是 | 是 |
| 邀请新成员 | 是 | 否 | 否 |
| 接受入群申请 | 是 | 否 | 否 |
| 移除成员 | 是 | 否 | 否 |
| 修改群名 | 是 | 否 | 否 |
| 转移控制权 | 是 | 否 | 否 |
| 触发 epoch 轮换 | 是 | 否 | 否 |
| 解散群 | 是 | 否 | 否 |

#### 为什么 V1 不开放 admin 管理权限

因为一旦 `admin` 也能改成员状态，就会立刻遇到这些协议问题：

* 两个 admin 同时邀请不同成员，事件序列谁在前
* 一个 admin 邀请，另一个 admin 移除，谁生效
* 成员变更后由谁负责 rekey
* 节点离线重连后如何判断哪条管理事件是最终状态

这些问题都不是 UI 权限判断，而是协议层一致性问题。  
所以 V1 建议把“管理权”收敛到 `controller`，这是为了减少状态分叉。

#### 为什么还要保留 admin 角色字段

虽然 V1 不给 `admin` 实权，但仍建议把 `admin` 写进成员角色枚举里，原因有三点：

* 后续升级权限模型时不需要改表结构
* UI 可以先展示“管理员”标签
* controller 转移或多管理员版本演进时更平滑

### 6.4 V2 可演进的 admin 模型

如果后续你要开放管理员能力，建议按下面顺序逐步放权，而不是一次性全开：

#### V2-A：弱管理员

`admin` 只允许做低风险操作：

* 修改群名
* 修改群公告
* 邀请成员

但不允许：

* 移除成员
* 转移 controller
* 触发最终成员裁决

这种模型下，协议仍可要求：

* `admin` 发起邀请
* `controller` 最终确认并写入正式事件

也就是：

* `admin` 可以提案
* `controller` 负责提交

#### V2-B：半管理者

如果想让 `admin` 真正参与成员管理，建议只开放：

* 邀请成员
* 处理加入请求

仍然不开放：

* 踢人
* controller 转移
* 解散群

这样可以把高风险 rekey 冲突控制住。

#### V2-C：完整多管理员

只有在你准备引入更复杂的一致性机制时，才建议开放：

* 多 admin 并发移人
* 多 admin 并发审批
* 多 admin 并发 rekey

这通常意味着要补其中至少一种：

* 单调事件日志 + 权威提交者
* 多签确认
* 共识或 DAG 合并规则

否则会非常难排错。

---

## 7. 密钥模型

### 7.1 推荐方案：按 epoch 的组密钥

每个群在每个 epoch 维护一把 `group_epoch_key`：

* 同一个 epoch 内，所有有效成员都能解密群消息
* 成员变更时，必须切换到新 epoch
* 被移除成员不会收到新 epoch 的密钥

### 7.2 组密钥如何分发

推荐做法：

1. controller 生成新的 `group_epoch_key`
2. 针对每个有效成员，单独封装一个 `wrapped_group_key`
3. 这个封装使用现有单聊安全能力
4. 成员收到后保存到本地 `group_epochs`

这里可以复用当前单聊的两种安全路径：

* 已有单聊会话时：直接走现有会话密钥加密后发送控制包
* 尚未建立单聊会话时：用对方 profile 中的 `chat_kex_pub` 做一次点对点加密封装

### 7.3 为什么不建议 V1 直接上 Sender Key 或 MLS

#### 不直接上 Sender Key 的原因

Signal 风格 Sender Key 很适合群聊，但会引入：

* 每个发送者一套 sender chain
* 成员变更时每个发送者都要重新分发 sender key
* 丢包和补同步逻辑更复杂

这对当前项目来说跨度有点大。

#### 不直接上 MLS 的原因

MLS 更现代，但当前项目要为它补的能力太多：

* 树状态维护
* proposal / commit / welcome
* 异步成员变更恢复
* 多设备与历史树状态一致性

如果现在目标是尽快做出“工程上可用”的群聊，MLS 成本过高。

### 7.4 认证

因为群内成员共享同一 epoch key，所以还需要单独做“发送者认证”。

建议每条群消息都附带：

* `sender_peer_id`
* `sender_seq`
* `signature`

签名内容建议覆盖：

* `group_id`
* `epoch`
* `msg_id`
* `sender_peer_id`
* `sender_seq`
* `ciphertext_hash`
* `sent_at_unix`

这样可以保证：

* 不能伪造别人发的消息
* 共享组密钥不影响来源认证

---

## 8. 群协议家族

建议新增独立协议族，不混进现有单聊协议：

* `/meshproxy/group/control/1.0.0`
* `/meshproxy/group/msg/1.0.0`
* `/meshproxy/group/ack/1.0.0`
* `/meshproxy/group/sync/1.0.0`
* `/meshproxy/group/relay-e2e/1.0.0`

### 8.1 direct 与 relay 的关系

建议保持和现有单聊一致的传输策略：

* 能直连就 direct
* 不能直连就封装进 `/meshproxy/group/relay-e2e/1.0.0`

也可以进一步简化成：

* 上层用统一 `GroupEnvelope`
* 底层 transport 自动选择 direct 或 relay-e2e

### 8.2 控制消息类型

建议先支持这些控制事件：

* `group_create`
* `group_invite`
* `group_join`
* `group_leave`
* `group_remove`
* `group_title_update`
* `group_controller_transfer`
* `group_epoch_rotate`

### 8.3 数据消息类型

建议支持：

* `group_chat_text`
* `group_chat_file`
* `group_delivery_ack`
* `group_message_revoke`

### 8.4 群事件定义表

下面这张表建议作为 V1 控制面的标准事件集合。  
这里的“事件”特指会改变群状态、成员状态、权限状态或 epoch 状态的控制消息。

| 事件类型 | 发送者 | 是否写入 group_events | 是否影响成员状态 | 是否触发 rekey | 说明 |
|---|---|---|---|---|---|
| `group_create` | controller | 是 | 是 | 是 | 创建群并初始化 `epoch=1` |
| `group_invite` | controller | 是 | 是 | 否 | 将目标成员标记为 `invited` |
| `group_join` | invited member -> controller | 是 | 是 | 否 | 被邀请者接受并进入 `active` |
| `group_leave` | active member -> controller | 是 | 是 | 是 | 成员主动退出，剩余成员进入新 epoch |
| `group_remove` | controller | 是 | 是 | 是 | controller 移除成员，必须立即 rekey |
| `group_title_update` | controller | 是 | 否 | 否 | 更新群名称等资料 |
| `group_controller_transfer` | controller | 是 | 否 | 是 | 控制权移交后必须 rekey |
| `group_epoch_rotate` | controller | 是 | 否 | 是 | 显式轮换 epoch 和组密钥 |

### 8.5 事件通用字段

所有控制事件建议共享一个统一外层结构：

```json
{
  "type": "group_control",
  "event_type": "group_invite",
  "group_id": "uuid",
  "event_id": "uuid",
  "event_seq": 7,
  "actor_peer_id": "12D3KooW...",
  "created_at_unix": 1740000000000,
  "payload": {},
  "signature": "..."
}
```

建议所有事件都至少包含这些字段：

* `type`
* `event_type`
* `group_id`
* `event_id`
* `event_seq`
* `actor_peer_id`
* `created_at_unix`
* `payload`
* `signature`

签名建议覆盖：

* `event_type`
* `group_id`
* `event_id`
* `event_seq`
* `actor_peer_id`
* `created_at_unix`
* `payload_hash`

这样做的好处是：

* 事件可以统一存储
* 控制 handler 更好扩展
* 补同步时可以按统一 envelope 回放事件

### 8.6 各事件字段与校验规则

#### `group_create`

用途：

* 创建群
* 写入初始成员
* 初始化 `epoch=1`

建议 payload：

```json
{
  "title": "meshproxy dev",
  "controller_peer_id": "12D3KooW-controller",
  "initial_members": [
    "12D3KooW-a",
    "12D3KooW-b"
  ],
  "epoch": 1
}
```

校验规则：

* `actor_peer_id` 必须等于 `controller_peer_id`
* `event_seq` 必须等于 `1`
* `initial_members` 必须包含 controller 自己
* 本地若已存在该 `group_id`，则拒绝重复创建
* 收到该事件时应同时收到本地可解的 `epoch=1` 组密钥

#### `group_invite`

用途：

* 邀请新成员进入群
* 将目标成员状态置为 `invited`

建议 payload：

```json
{
  "invitee_peer_id": "12D3KooW-new",
  "role": "member",
  "invite_text": "join us",
  "epoch": 3,
  "wrapped_group_key": "base64..."
}
```

校验规则：

* 只能由当前 `controller` 发送
* `invitee_peer_id` 不能已经是 `active`
* `event_seq` 必须等于本地 `last_event_seq + 1`
* `epoch` 必须等于当前群 `current_epoch`
* 发给被邀请者时必须带其专属 `wrapped_group_key`

处理结果：

* 若我是被邀请者，则本地写入群资料和邀请记录
* 若我是现有成员，则只更新成员状态为 `invited`

#### `group_join`

用途：

* 被邀请成员接受加入
* controller 将其成员状态切换为 `active`

建议 payload：

```json
{
  "joiner_peer_id": "12D3KooW-new",
  "accepted_epoch": 3
}
```

校验规则：

* 发送链路上，消息来源应是 `joiner_peer_id`
* controller 在写入正式事件前，必须先确认该成员状态为 `invited`
* `accepted_epoch` 必须不小于该成员收到邀请时的 epoch
* 只有 controller 可以给全体成员 fanout 最终 `group_join` 事件

处理结果：

* `group_members.state` 从 `invited` 变为 `active`
* `joined_epoch` 写入当前 epoch

#### `group_leave`

用途：

* 成员主动退出群

建议 payload：

```json
{
  "leaver_peer_id": "12D3KooW-member",
  "reason": "left_by_self"
}
```

校验规则：

* 发起者必须等于 `leaver_peer_id`
* 发起者当前必须是 `active`
* controller 收到后必须写入正式 leave 事件
* leave 事件提交后必须紧跟一次新 epoch 分发

处理结果：

* 该成员状态改为 `left`
* `left_epoch` 写入旧 epoch
* 群切换到新 epoch

#### `group_remove`

用途：

* controller 移除指定成员

建议 payload：

```json
{
  "target_peer_id": "12D3KooW-member",
  "reason": "policy_violation"
}
```

校验规则：

* 只能由当前 `controller` 发起
* `target_peer_id` 当前必须是 `active` 或 `invited`
* 不允许移除不存在的成员
* 若移除的是当前 controller，自身逻辑应拒绝
* 事件提交后必须立即轮换 epoch

处理结果：

* 目标成员状态改为 `removed`
* 剩余成员收到新 epoch 密钥
* 被移除成员不再接收后续 epoch

#### `group_title_update`

用途：

* 更新群标题
* 后续也可扩展到群头像、公告等资料

建议 payload：

```json
{
  "title": "meshproxy core team"
}
```

校验规则：

* 只能由当前 `controller` 发起
* `title` 不能为空
* 长度应受限，例如 `1 ~ 64` 字符

处理结果：

* 更新 `groups.title`
* 不触发 rekey

#### `group_controller_transfer`

用途：

* 将群控制权从旧 controller 转给新 controller

建议 payload：

```json
{
  "from_peer_id": "12D3KooW-old",
  "to_peer_id": "12D3KooW-new"
}
```

校验规则：

* 只能由当前 `controller` 发起
* `from_peer_id` 必须等于当前 controller
* `to_peer_id` 必须是当前 `active` 成员
* 转移成功后必须立即 rekey

处理结果：

* `groups.controller_peer_id` 更新
* 新 controller 获得后续控制权
* 所有 active 成员进入新 epoch

#### `group_epoch_rotate`

用途：

* 显式轮换群组密钥
* 通常作为 `leave/remove/controller_transfer` 的后续动作出现

建议 payload：

```json
{
  "old_epoch": 3,
  "new_epoch": 4,
  "members": [
    "12D3KooW-a",
    "12D3KooW-b"
  ],
  "wrapped_keys": {
    "12D3KooW-a": "base64...",
    "12D3KooW-b": "base64..."
  }
}
```

校验规则：

* 只能由当前 `controller` 发起
* `new_epoch` 必须等于 `old_epoch + 1`
* `members` 必须等于当前所有 `active` 成员集合
* 本地 peer 若在 `members` 中，则必须能找到自己的 `wrapped_keys[peer_id]`

处理结果：

* 写入 `group_epochs`
* 更新 `groups.current_epoch`
* 后续消息只能使用新 epoch

### 8.7 V1 事件顺序规则

为了避免群状态分叉，V1 建议强制以下顺序规则：

* 同一 `group_id` 下，只有 controller 能分配正式 `event_seq`
* 所有成员都按 `event_seq` 顺序提交事件到本地状态机
* 若收到 `event_seq` 跳号事件，不立即提交，先进入待补同步队列
* 若收到旧 `event_seq`，按重复事件处理

这条规则很关键，因为它决定了：

* 成员列表最终一致
* `current_epoch` 最终一致
* 邀请、加入、移除不会在不同节点上得出不同结果

### 8.8 事件与传输消息的区别

这里要明确区分两类对象：

#### 事件

事件是“会改变群状态”的控制记录，必须落 `group_events`：

* `group_create`
* `group_invite`
* `group_join`
* `group_leave`
* `group_remove`
* `group_title_update`
* `group_controller_transfer`
* `group_epoch_rotate`

#### 传输消息

传输消息是网络交互包，不一定都落成正式事件，例如：

* 被邀请者先发一个 `join_request`
* controller 校验后再写正式 `group_join`
* 成员先发一个 `leave_request`
* controller 落正式 `group_leave`

也就是说：

* 网络消息可以是“请求”
* 正式事件必须由 controller 编排并赋予最终顺序

这样协议会更稳，也更贴合当前单写者模型。

---

## 9. 控制面详细流程

### 9.1 创建群

创建群时：

1. 发起者生成 `group_id`
2. 本地写入 `Group`
3. 本地生成 `epoch=1` 的 `group_epoch_key`
4. 写入 `group_create` 事件，`event_seq=1`
5. 创建者自己作为 `controller`
6. 给每个被邀请成员发送 `group_invite`
7. 同时为每个受邀成员附带 `epoch=1` 的密钥包

`group_create` 事件至少包含：

* `group_id`
* `title`
* `controller_peer_id`
* `initial_members`
* `epoch`
* `created_at`

### 9.2 邀请成员

controller 邀请新成员时：

1. 生成 `group_invite` 事件
2. 被邀请成员状态先标记为 `invited`
3. 向对方发送 invite 包
4. invite 包里带上：
   * 群基础信息
   * 当前成员摘要
   * 当前 epoch
   * 给该成员包装后的组密钥
   * 最近一段事件日志

### 9.3 接受邀请

被邀请人收到 invite 后：

1. 校验 controller 签名
2. 校验自己是否在邀请列表中
3. 保存群基础信息
4. 保存当前 epoch 密钥
5. 发送 `group_join`

controller 收到 `group_join` 后：

1. 生成 `group_join` 事件
2. 将成员状态改为 `active`
3. 把这条 join 事件 fanout 给其他 active 成员

### 9.4 退群

成员退群流程：

1. 成员自己发 `group_leave`
2. controller 写入 leave 事件
3. controller 生成新 epoch
4. 新 epoch 只发给剩余 active 成员

### 9.5 移除成员

移除成员流程：

1. controller 写入 `group_remove`
2. 立刻生成新 epoch
3. 给剩余 active 成员分发新 epoch 密钥
4. 后续群消息只能用新 epoch 发送

这里“移除成员 -> 立刻 rekey”是强制规则，不建议做成可选。

---

## 10. 数据面详细流程

### 10.1 发送群文本

发送者发送群文本时：

1. 读取本地 `group.current_epoch`
2. 读取对应 `group_epoch_key`
3. 生成 `msg_id`
4. 对明文做 AEAD 加密
5. 构造 `GroupChatText`
6. 对消息头和密文摘要做签名
7. 本地落库
8. fanout 给所有 `active` 成员，自己除外

### 10.2 发送群文件

群文件与单聊文件逻辑一致，但对象换成 group：

* 文件密文按组 epoch 加密
* 元信息带 `file_name / mime_type / file_size`
* 传输仍然是逐成员 fanout
* 单文件大小上限可以先沿用单聊限制

### 10.3 fanout 的本质

这里的 fanout 不是广播协议，而是：

* 对每个成员创建一个投递任务
* 每个任务独立 direct / relay
* 每个任务独立 ACK / 重试

这样最贴合现有 outbox 模型。

### 10.4 群消息包建议结构

```json
{
  "type": "group_chat_text",
  "group_id": "uuid",
  "epoch": 3,
  "msg_id": "uuid",
  "sender_peer_id": "12D3KooW...",
  "sender_seq": 18,
  "ciphertext": "...",
  "sent_at_unix": 1740000000000,
  "signature": "..."
}
```

### 10.5 接收群消息

接收方收到群消息后：

1. 校验 `group_id`
2. 校验发送者当前是否是 active 成员
3. 校验 `epoch` 是否存在
4. 校验签名
5. 校验 `(sender_peer_id, sender_seq)` 是否重放
6. 解密
7. 落库
8. 回 ACK

---

## 11. ACK、重试与投递状态

单聊里消息状态只有一对一，但群聊需要变成“每成员一份投递状态”。

### 11.1 新的状态模型

建议群消息有两层状态：

#### 消息整体状态

* `local_only`
* `partially_sent`
* `partially_delivered`
* `delivered_all`
* `failed_partial`

#### 每成员投递状态

* `pending`
* `sent_to_transport`
* `delivered_remote`
* `queued_for_retry`
* `failed`

### 11.2 ACK 语义

群 ACK 必须带接收者身份：

```json
{
  "type": "group_delivery_ack",
  "group_id": "uuid",
  "msg_id": "uuid",
  "from_peer_id": "receiver",
  "to_peer_id": "sender",
  "acked_at_unix": 1740000000000
}
```

发送端收到 ACK 后，只更新该成员的投递状态，不更新其他成员。

---

## 12. 补同步机制

因为当前系统没有 mailbox，所以群聊必须接受一个现实：

* 如果所有其他成员都不在线，离线节点无法从网络中取回缺失消息

但只要有任一在线成员保留历史，就可以补同步。

### 12.1 建议新增 group sync 协议

`/meshproxy/group/sync/1.0.0`

### 12.2 同步内容分两类

1. 群事件
2. 群消息

### 12.3 群事件同步

群事件可以使用简单顺序游标：

* 本地保存 `last_event_seq`
* 同步时向对方请求 `event_seq > last_event_seq` 的事件

因为 V1 是 controller 单写者，所以这个顺序是稳定的。

### 12.4 群消息同步

群消息不建议依赖单一时间戳排序，建议每个发送者维护自己的 `sender_seq`。

本地同步游标保存：

* `group_id`
* `peer_id -> max_sender_seq`

同步请求示例：

```json
{
  "type": "group_sync_request",
  "group_id": "uuid",
  "last_event_seq": 21,
  "sender_cursors": {
    "peerA": 15,
    "peerB": 8
  }
}
```

对方返回：

* 缺失的群事件
* 所有 `sender_seq` 大于游标的群消息

### 12.5 为什么不用全局消息序号

因为群消息是多节点各自发送，不存在天然全局单调序列。  
如果硬要全局排序，就需要：

* 中心排序者
* 共识
* 或复杂的全序协议

这都不适合当前项目。

所以 V1 采用：

* 成员变更事件有全局顺序
* 消息没有全局顺序，只有“每发送者局部顺序”
* UI 展示时按 `sent_at + sender_seq + 本地接收时间` 做稳定排序

---

## 13. relay-e2e 在群聊里的复用方式

群聊不应该走 SOCKS5，也不应该新造一套完全不同的中继框架。

推荐做法：

* direct：直接开 `/meshproxy/group/control/1.0.0` 或 `/meshproxy/group/msg/1.0.0`
* relay：把 `GroupEnvelope` 封进 `/meshproxy/group/relay-e2e/1.0.0`

它应复用现有 raw tunnel / chat relay-e2e 的几个核心概念：

* `RouteHeader`
* `TunnelID`
* `Path`
* `HopIndex`
* `EncryptedFrame`

但最后一跳不再是 exit，也不是单聊 handler，而是 group handler。

### 13.1 relay 可见性

relay 节点只应看到：

* 上一跳
* 下一跳
* 路由头

不应看到：

* 群明文
* 群 title
* 群成员列表
* 群文件内容

注意：

* relay 仍然能从流量模式推断“你正在向多个 peer 同时发送”
* 这属于流量分析层面，V1 不处理

---

## 14. 存储设计建议

建议在现有 `chat.db` 里新增以下表：

### 14.1 groups

```sql
CREATE TABLE groups (
  group_id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  avatar TEXT NOT NULL DEFAULT '',
  controller_peer_id TEXT NOT NULL,
  current_epoch INTEGER NOT NULL,
  state TEXT NOT NULL,
  last_event_seq INTEGER NOT NULL DEFAULT 0,
  last_message_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

### 14.2 group_members

```sql
CREATE TABLE group_members (
  group_id TEXT NOT NULL,
  peer_id TEXT NOT NULL,
  role TEXT NOT NULL,
  state TEXT NOT NULL,
  invited_by TEXT NOT NULL DEFAULT '',
  joined_epoch INTEGER NOT NULL DEFAULT 0,
  left_epoch INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(group_id, peer_id)
);
```

### 14.3 group_events

```sql
CREATE TABLE group_events (
  event_id TEXT PRIMARY KEY,
  group_id TEXT NOT NULL,
  event_seq INTEGER NOT NULL,
  event_type TEXT NOT NULL,
  actor_peer_id TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  signature BLOB NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(group_id, event_seq)
);
```

### 14.4 group_epochs

```sql
CREATE TABLE group_epochs (
  group_id TEXT NOT NULL,
  epoch INTEGER NOT NULL,
  wrapped_key_for_local BLOB NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY(group_id, epoch)
);
```

### 14.5 group_messages

```sql
CREATE TABLE group_messages (
  msg_id TEXT PRIMARY KEY,
  group_id TEXT NOT NULL,
  epoch INTEGER NOT NULL,
  sender_peer_id TEXT NOT NULL,
  sender_seq INTEGER NOT NULL,
  msg_type TEXT NOT NULL,
  plaintext TEXT NOT NULL,
  file_name TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  file_size INTEGER NOT NULL DEFAULT 0,
  ciphertext_blob BLOB NOT NULL,
  signature BLOB NOT NULL,
  state TEXT NOT NULL,
  sent_at TEXT NOT NULL,
  delivered_summary TEXT NOT NULL DEFAULT ''
);
```

### 14.6 group_message_deliveries

```sql
CREATE TABLE group_message_deliveries (
  msg_id TEXT NOT NULL,
  peer_id TEXT NOT NULL,
  transport_mode TEXT NOT NULL,
  state TEXT NOT NULL,
  retry_count INTEGER NOT NULL DEFAULT 0,
  next_retry_at TEXT NOT NULL DEFAULT '',
  delivered_at TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  PRIMARY KEY(msg_id, peer_id)
);
```

### 14.7 group_sync_cursors

```sql
CREATE TABLE group_sync_cursors (
  group_id TEXT NOT NULL,
  peer_id TEXT NOT NULL,
  max_sender_seq INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(group_id, peer_id)
);
```

---

## 15. API 层建议

本地 API 建议新增：

* `POST /api/v1/groups`
* `GET /api/v1/groups`
* `GET /api/v1/groups/:group_id`
* `POST /api/v1/groups/:group_id/invite`
* `POST /api/v1/groups/:group_id/join`
* `POST /api/v1/groups/:group_id/leave`
* `POST /api/v1/groups/:group_id/remove`
* `GET /api/v1/groups/:group_id/messages`
* `POST /api/v1/groups/:group_id/messages/text`
* `POST /api/v1/groups/:group_id/messages/file`
* `POST /api/v1/groups/:group_id/sync`

如果后面做 UI，群会话列表和单聊会话列表建议分开，但可以共用一套消息面板组件。

---

## 16. 安全规则

### 16.1 强制规则

以下规则建议写死：

* 只有 active 成员才能解密当前 epoch 消息
* 成员减少时必须 rekey
* controller 变更时必须 rekey
* 未拿到当前 epoch 的成员不能发送群消息
* 群消息必须签名
* `(group_id, sender_peer_id, sender_seq)` 必须唯一

### 16.2 重放保护

每个成员本地记录：

* 每个发送者的 `max_sender_seq`

如果收到更小或重复的序号：

* 直接丢弃
* 或标记为重复消息

### 16.3 历史保密

V1 可以做到：

* 新成员默认不自动获得旧 epoch 的历史密钥
* 因此不能直接解密加入前消息

这是一项优点，不是缺点。  
是否给新成员补历史，建议作为显式可选能力，不要默认开启。

---

## 17. 性能与规模边界

### 17.1 成本模型

这个方案的发送成本是：

* 传输 fanout：`O(n)`
* 成员变更 rekey：`O(n)`

其中 `n` 是群成员数。

### 17.2 适用范围

它非常适合：

* 小群
* 中等规模讨论组
* 技术群
* 熟人群

不适合：

* 数百上千人的大频道
* 超高频消息流
* 对流量分析极度敏感的群场景

### 17.3 推荐 V1 限制

建议直接在产品和协议层设限制：

* 群成员上限先设为 `32`
* 后续压测通过后再放宽到 `64`

---

## 18. 分阶段实现建议

### Phase 1：控制面骨架

先做：

* `groups / group_members / group_events / group_epochs`
* `group_create / invite / join / leave / remove`
* controller 单写者模型
* epoch 轮换

目标：

* 能稳定创建群、进群、退群、移人

### Phase 2：群文本

再做：

* `group_chat_text`
* per-member fanout
* per-member ACK
* 本地投递状态汇总

目标：

* 群文本能 direct / relay 地发出去

### Phase 3：群文件

补上：

* `group_chat_file`
* 文件 blob 存储
* 文件 ACK / 重试

### Phase 4：补同步

补：

* `group_sync`
* 事件同步
* 消息游标同步

目标：

* 节点重连后能从其他在线成员补齐缺失内容

### Phase 5：治理增强

后续再考虑：

* controller 转移
* 多 admin
* 历史消息选择性共享
* 更强的 traffic padding
* 更高级的群密钥算法

### 当前任务拆分清单

- [x] 定义群角色、成员状态、控制事件模型
- [x] 设计群聊 SQLite 表结构与 migration 方案
- [x] 设计本地 API 形态与 handler 责任边界
- [x] 实现群聊基础类型草案
- [x] 实现群聊基础表 migration
- [x] 实现群组创建、列表、详情查询
- [x] 实现本地控制面基础操作：邀请、改群名、移除成员、转移 controller
- [x] 接入本地 API：`/api/v1/groups` 及子路由
- [x] 实现群控制事件网络传播
- [x] 实现群 epoch 密钥真正分发与 rekey
- [x] 实现群文本消息发送与 per-member fanout
- [x] 实现群文件消息
- [x] 实现群 ACK / 重试状态汇总
- [x] 实现 group sync 补同步
- [ ] 实现群聊 UI

---

## 19. 为什么这是当前项目最合适的 V1

因为它同时满足了三件事：

1. 保持去中心化
2. 不推翻现有单聊架构
3. 工程复杂度可控

如果现在直接做“完全对称、无控制者、多管理员并发变更、群全序、超大规模”：

* 协议会迅速膨胀
* 存储和同步会变复杂
* 排错成本会非常高
* 很容易做成一个概念正确、工程上很难跑稳的系统

而这个方案的关键取舍是：

* 把难题压到最少
* 先让群聊真正跑起来
* 以后再迭代到更强模型

---

## 20. 最终建议

如果你现在准备开始实现，我建议就按下面这条主线推进：

1. V1 采用“controller 单写者 + 群 epoch 组密钥 + 单条消息 fanout”
2. 组密钥用现有单聊安全路径逐成员分发
3. 消息 direct 优先，失败时 relay-e2e
4. 不做全网 gossip 群消息广播
5. 不做 mailbox，离线补同步依赖其他在线成员

这是当前 meshproxy 最稳、最贴近现有实现、也最容易成功落地的去中心化群聊方案。

---

## 21. Go 结构体草案

这一节的目标不是直接定最终代码，而是给出一套和当前 `internal/chat/types.go` 风格一致的草案，方便后续实现时少走弯路。

### 21.1 建议新增常量

```go
const (
	GroupRoleController = "controller"
	GroupRoleAdmin      = "admin"
	GroupRoleMember     = "member"
)

const (
	GroupStateActive   = "active"
	GroupStateArchived = "archived"
)

const (
	GroupMemberStateInvited = "invited"
	GroupMemberStateActive  = "active"
	GroupMemberStateLeft    = "left"
	GroupMemberStateRemoved = "removed"
)

const (
	GroupEventCreate             = "group_create"
	GroupEventInvite             = "group_invite"
	GroupEventJoin               = "group_join"
	GroupEventLeave              = "group_leave"
	GroupEventRemove             = "group_remove"
	GroupEventTitleUpdate        = "group_title_update"
	GroupEventControllerTransfer = "group_controller_transfer"
	GroupEventEpochRotate        = "group_epoch_rotate"
)

const (
	GroupMessageTypeText   = "group_chat_text"
	GroupMessageTypeFile   = "group_chat_file"
	GroupMessageTypeAck    = "group_delivery_ack"
	GroupMessageTypeRevoke = "group_message_revoke"
)

const (
	GroupDeliveryPending          = "pending"
	GroupDeliverySentToTransport  = "sent_to_transport"
	GroupDeliveryDeliveredRemote  = "delivered_remote"
	GroupDeliveryQueuedForRetry   = "queued_for_retry"
	GroupDeliveryFailed           = "failed"
)
```

### 21.2 本地模型结构

这些结构主要对应 SQLite 里的本地状态。

```go
type Group struct {
	GroupID          string    `json:"group_id"`
	Title            string    `json:"title"`
	Avatar           string    `json:"avatar"`
	ControllerPeerID string    `json:"controller_peer_id"`
	CurrentEpoch     uint64    `json:"current_epoch"`
	State            string    `json:"state"`
	LastEventSeq     uint64    `json:"last_event_seq"`
	LastMessageAt    time.Time `json:"last_message_at"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type GroupMember struct {
	GroupID     string    `json:"group_id"`
	PeerID      string    `json:"peer_id"`
	Role        string    `json:"role"`
	State       string    `json:"state"`
	InvitedBy   string    `json:"invited_by"`
	JoinedEpoch uint64    `json:"joined_epoch"`
	LeftEpoch   uint64    `json:"left_epoch"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type GroupEvent struct {
	EventID      string    `json:"event_id"`
	GroupID      string    `json:"group_id"`
	EventSeq     uint64    `json:"event_seq"`
	EventType    string    `json:"event_type"`
	ActorPeerID  string    `json:"actor_peer_id"`
	PayloadJSON  string    `json:"payload_json"`
	Signature    []byte    `json:"signature"`
	CreatedAt    time.Time `json:"created_at"`
}

type GroupEpoch struct {
	GroupID             string    `json:"group_id"`
	Epoch               uint64    `json:"epoch"`
	WrappedKeyForLocal  []byte    `json:"wrapped_key_for_local"`
	CreatedAt           time.Time `json:"created_at"`
}

type GroupMessage struct {
	MsgID         string    `json:"msg_id"`
	GroupID       string    `json:"group_id"`
	Epoch         uint64    `json:"epoch"`
	SenderPeerID  string    `json:"sender_peer_id"`
	SenderSeq     uint64    `json:"sender_seq"`
	MsgType       string    `json:"msg_type"`
	Plaintext     string    `json:"plaintext"`
	FileName      string    `json:"file_name,omitempty"`
	MIMEType      string    `json:"mime_type,omitempty"`
	FileSize      int64     `json:"file_size,omitempty"`
	State         string    `json:"state"`
	CreatedAt     time.Time `json:"created_at"`
}

type GroupMessageDelivery struct {
	MsgID          string    `json:"msg_id"`
	PeerID         string    `json:"peer_id"`
	TransportMode  string    `json:"transport_mode"`
	State          string    `json:"state"`
	RetryCount     int       `json:"retry_count"`
	NextRetryAt    time.Time `json:"next_retry_at,omitempty"`
	DeliveredAt    time.Time `json:"delivered_at,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type GroupSyncCursor struct {
	GroupID      string    `json:"group_id"`
	PeerID       string    `json:"peer_id"`
	MaxSenderSeq uint64    `json:"max_sender_seq"`
	UpdatedAt    time.Time `json:"updated_at"`
}
```

### 21.3 协议层 envelope 草案

建议群聊控制面和数据面都用 envelope 包一层，再根据 `EventType` 或 `MsgType` 分发。

```go
type GroupControlEnvelope struct {
	Type          string          `json:"type"`
	EventType     string          `json:"event_type"`
	GroupID       string          `json:"group_id"`
	EventID       string          `json:"event_id"`
	EventSeq      uint64          `json:"event_seq"`
	ActorPeerID   string          `json:"actor_peer_id"`
	CreatedAtUnix int64           `json:"created_at_unix"`
	Payload       json.RawMessage `json:"payload"`
	Signature     []byte          `json:"signature"`
}

type GroupMessageEnvelope struct {
	Type          string `json:"type"`
	GroupID       string `json:"group_id"`
	Epoch         uint64 `json:"epoch"`
	MsgID         string `json:"msg_id"`
	SenderPeerID  string `json:"sender_peer_id"`
	SenderSeq     uint64 `json:"sender_seq"`
	Ciphertext    []byte `json:"ciphertext"`
	SentAtUnix    int64  `json:"sent_at_unix"`
	Signature     []byte `json:"signature"`
}
```

如果后续想支持多个消息子类型，也可以在 `GroupMessageEnvelope` 外再包一层：

```go
type GroupWireEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}
```

### 21.4 各控制事件 payload 草案

```go
type GroupCreatePayload struct {
	Title            string   `json:"title"`
	ControllerPeerID string   `json:"controller_peer_id"`
	InitialMembers   []string `json:"initial_members"`
	Epoch            uint64   `json:"epoch"`
}

type GroupInvitePayload struct {
	InviteePeerID   string `json:"invitee_peer_id"`
	Role            string `json:"role"`
	InviteText      string `json:"invite_text"`
	Epoch           uint64 `json:"epoch"`
	WrappedGroupKey []byte `json:"wrapped_group_key"`
}

type GroupJoinPayload struct {
	JoinerPeerID   string `json:"joiner_peer_id"`
	AcceptedEpoch  uint64 `json:"accepted_epoch"`
}

type GroupLeavePayload struct {
	LeaverPeerID string `json:"leaver_peer_id"`
	Reason       string `json:"reason"`
}

type GroupRemovePayload struct {
	TargetPeerID string `json:"target_peer_id"`
	Reason       string `json:"reason"`
}

type GroupTitleUpdatePayload struct {
	Title string `json:"title"`
}

type GroupControllerTransferPayload struct {
	FromPeerID string `json:"from_peer_id"`
	ToPeerID   string `json:"to_peer_id"`
}

type GroupEpochRotatePayload struct {
	OldEpoch    uint64            `json:"old_epoch"`
	NewEpoch    uint64            `json:"new_epoch"`
	Members     []string          `json:"members"`
	WrappedKeys map[string][]byte `json:"wrapped_keys"`
}
```

### 21.5 数据消息草案

```go
type GroupChatText struct {
	Type          string `json:"type"`
	GroupID       string `json:"group_id"`
	Epoch         uint64 `json:"epoch"`
	MsgID         string `json:"msg_id"`
	SenderPeerID  string `json:"sender_peer_id"`
	SenderSeq     uint64 `json:"sender_seq"`
	Ciphertext    []byte `json:"ciphertext"`
	SentAtUnix    int64  `json:"sent_at_unix"`
	Signature     []byte `json:"signature"`
}

type GroupChatFile struct {
	Type          string `json:"type"`
	GroupID       string `json:"group_id"`
	Epoch         uint64 `json:"epoch"`
	MsgID         string `json:"msg_id"`
	SenderPeerID  string `json:"sender_peer_id"`
	SenderSeq     uint64 `json:"sender_seq"`
	FileName      string `json:"file_name"`
	MIMEType      string `json:"mime_type"`
	FileSize      int64  `json:"file_size"`
	Ciphertext    []byte `json:"ciphertext"`
	SentAtUnix    int64  `json:"sent_at_unix"`
	Signature     []byte `json:"signature"`
}

type GroupDeliveryAck struct {
	Type          string `json:"type"`
	GroupID       string `json:"group_id"`
	MsgID         string `json:"msg_id"`
	FromPeerID    string `json:"from_peer_id"`
	ToPeerID      string `json:"to_peer_id"`
	AckedAtUnix   int64  `json:"acked_at_unix"`
}

type GroupMessageRevoke struct {
	Type          string `json:"type"`
	GroupID       string `json:"group_id"`
	MsgID         string `json:"msg_id"`
	FromPeerID    string `json:"from_peer_id"`
	RevokedAtUnix int64  `json:"revoked_at_unix"`
}
```

### 21.6 补同步消息草案

```go
type GroupSyncRequest struct {
	Type          string            `json:"type"`
	GroupID       string            `json:"group_id"`
	LastEventSeq  uint64            `json:"last_event_seq"`
	SenderCursors map[string]uint64 `json:"sender_cursors"`
}

type GroupSyncResponse struct {
	Type          string                 `json:"type"`
	GroupID       string                 `json:"group_id"`
	Events        []GroupControlEnvelope `json:"events"`
	Messages      []GroupMessageEnvelope `json:"messages"`
}
```

---

## 22. 服务层职责拆分建议

如果后面开始实现，建议不要把所有群逻辑全塞进现有 `chat.Service` 的一个大文件里，不然后续会非常难维护。

### 22.1 建议新增能力边界

建议至少拆成这几类职责：

* `GroupService`
* `GroupStore`
* `GroupCrypto`
* `GroupSync`

### 22.2 GroupService

负责：

* 创建群
* 邀请成员
* 接收控制事件
* 发送群消息
* fanout 投递
* ACK 处理
* 调用 store 和 crypto

建议方法草案：

```go
type GroupService struct {
	ctx       context.Context
	host      host.Host
	routing   corerouting.Routing
	discovery *discovery.Store
	store     *Store
	localPeer string
}

func (s *GroupService) CreateGroup(title string, members []string) (Group, error)
func (s *GroupService) InviteMember(groupID, peerID string) error
func (s *GroupService) AcceptInvite(groupID string) error
func (s *GroupService) LeaveGroup(groupID string) error
func (s *GroupService) RemoveMember(groupID, peerID string) error
func (s *GroupService) SendGroupText(groupID, plaintext string) (GroupMessage, error)
func (s *GroupService) SendGroupFile(groupID, fileName, mimeType string, data []byte) (GroupMessage, error)
func (s *GroupService) SyncGroup(groupID, fromPeerID string) error
```

### 22.3 GroupStore

负责：

* `groups`
* `group_members`
* `group_events`
* `group_epochs`
* `group_messages`
* `group_message_deliveries`
* `group_sync_cursors`

也就是：

* 所有群状态落库和查询都放这里
* 不要把 SQL 散落在 handler 和 service 中

### 22.4 GroupCrypto

负责：

* 生成 `group_epoch_key`
* 包装和解包 `wrapped_group_key`
* 群消息 AEAD 加解密
* 事件和消息签名验签

这部分建议和现有单聊加密能力复用底层 primitives，但逻辑上单独抽出来，避免：

* 单聊会话密钥逻辑和群 epoch 逻辑互相污染

### 22.5 GroupSync

负责：

* 构造 `GroupSyncRequest`
* 处理 `GroupSyncResponse`
* 发现 `event_seq` 缺口
* 发现 `sender_seq` 缺口
* 从在线成员补齐消息

---

## 23. 与现有 chat.Service 的映射建议

当前项目已经有单聊 `chat.Service`，所以群聊实现时最容易犯的错误是：

* 直接复制单聊代码一份
* 然后在各处加 `if group` 分支

这会很快把代码带进维护地狱。

### 23.1 推荐复用的部分

这些能力建议尽量复用：

* `ensurePeerConnected`
* direct 优先、失败 relay 的发送策略
* `sendEnvelope`
* 文件大小限制
* 本地 outbox / retry loop
* relay-e2e 的 transport 适配

### 23.2 不建议直接复用的数据结构

这些部分不要硬改成兼容群聊：

* `Conversation`
* `Message`
* `sessionState`
* 一对一 `delivery_ack`

原因是这些类型天然绑定“一对一语义”，强行复用会让代码越来越扭曲。

### 23.3 推荐的实现关系

建议关系是：

* 单聊继续保留在 `chat.Service`
* 群聊新增 `group.Service`
* 底层 transport、公钥、peer 发现、relay 选择等能力抽共享 helper

这样更干净，也方便以后单独演进群聊协议。

---

## 24. 实现时最容易踩的坑

### 24.1 把 group_invite 当成最终入群

错误做法：

* controller 发完 invite 就把对方视作 active

正确做法：

* 只有 `group_join` 正式事件提交后，对方才是 active 成员

### 24.2 成员变更后不立刻 rekey

这是最危险的坑之一。  
如果移人后不 rekey，被移除成员理论上还能继续解密后续消息。

### 24.3 用时间戳代替 event_seq

时间戳不能保证群控制事件的唯一顺序，尤其是多节点网络环境下更不稳。

V1 一定要：

* controller 分配正式 `event_seq`

### 24.4 群消息直接写全局顺序号

群消息来自不同发送者，天然没有全局单调序。  
硬做全局号只会引出更多同步问题。

### 24.5 把群消息做成 gossip 广播

短期看简单，长期几乎一定会把：

* 权限控制
* ACK
* 重试
* 去重
* 历史同步

全都搅复杂。

这也是为什么文档一直强调 fanout 而不是广播。

---

## 25. 数据库 Migration 草案

这一节给的是“第一版可落地”的 migration 思路，目标是：

* 不破坏现有单聊表
* 群聊数据独立建表
* 后续可以逐步扩展，不需要反复大改 schema

### 25.1 migration 原则

建议遵守这几个原则：

* 群聊和单聊分表，不把群字段硬塞进现有 `conversations/messages`
* 事件表和消息表分开
* 成员表和投递状态表分开
* 所有 group 相关表都以 `group_` 前缀命名

### 25.2 建表顺序建议

建议按下面顺序执行：

1. `groups`
2. `group_members`
3. `group_events`
4. `group_epochs`
5. `group_messages`
6. `group_message_deliveries`
7. `group_sync_cursors`

这样做的原因是：

* 先有主实体
* 再有成员和事件
* 再有密钥 epoch
* 最后补消息和同步状态

### 25.3 groups

```sql
CREATE TABLE IF NOT EXISTS groups (
  group_id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  avatar TEXT NOT NULL DEFAULT '',
  controller_peer_id TEXT NOT NULL,
  current_epoch INTEGER NOT NULL,
  state TEXT NOT NULL,
  last_event_seq INTEGER NOT NULL DEFAULT 0,
  last_message_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_groups_updated_at
ON groups(updated_at DESC);
```

### 25.4 group_members

```sql
CREATE TABLE IF NOT EXISTS group_members (
  group_id TEXT NOT NULL,
  peer_id TEXT NOT NULL,
  role TEXT NOT NULL,
  state TEXT NOT NULL,
  invited_by TEXT NOT NULL DEFAULT '',
  joined_epoch INTEGER NOT NULL DEFAULT 0,
  left_epoch INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(group_id, peer_id)
);

CREATE INDEX IF NOT EXISTS idx_group_members_state
ON group_members(group_id, state);
```

### 25.5 group_events

```sql
CREATE TABLE IF NOT EXISTS group_events (
  event_id TEXT PRIMARY KEY,
  group_id TEXT NOT NULL,
  event_seq INTEGER NOT NULL,
  event_type TEXT NOT NULL,
  actor_peer_id TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  signature BLOB NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(group_id, event_seq)
);

CREATE INDEX IF NOT EXISTS idx_group_events_group_seq
ON group_events(group_id, event_seq);
```

### 25.6 group_epochs

```sql
CREATE TABLE IF NOT EXISTS group_epochs (
  group_id TEXT NOT NULL,
  epoch INTEGER NOT NULL,
  wrapped_key_for_local BLOB NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY(group_id, epoch)
);
```

如果后续你想支持“历史密钥多版本管理”或“多设备本地封装”，可以再扩成：

* `key_id`
* `wrapped_key_scheme`
* `wrapped_key_for_device`

但 V1 先不必引入这些复杂度。

### 25.7 group_messages

```sql
CREATE TABLE IF NOT EXISTS group_messages (
  msg_id TEXT PRIMARY KEY,
  group_id TEXT NOT NULL,
  epoch INTEGER NOT NULL,
  sender_peer_id TEXT NOT NULL,
  sender_seq INTEGER NOT NULL,
  msg_type TEXT NOT NULL,
  plaintext TEXT NOT NULL,
  file_name TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  file_size INTEGER NOT NULL DEFAULT 0,
  ciphertext_blob BLOB NOT NULL,
  signature BLOB NOT NULL,
  state TEXT NOT NULL,
  sent_at TEXT NOT NULL,
  delivered_summary TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_group_messages_sender_seq
ON group_messages(group_id, sender_peer_id, sender_seq);

CREATE INDEX IF NOT EXISTS idx_group_messages_group_time
ON group_messages(group_id, sent_at DESC);
```

这里建议保留：

* `msg_id` 作为全局主键
* `(group_id, sender_peer_id, sender_seq)` 作为重放保护和幂等约束

### 25.8 group_message_deliveries

```sql
CREATE TABLE IF NOT EXISTS group_message_deliveries (
  msg_id TEXT NOT NULL,
  peer_id TEXT NOT NULL,
  transport_mode TEXT NOT NULL,
  state TEXT NOT NULL,
  retry_count INTEGER NOT NULL DEFAULT 0,
  next_retry_at TEXT NOT NULL DEFAULT '',
  delivered_at TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  PRIMARY KEY(msg_id, peer_id)
);

CREATE INDEX IF NOT EXISTS idx_group_deliveries_retry
ON group_message_deliveries(state, next_retry_at);
```

这个表是群聊可靠投递的关键。  
没有它，群消息 fanout 之后几乎没法把 per-member 状态管清楚。

### 25.9 group_sync_cursors

```sql
CREATE TABLE IF NOT EXISTS group_sync_cursors (
  group_id TEXT NOT NULL,
  peer_id TEXT NOT NULL,
  max_sender_seq INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(group_id, peer_id)
);
```

这张表的语义是：

* 我已经同步到某个发送者的哪条消息了

注意它不是“成员资料表”，而是“本地同步游标表”。

### 25.10 可选补充表

如果你后面要支持更完整的申请流，可以补一张：

```sql
CREATE TABLE IF NOT EXISTS group_join_requests (
  request_id TEXT PRIMARY KEY,
  group_id TEXT NOT NULL,
  peer_id TEXT NOT NULL,
  state TEXT NOT NULL,
  message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

但如果 V1 仍然是“controller 主动邀请”，这张表可以先不做。

### 25.11 migrate 函数建议

如果后续实现风格继续跟现有 `Store.migrate()` 保持一致，建议新增：

```go
func (s *Store) ensureGroupTables() error
func (s *Store) ensureGroupMessageIndexes() error
func (s *Store) ensureGroupDeliveryIndexes() error
```

不建议把所有 SQL 全塞进一个超长 `stmts := []string{}` 里，否则群聊继续扩展时会越来越难维护。

---

## 26. 本地 API 请求/响应草案

这一节默认沿用当前本地 HTTP API 风格，重点是把群聊接口形态先定下来。

### 26.1 创建群

`POST /api/v1/groups`

请求：

```json
{
  "title": "meshproxy dev",
  "members": [
    "12D3KooW-peerA",
    "12D3KooW-peerB"
  ]
}
```

响应：

```json
{
  "group_id": "uuid",
  "title": "meshproxy dev",
  "controller_peer_id": "12D3KooW-local",
  "current_epoch": 1,
  "state": "active",
  "created_at": "2026-03-16T10:00:00Z",
  "updated_at": "2026-03-16T10:00:00Z"
}
```

### 26.2 群列表

`GET /api/v1/groups`

响应：

```json
[
  {
    "group_id": "uuid",
    "title": "meshproxy dev",
    "controller_peer_id": "12D3KooW-local",
    "current_epoch": 2,
    "state": "active",
    "last_message_at": "2026-03-16T10:20:00Z",
    "member_count": 5
  }
]
```

### 26.3 群详情

`GET /api/v1/groups/:group_id`

响应：

```json
{
  "group": {
    "group_id": "uuid",
    "title": "meshproxy dev",
    "controller_peer_id": "12D3KooW-local",
    "current_epoch": 2,
    "state": "active"
  },
  "members": [
    {
      "peer_id": "12D3KooW-local",
      "role": "controller",
      "state": "active"
    },
    {
      "peer_id": "12D3KooW-peerA",
      "role": "member",
      "state": "active"
    }
  ]
}
```

### 26.4 邀请成员

`POST /api/v1/groups/:group_id/invite`

请求：

```json
{
  "peer_id": "12D3KooW-peerC",
  "role": "member",
  "invite_text": "join us"
}
```

响应：

```json
{
  "ok": true,
  "event_type": "group_invite",
  "group_id": "uuid",
  "peer_id": "12D3KooW-peerC"
}
```

### 26.5 接受邀请

`POST /api/v1/groups/:group_id/join`

请求：

```json
{
  "accepted": true
}
```

响应：

```json
{
  "ok": true,
  "group_id": "uuid",
  "state": "active"
}
```

如果你后续要支持拒绝邀请，也可以扩成：

* `accepted: false`
* 或单独做 `POST /api/v1/groups/:group_id/reject`

### 26.6 主动退群

`POST /api/v1/groups/:group_id/leave`

请求：

```json
{
  "reason": "left_by_self"
}
```

响应：

```json
{
  "ok": true,
  "group_id": "uuid"
}
```

### 26.7 移除成员

`POST /api/v1/groups/:group_id/remove`

请求：

```json
{
  "peer_id": "12D3KooW-peerC",
  "reason": "policy_violation"
}
```

响应：

```json
{
  "ok": true,
  "group_id": "uuid",
  "removed_peer_id": "12D3KooW-peerC"
}
```

### 26.8 修改群名

`POST /api/v1/groups/:group_id/title`

请求：

```json
{
  "title": "meshproxy core team"
}
```

响应：

```json
{
  "ok": true,
  "group_id": "uuid",
  "title": "meshproxy core team"
}
```

### 26.9 转移 controller

`POST /api/v1/groups/:group_id/controller`

请求：

```json
{
  "peer_id": "12D3KooW-peerA"
}
```

响应：

```json
{
  "ok": true,
  "group_id": "uuid",
  "controller_peer_id": "12D3KooW-peerA"
}
```

### 26.10 获取群消息

`GET /api/v1/groups/:group_id/messages`

响应：

```json
[
  {
    "msg_id": "uuid",
    "group_id": "uuid",
    "epoch": 2,
    "sender_peer_id": "12D3KooW-peerA",
    "sender_seq": 7,
    "msg_type": "group_chat_text",
    "plaintext": "hello group",
    "state": "partially_delivered",
    "created_at": "2026-03-16T10:30:00Z"
  }
]
```

如果需要更好的 UI 支撑，建议加上：

* `deliveries`
* `member_count`
* `delivered_count`

### 26.11 发送群文本

`POST /api/v1/groups/:group_id/messages/text`

请求：

```json
{
  "plaintext": "hello group"
}
```

响应：

```json
{
  "msg_id": "uuid",
  "group_id": "uuid",
  "state": "local_only",
  "created_at": "2026-03-16T10:30:00Z"
}
```

### 26.12 发送群文件

`POST /api/v1/groups/:group_id/messages/file`

请求：

* `multipart/form-data`
* `file`
* 可选 `caption`

响应：

```json
{
  "msg_id": "uuid",
  "group_id": "uuid",
  "msg_type": "group_chat_file",
  "file_name": "demo.png",
  "file_size": 12345
}
```

### 26.13 触发补同步

`POST /api/v1/groups/:group_id/sync`

请求：

```json
{
  "from_peer_id": "12D3KooW-peerA"
}
```

响应：

```json
{
  "ok": true,
  "group_id": "uuid",
  "from_peer_id": "12D3KooW-peerA"
}
```

### 26.14 错误返回建议

建议继续沿用当前本地 API 常见风格：

```json
{
  "error": "only controller can remove members"
}
```

群聊里比较常见的错误包括：

* `group not found`
* `peer is not an active member`
* `only controller can invite members`
* `local peer does not have current group epoch key`
* `event sequence gap detected`
* `group sync source is offline`

---

## 27. Handler 与处理流程草案

如果开始写代码，建议群聊至少有 4 类 handler：

* control handler
* message handler
* ack handler
* sync handler

### 27.1 control handler

负责：

* 接收 `GroupControlEnvelope`
* 验签
* 检查 `event_seq`
* 更新本地群状态
* 必要时触发 epoch 更新

建议骨架：

```go
func (s *GroupService) handleGroupControlStream(str network.Stream)
func (s *GroupService) handleGroupControlEnvelope(env GroupControlEnvelope) error
```

### 27.2 message handler

负责：

* 接收 `GroupChatText / GroupChatFile`
* 验签
* 查找本地 epoch key
* 解密
* 落库
* 回 `GroupDeliveryAck`

建议骨架：

```go
func (s *GroupService) handleGroupMessageStream(str network.Stream)
func (s *GroupService) handleGroupMessage(env GroupMessageEnvelope) error
```

### 27.3 ack handler

负责：

* 更新 `group_message_deliveries`
* 汇总消息整体状态

建议骨架：

```go
func (s *GroupService) handleGroupAckStream(str network.Stream)
func (s *GroupService) handleGroupAck(ack GroupDeliveryAck) error
```

### 27.4 sync handler

负责：

* 收到同步请求
* 查询缺失事件
* 查询缺失消息
* 打包返回

建议骨架：

```go
func (s *GroupService) handleGroupSyncStream(str network.Stream)
func (s *GroupService) handleGroupSyncRequest(req GroupSyncRequest) (GroupSyncResponse, error)
```

### 27.5 发送群文本的内部流程

建议内部流程固定为：

1. 校验本地是否为 active 成员
2. 读取当前 epoch 和 group key
3. 分配 `sender_seq`
4. 本地加密并签名
5. 写入 `group_messages`
6. 为每个成员初始化 `group_message_deliveries`
7. fanout 发送
8. 后台重试失败成员

### 27.6 处理控制事件的内部流程

建议内部流程固定为：

1. 解析 envelope
2. 校验签名
3. 检查 `group_id`
4. 检查 `event_seq`
5. 根据 `event_type` 做状态变更
6. 如果涉及新 epoch，先保存新密钥再切换当前 epoch
7. 更新 `last_event_seq`

这里第 6 步非常重要。  
不要先切 `current_epoch`，再尝试保存密钥；否则本地会进入“知道新 epoch，但没有新 key”的坏状态。
