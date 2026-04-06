# IPFS HTTP 接口文档

本文档整理本项目对外暴露的 IPFS 相关 HTTP 接口，来源于：

- [internal/api/ipfs_handlers.go](/mnt/e/code/mesh-proxy/internal/api/ipfs_handlers.go)
- [internal/api/local_api.go](/mnt/e/code/mesh-proxy/internal/api/local_api.go)
- [internal/ipfsnode/interfaces.go](/mnt/e/code/mesh-proxy/internal/ipfsnode/interfaces.go)
- [internal/ipfsnode/node.go](/mnt/e/code/mesh-proxy/internal/ipfsnode/node.go)

## 启用条件

IPFS 接口是否可用，取决于嵌入式 IPFS 配置：

- `GatewayEnabled`：是否注册只读网关 `/ipfs/...`
- `GatewayWritable`：是否允许通过 `/ipfs` 或 `/ipfs/` 上传
- `APIEnabled`：是否注册写入、统计、pin 相关接口 `/api/ipfs/*`

当 `APIEnabled` 未开启时，`/api/ipfs/*` 会直接返回 404。

## 接口总览

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` / `HEAD` | `/ipfs/{cid}/...` | 只读网关访问内容 |
| `POST` / `PUT` | `/ipfs`、`/ipfs/` | 可选写入上传，行为同 `add` |
| `POST` | `/api/ipfs/add` | 上传单文件 |
| `POST` | `/api/ipfs/add-dir` | 目录上传占位，当前未实现 |
| `GET` | `/api/ipfs/stat/{cid}` | 查询对象统计信息 |
| `POST` | `/api/ipfs/pin/{cid}` | 递归 pin |
| `DELETE` | `/api/ipfs/pin/{cid}` | 递归 unpin |

## 统一错误格式

IPFS 写接口会返回统一 JSON 错误：

```json
{
  "error": {
    "code": "BAD_REQUEST",
    "message": "method not allowed"
  }
}
```

常见 `code`：

- `BAD_REQUEST`
- `PAYLOAD_TOO_LARGE`
- `INVALID_CID`
- `CID_NOT_FOUND`
- `NOT_IMPLEMENTED`
- `PIN_FAILED`
- `UNPIN_FAILED`
- `INTERNAL_ERROR`

## 1. 只读网关

### `GET /ipfs/{cid}/...`

通过本地嵌入式 gateway 读取内容，例如：

- `GET /ipfs/bafy.../avatar.png`
- `GET /ipfs/bafy...`

可选请求头：

- `http_mirror_gateway`：可选，请填纯 `host` 或 `host:port`，例如 `gateway.example.com`、`gateway.example.com:8443`

请求头规则：

- 该请求头只对当前这一次 `/ipfs/...` 读取生效
- 如果设置了该请求头，会优先使用这个镜像网关拉取内容，而不是配置文件里的 `ipfs.http_mirror_gateway`
- 服务端会固定按 `https://{host}` 组装镜像地址，不接受客户端自行指定 `http://` 或 `https://`
- 请求头必须是纯 host，不能包含 scheme、路径、查询串或空白字符
- 请求头非法时，接口返回 `400 BAD_REQUEST`

镜像拉取规则：

- 对纯 CID 请求，例如 `GET /ipfs/{cid}`，节点会优先尝试从镜像网关请求 `GET https://{mirror}/ipfs/{cid}?format=car`
- 如果镜像网关返回合法 CAR，则会先在临时 blockstore 中校验整棵 DAG，再导入本地节点
- 如果 CAR 不可用，再回退为单文件拉取校验
- 对带文件名语义的请求，不走 CAR，直接按单文件拉取并校验 CID。以下两类都视为带文件名语义：
  - `GET /ipfs/{cid}/avatar.png`
  - `GET /ipfs/{cid}?filename=avatar.png`

支持：

- `GET`
- `HEAD`

不支持：

- `POST`
- `PUT`
- 其他非读取方法

### `POST /ipfs` 和 `PUT /ipfs/`

仅在 `GatewayWritable` 开启时允许。

请求体要求为 `multipart/form-data`，字段与 `/api/ipfs/add` 一致，主要字段：

- `file`：必填，上传文件
- `filename`：可选，文件名
- `pin`：可选，是否 pin
- `rawLeaves`：可选
- `hashFunction`：可选
- `chunker`：可选
- `cidVersion`：可选，当前仅支持 `1`

返回示例：

```json
{
  "cid": "bafy...",
  "size": 12345,
  "pinned": true
}
```

## 2. 上传接口

### `POST /api/ipfs/add`

上传单个文件。

请求：

- `Content-Type: multipart/form-data`
- 表单字段：
  - `file`：必填
  - `filename`：可选
  - `pin`：可选，默认跟随配置 `auto_pin_on_add`
  - `rawLeaves`：可选，默认跟随配置
  - `hashFunction`：可选，默认跟随配置
  - `chunker`：可选，默认跟随配置
  - `cidVersion`：可选，当前仅支持 `1`

返回：

```json
{
  "cid": "bafy...",
  "size": 12345,
  "pinned": false
}
```

说明：

- 上传成功后会尝试执行 `stat`，用于回填 `size` 和 `pinned`
- 如果 `stat` 失败，接口仍会返回 `cid`，`size` 默认 `0`

### `POST /api/ipfs/add-dir`

当前为占位接口，返回：

- HTTP 状态码：`501 Not Implemented`
- JSON：

```json
{
  "error": "not implemented"
}
```

## 3. 统计接口

### `GET /api/ipfs/stat/{cid}`

查询对象统计信息。

路径参数：

- `{cid}`：CID 字符串

返回：

```json
{
  "cid": "bafy...",
  "size": 12345,
  "numLinks": 2,
  "local": true,
  "pinned": false
}
```

字段说明：

- `cid`：对象 CID
- `size`：对象大小
- `numLinks`：链接数
- `local`：本节点是否已有该对象
- `pinned`：是否已递归 pin

错误：

- 缺少 CID：`BAD_REQUEST`
- CID 非法：`INVALID_CID`
- 内容不存在：`CID_NOT_FOUND`

## 4. Pin 接口

### `POST /api/ipfs/pin/{cid}`

递归 pin 指定 CID。

请求体可选：

```json
{
  "recursive": true
}
```

说明：

- 当前实现仅支持递归 pin
- `recursive` 默认为 `true`
- 若传 `false`，会返回 `NOT_IMPLEMENTED`

返回：

```json
{
  "ok": true,
  "cid": "bafy..."
}
```

### `DELETE /api/ipfs/pin/{cid}`

递归 unpin 指定 CID。

请求体可选：

```json
{
  "recursive": true
}
```

说明：

- 当前实现仅支持递归 unpin
- `recursive` 默认为 `true`
- 若传 `false`，会返回 `NOT_IMPLEMENTED`

返回：

```json
{
  "ok": true,
  "cid": "bafy..."
}
```

## 5. 实现补充

- 上传接口默认限制由 `MaxUploadBytes` 控制
- 节点可通过配置项 `ipfs.http_mirror_gateway` 指定默认 HTTP 镜像网关；未配置或留空时，默认使用 `https://ipfs.io`
- `/ipfs/...` 读取请求也可通过请求头 `http_mirror_gateway` 覆盖本次使用的镜像网关
- `/ipfs/...` 是内容访问入口，`/api/ipfs/*` 是管理入口
- `AddDir` 目前未实现，但保留接口形状，便于后续补齐
