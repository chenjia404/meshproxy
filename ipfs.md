目标已经收窄为：

* **只做原型**
* **复用现有 libp2p host**
* **兼容公网标准 IPFS CID**
* **能和 Kubo 互通**
* **提供标准 IPFS Gateway 风格读取能力**
* **提供最小写入接口**
* **暂不设计聊天系统如何使用**

`boxo` 是官方的 Go IPFS SDK；UnixFS 是 IPFS 文件系统默认依赖的数据格式；`coreiface` 提供 IPFS Core API 接口；`merkledag` 提供 DAG 服务；Kubo 是 Go 版 IPFS 的主要实现。([Go Packages][1])

---

# 文档标题

**Embedded IPFS Gateway Prototype on Shared libp2p Host (Go + boxo)**

---

# 1. 项目目标

实现一个嵌入式 IPFS 子系统，运行在现有 Go 聊天节点进程内，复用同一个 libp2p host，具备以下能力：

1. 使用标准 CID 和 UnixFS 导入文件。
2. 使用本地持久化 blockstore/datastore 保存内容。
3. 通过 Bitswap + Routing 与公网 Kubo 节点互通。
4. 暴露最小 HTTP 写入接口：

   * `POST /api/ipfs/add`
   * `POST /api/ipfs/add-dir`（可选，首版可占位）
   * `POST /api/ipfs/pin/{cid}`
   * `DELETE /api/ipfs/pin/{cid}`
5. 暴露标准 Gateway 风格读取接口：

   * `GET /ipfs/{cid}`
6. 复用现有 libp2p host，不创建第二个 libp2p host。
7. 不实现聊天业务耦合逻辑。
8. 不实现 IPNS。
9. 不实现复杂权限控制。
10. 不暴露 Kubo RPC 兼容接口。

Kubo 文档明确区分了 RPC API 和 HTTP Gateway：RPC 是管理员级接口，默认只绑定 localhost，不应暴露到公网；Gateway 才是面向浏览器和公网消费的接口。([docs.ipfs.tech][2])

---

# 2. 非目标

以下内容不在本次原型范围内：

1. 不实现完整 Kubo 节点行为。
2. 不实现 MFS。
3. 不实现 IPNS 发布/解析。
4. 不实现高级 Delegated Routing。
5. 不实现聊天消息与 CID 的绑定。
6. 不实现端到端加密附件设计。
7. 不实现多租户 ACL。
8. 不实现后台 GC 策略优化。
9. 不实现公网可写 Gateway。
10. 不实现 CAR 导入导出。

---

# 3. 技术原则

1. **共享一个 libp2p host**
   原有聊天节点的 host 是唯一 host。IPFS 子系统只挂接其网络能力，不单独创建 host。

2. **标准 UnixFS 导入**
   文件和目录必须使用 UnixFS importer/exporter。UnixFS 是 IPFS 文件系统默认假设的数据格式。([Go Packages][3])

3. **标准 CID / DAG / Blockstore**
   不自定义文件格式，不自定义“文件 = 单块”的协议语义。

4. **与 Kubo 互通优先**
   只要 Kubo 能 add 的普通文件，本原型也应该能 add，并通过 Gateway 读取；只要本原型 add 出来的 CID，Kubo 也应能通过标准网络路径获取，前提是双方网络可达且内容已被提供。

5. **读取接口优先做 Gateway**
   Gateway 是对外读取接口主形态。Kubo/HTTP Gateway 模式是官方认可的公开消费接口。([docs.ipfs.tech][4])

6. **写入接口只做服务内 API**
   通过业务 HTTP API 写入，不做 public writable gateway。

---

# 4. 建议依赖

以下为建议依赖方向，代码生成时按当前稳定版本解析：

* `github.com/libp2p/go-libp2p`
* `github.com/ipfs/boxo`
* `github.com/ipfs/boxo/blockstore`
* `github.com/ipfs/boxo/blockservice`
* `github.com/ipfs/boxo/ipld/merkledag`
* `github.com/ipfs/boxo/ipld/unixfs`
* `github.com/ipfs/boxo/coreiface`
* `github.com/ipfs/go-cid`
* `github.com/ipfs/go-datastore`
* `github.com/ipfs/go-datastore/sync`
* `github.com/ipfs/go-ds-leveldb` 或 `badger`
* `github.com/ipfs/kubo/core/coreiface/options`（如需要部分兼容选项结构）
* `github.com/ipfs/kubo/client/rpc` 仅用于测试互通，不用于主实现

`boxo` 是官方 Go IPFS SDK，`coreiface` 是 IPFS Core API 接口集合，`merkledag` 是 DAG 服务实现相关包。([Go Packages][1])

---

# 5. 总体架构

```text
+----------------------------------------------------------+
|                    Existing Chat Process                 |
|----------------------------------------------------------|
| Shared libp2p Host                                       |
|  - identity                                              |
|  - transports                                            |
|  - security                                              |
|  - muxers                                                |
|  - relay / hole punching / conn manager                  |
|                                                          |
| Existing Chat Protocols                                  |
|  - private chat                                          |
|  - group chat                                            |
|  - sync                                                  |
|                                                          |
| Embedded IPFS Subsystem                                  |
|  - Datastore                                             |
|  - Blockstore                                            |
|  - Routing                                               |
|  - Bitswap                                               |
|  - BlockService                                          |
|  - DAGService                                            |
|  - UnixFS importer/exporter                              |
|  - Pin service                                           |
|  - Gateway backend                                       |
|                                                          |
| HTTP Server                                              |
|  - /api/ipfs/add                                         |
|  - /api/ipfs/pin/{cid}                                   |
|  - /ipfs/{cid}                                           |
+----------------------------------------------------------+
```

---

# 6. 模块拆分

## 6.1 `internal/ipfsnode`

负责 IPFS 子系统生命周期。

职责：

* 接收共享 host
* 初始化 datastore
* 初始化 blockstore
* 初始化 routing
* 初始化 bitswap
* 初始化 blockservice / dagservice
* 初始化 UnixFS importer/exporter
* 初始化 gateway backend
* 暴露 `IPFSService`

---

## 6.2 `internal/ipfsstore`

负责本地持久化。

职责：

* 创建 `data/ipfs/datastore`
* 创建 blockstore
* 封装 datastore 打开与关闭
* 提供 repo layout 初始化

---

## 6.3 `internal/ipfsunixfs`

负责文件和目录导入导出。

职责：

* `AddFile`
* `AddDir`
* `Cat`
* `Get`
* `Stat`

UnixFS 是文件系统默认使用的数据格式。([Go Packages][3])

---

## 6.4 `internal/ipfsrouting`

负责内容发现。

职责：

* 初始化 DHT 或路由接口
* 对新内容触发 Provide
* 查询 providers

IPFS 文档中将 Amino DHT 和 Delegated Routing 归类为现代内容路由体系的一部分。([docs.ipfs.tech][5])

---

## 6.5 `internal/ipfsgateway`

负责 `/ipfs/{cid}`。

职责：

* 封装 gateway backend
* 注册 HTTP route
* 支持只读 gateway
* 支持 range / content-type / disposition 的基础能力

`boxo` 生态里存在 gateway 参考实现，Rainbow 也是基于 Boxo 的专门 Go Gateway。([Go Packages][6])

---

## 6.6 `internal/ipfspin`

负责 pin/unpin 状态。

职责：

* recursive pin
* direct pin
* pin state 查询
* 与 GC 联动（首版可先不做真实 GC）

---

## 6.7 `internal/api`

负责 HTTP API。

职责：

* `POST /api/ipfs/add`
* `POST /api/ipfs/add-dir`（首版可返回 not implemented）
* `POST /api/ipfs/pin/{cid}`
* `DELETE /api/ipfs/pin/{cid}`
* `GET /api/ipfs/stat/{cid}`
* `GET /ipfs/{cid}`

---

# 7. 目录结构

```text
cmd/chatnode/
  main.go

internal/p2p/
  host.go

internal/ipfsnode/
  node.go
  config.go
  lifecycle.go
  interfaces.go

internal/ipfsstore/
  datastore.go
  blockstore.go

internal/ipfsrouting/
  routing.go
  provide.go

internal/ipfsunixfs/
  add.go
  add_dir.go
  cat.go
  get.go
  stat.go

internal/ipfspin/
  pin.go
  state.go

internal/ipfsgateway/
  gateway.go
  backend.go

internal/api/
  ipfs_handlers.go
  gateway_routes.go

internal/config/
  config.go
```

---

# 8. 配置设计

```go
type IPFSConfig struct {
    Enabled bool

    DataDir string

    // Gateway
    GatewayEnabled bool
    GatewayListenAddr string
    GatewayWritable bool // always false in v1

    // API
    APIEnabled bool
    APIListenAddr string

    // Storage
    DatastoreType string // "leveldb" or "badger"
    BlockstoreNoSync bool

    // Import
    Chunker string           // default: "size-262144"
    RawLeaves bool           // default: true
    CIDVersion int           // default: 1
    HashFunction string      // default: "sha2-256"

    // Provide / routing
    AutoProvide bool
    ReprovideIntervalSeconds int
    RoutingMode string // "dht-client" for v1

    // Fetch
    FetchTimeoutSeconds int

    // Pin
    AutoPinOnAdd bool
}
```

默认值建议：

* `Chunker=size-262144`
* `RawLeaves=true`
* `CIDVersion=1`
* `HashFunction=sha2-256`
* `RoutingMode=dht-client`
* `AutoProvide=true`
* `AutoPinOnAdd=true`

---

# 9. 核心接口

## 9.1 服务接口

```go
type IPFSService interface {
    AddFile(ctx context.Context, r io.Reader, opt AddFileOptions) (cid.Cid, error)
    AddDir(ctx context.Context, path string, opt AddDirOptions) (cid.Cid, error)

    Cat(ctx context.Context, c cid.Cid) (io.ReadCloser, error)
    Get(ctx context.Context, c cid.Cid, w io.Writer) error
    Stat(ctx context.Context, c cid.Cid) (*ObjectStat, error)

    Pin(ctx context.Context, c cid.Cid, recursive bool) error
    Unpin(ctx context.Context, c cid.Cid, recursive bool) error
    IsPinned(ctx context.Context, c cid.Cid) (bool, error)

    Provide(ctx context.Context, c cid.Cid, recursive bool) error
    HasLocal(ctx context.Context, c cid.Cid) (bool, error)
}
```

## 9.2 Add 选项

```go
type AddFileOptions struct {
    Filename   string
    RawLeaves  bool
    CIDVersion int
    Chunker    string
    Pin        bool
}

type AddDirOptions struct {
    Wrap       bool
    RawLeaves  bool
    CIDVersion int
    Chunker    string
    Pin        bool
}

type ObjectStat struct {
    CID       string
    Size      int64
    NumLinks  int
    Local     bool
    Pinned    bool
}
```

---

# 10. 生命周期与初始化顺序

启动时必须按以下顺序初始化：

1. 加载业务配置
2. 创建或获取现有共享 libp2p host
3. 初始化 datastore
4. 初始化 blockstore
5. 初始化 routing
6. 初始化 bitswap
7. 初始化 blockservice
8. 初始化 DAGService
9. 初始化 UnixFS add/cat/get 服务
10. 初始化 pin service
11. 初始化 gateway backend
12. 注册 HTTP API 与 gateway 路由
13. 启动 HTTP server

关键原则：

* `IPFSNode` 接受 `host.Host` 作为依赖注入参数
* 不允许内部调用 `libp2p.New()` 创建第二个 host

---

# 11. 数据流设计

## 11.1 AddFile

```text
HTTP POST /api/ipfs/add
  -> API handler
  -> IPFSService.AddFile(reader, options)
  -> UnixFS importer splits file into chunks
  -> DAG nodes are created
  -> blocks written to blockstore
  -> root CID returned
  -> optional recursive pin
  -> optional provide
  -> JSON response
```

## 11.2 Cat/Get

```text
HTTP GET /ipfs/{cid}
  -> Gateway handler
  -> Resolve CID
  -> DAGService / UnixFS exporter
  -> if missing local blocks:
       fetch via Bitswap / routing
  -> stream bytes to response
```

## 11.3 Pin

```text
POST /api/ipfs/pin/{cid}
  -> parse CID
  -> recursive pin
  -> store pin metadata
  -> return success
```

---

# 12. HTTP API 规范

## 12.1 `POST /api/ipfs/add`

### 请求

* `multipart/form-data`
* field: `file`
* 可选 query/body 参数：

  * `pin=true|false`
  * `rawLeaves=true|false`
  * `cidVersion=1`

### 响应

```json
{
  "cid": "bafy...",
  "size": 123456,
  "pinned": true
}
```

### 约束

* 单次上传限制默认 64MB
* 超出返回 `413`

---

## 12.2 `POST /api/ipfs/add-dir`

首版可选。若实现，支持上传压缩包或多文件表单。
若不实现，返回：

```json
{
  "error": "not implemented"
}
```

---

## 12.3 `GET /api/ipfs/stat/{cid}`

响应：

```json
{
  "cid": "bafy...",
  "size": 123456,
  "numLinks": 4,
  "local": true,
  "pinned": true
}
```

---

## 12.4 `POST /api/ipfs/pin/{cid}`

请求体：

```json
{
  "recursive": true
}
```

响应：

```json
{
  "ok": true,
  "cid": "bafy..."
}
```

---

## 12.5 `DELETE /api/ipfs/pin/{cid}`

响应：

```json
{
  "ok": true,
  "cid": "bafy..."
}
```

---

## 12.6 `GET /ipfs/{cid}`

标准只读 Gateway 路径。

要求：

* 支持文件字节流输出
* 尽量自动设置 `Content-Type`
* 支持基础 `Range`
* 对目录 CID，首版可：

  * 返回简单目录 listing，或
  * 返回 501 not implemented

Gateway 是首版必须实现的核心读接口。官方文档也明确将 HTTP Gateway 作为适合浏览器与公网的接口形态。([docs.ipfs.tech][2])

---

# 13. Gateway 行为要求

1. 只读
2. 不支持 writable gateway
3. 默认允许：

   * `GET`
   * `HEAD`
4. 禁止：

   * `PUT`
   * `POST` 到 `/ipfs/*`
   * `DELETE` 到 `/ipfs/*`
5. 对未知 CID 返回 404
6. 对格式错误 CID 返回 400
7. 对取回超时返回 504
8. 对本地无数据且网络不可达返回 404 或 504，由实现决定，但需区分日志原因

---

# 14. 与 Kubo 互通要求

必须满足以下测试用例：

## 用例 A：本系统 add，Kubo 读取

1. 在本系统执行 `POST /api/ipfs/add`
2. 得到 CID
3. Kubo 节点连接到同一网络
4. Kubo 能通过 `ipfs cat <cid>` 或 gateway 读取该内容

## 用例 B：Kubo add，本系统 Gateway 读取

1. 在 Kubo 执行 `ipfs add file`
2. 得到 CID
3. 本系统能在 `/ipfs/{cid}` 读取到相同内容

## 用例 C：本系统 add，小文件 CID 稳定

同一输入文件、同一导入参数，重复导入应得到相同 CID。

## 用例 D：本系统重启后仍能读取

内容保存在本地 blockstore，重启后 `/ipfs/{cid}` 仍可读取。

Kubo 是 Go 版的主要 IPFS 实现，UnixFS 是其文件系统基础数据格式。([Go Packages][7])

---

# 15. 本地存储布局

```text
data/
  ipfs/
    datastore/
    blocks/
    pins.json
```

要求：

* `datastore/` 用于元数据
* `blocks/` 用于块数据
* `pins.json` 首版可简单实现；后续可迁移到 datastore 内部表/命名空间

---

# 16. 错误处理规范

统一 JSON 错误格式：

```json
{
  "error": {
    "code": "CID_NOT_FOUND",
    "message": "content not found"
  }
}
```

错误码建议：

* `INVALID_CID`
* `BAD_REQUEST`
* `PAYLOAD_TOO_LARGE`
* `CID_NOT_FOUND`
* `FETCH_TIMEOUT`
* `PIN_FAILED`
* `UNPIN_FAILED`
* `INTERNAL_ERROR`
* `NOT_IMPLEMENTED`

---

# 17. 日志要求

所有关键路径必须结构化日志：

字段建议：

* `module`
* `cid`
* `peer_id`
* `op`
* `duration_ms`
* `bytes`
* `local_hit`
* `error`

关键日志点：

* add start/end
* pin start/end
* gateway read start/end
* remote fetch start/end
* provide start/end

---

# 18. 观测指标要求

如果项目已有 metrics，增加：

* `ipfs_add_total`
* `ipfs_add_bytes_total`
* `ipfs_gateway_requests_total`
* `ipfs_gateway_bytes_served_total`
* `ipfs_fetch_remote_total`
* `ipfs_fetch_remote_fail_total`
* `ipfs_pin_total`
* `ipfs_pin_fail_total`

---

# 19. 实现建议

## 19.1 Routing 模式

v1 建议只做：

* `dht-client`

不做：

* delegated routing
* IPNI
* provider server

理由：

* 原型更简单
* 能满足与 Kubo 基础互通

IPFS 当前支持多种 routing 系统，但首版没必要全上。([docs.ipfs.tech][5])

---

## 19.2 Import 参数

v1 固定：

* `chunker=size-262144`
* `rawLeaves=true`
* `cidVersion=1`

这样有利于 CID 可预测性。

---

## 19.3 目录支持

v1 可分两档：

### 方案一

首版只实现 `AddFile`，目录返回 `not implemented`

### 方案二

实现基础目录导入，但 gateway 对目录只做简单 listing

建议先做方案一，尽快跑通互通链路。

---

## 19.4 Pin 存储

首版可简单存 `pins.json`，格式：

```json
{
  "recursive": ["bafy..."],
  "direct": ["bafy..."]
}
```

后续再迁移到 datastore namespace。

---

# 20. 安全与边界

1. 不实现 public write gateway
2. `POST /api/ipfs/add` 默认只监听内网或本地
3. 不实现 Kubo RPC 暴露
4. 网关默认只读
5. 不在日志中打印完整上传路径
6. 限制上传大小
7. 限制远程抓取超时

Kubo 官方明确提醒不要把 RPC API 暴露到公网。([docs.ipfs.tech][2])

---

# 21. 开发里程碑

## Milestone 1：基础骨架

* [ ] 共享 host 注入
* [ ] IPFSConfig
* [ ] datastore / blockstore 初始化
* [ ] HTTP server 路由骨架

## Milestone 2：写入

* [ ] `AddFile`
* [ ] 返回标准 CID
* [ ] 本地持久化
* [ ] `POST /api/ipfs/add`

## Milestone 3：读取

* [ ] `Cat/Get`
* [ ] `/api/ipfs/stat/{cid}`
* [ ] `/ipfs/{cid}`

## Milestone 4：联网互通

* [ ] routing
* [ ] bitswap
* [ ] provide
* [ ] Kubo 互通测试

## Milestone 5：pin

* [ ] pin/unpin
* [ ] pin metadata
* [ ] 重启后 pin 恢复

---

# 22. 测试要求

## 单元测试

* CID 解析
* add 同内容 CID 稳定
* pin/unpin 状态
* stat 正确性

## 集成测试

* 本地 add -> gateway read
* 本地 add -> 重启 -> gateway read
* Kubo add -> 本系统 gateway read
* 本系统 add -> Kubo cat
* invalid CID -> 400
* unknown CID -> 404

---

# 23. 给 Codex/Cursor 的实现指令

把下面这段一起喂给 AI。

```text
You are generating a Go prototype for an embedded IPFS subsystem using boxo.

Requirements:

1. Reuse an existing libp2p host instead of creating a new one.
2. Build an embedded IPFS subsystem that can interoperate with Kubo.
3. Use standard UnixFS for file import/export.
4. Use persistent local datastore and blockstore.
5. Support:
   - POST /api/ipfs/add
   - GET /api/ipfs/stat/{cid}
   - POST /api/ipfs/pin/{cid}
   - DELETE /api/ipfs/pin/{cid}
   - GET /ipfs/{cid}
6. Gateway must be read-only.
7. Use CIDv1, raw leaves, sha2-256, chunker=size-262144 by default.
8. Implement the code in a modular structure:

   cmd/chatnode/main.go
   internal/ipfsnode/node.go
   internal/ipfsnode/config.go
   internal/ipfsnode/interfaces.go
   internal/ipfsstore/datastore.go
   internal/ipfsstore/blockstore.go
   internal/ipfsunixfs/add.go
   internal/ipfsunixfs/cat.go
   internal/ipfsunixfs/get.go
   internal/ipfsunixfs/stat.go
   internal/ipfspin/pin.go
   internal/ipfsgateway/gateway.go
   internal/api/ipfs_handlers.go

9. Expose a constructor:
   NewEmbeddedIPFS(ctx context.Context, h host.Host, cfg IPFSConfig) (*EmbeddedIPFS, error)

10. Expose a service interface:
   type IPFSService interface {
       AddFile(ctx context.Context, r io.Reader, opt AddFileOptions) (cid.Cid, error)
       Cat(ctx context.Context, c cid.Cid) (io.ReadCloser, error)
       Get(ctx context.Context, c cid.Cid, w io.Writer) error
       Stat(ctx context.Context, c cid.Cid) (*ObjectStat, error)
       Pin(ctx context.Context, c cid.Cid, recursive bool) error
       Unpin(ctx context.Context, c cid.Cid, recursive bool) error
       IsPinned(ctx context.Context, c cid.Cid) (bool, error)
       Provide(ctx context.Context, c cid.Cid, recursive bool) error
       HasLocal(ctx context.Context, c cid.Cid) (bool, error)
   }

11. Use structured logging and return JSON errors for HTTP API.
12. Keep directory add support optional; if not implemented, return a JSON not implemented error.
13. Write clean, compilable Go code with comments, types, and explicit error handling.
14. Add a minimal integration example in main.go showing:
   - create or inject shared libp2p host
   - initialize EmbeddedIPFS
   - mount handlers
   - start HTTP server
15. Do not implement chat-layer logic.
16. Do not implement IPNS.
17. Do not expose Kubo RPC.
18. Prefer correctness and compile-ability over feature breadth.

Also generate:
- go.mod
- README.md
- example curl commands
- a small test plan in markdown
```

---

# 24. 交付要求

让 AI 最终产出这些文件：

```text
go.mod
README.md
cmd/chatnode/main.go
internal/ipfsnode/config.go
internal/ipfsnode/interfaces.go
internal/ipfsnode/node.go
internal/ipfsstore/datastore.go
internal/ipfsstore/blockstore.go
internal/ipfsunixfs/add.go
internal/ipfsunixfs/cat.go
internal/ipfsunixfs/get.go
internal/ipfsunixfs/stat.go
internal/ipfspin/pin.go
internal/ipfsgateway/gateway.go
internal/api/ipfs_handlers.go
TESTPLAN.md
```

---

# 25. 你喂给 AI 时的额外提示

再补一句最有效：

```text
First produce a compile-oriented skeleton with conservative, currently maintained boxo APIs.
Avoid guessing deprecated APIs.
If a boxo API is uncertain, isolate it behind small wrapper functions and leave TODO comments only in the narrowest place.
```
