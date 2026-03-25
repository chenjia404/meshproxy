# mesh-proxy 身份私钥导入导出 API

本文档描述本地节点身份私钥的导入与导出接口。

## 1. 约定

- 基底地址：由配置 `api.listen` 决定
- 编码格式：`base58`
- 当前仅支持 `Ed25519` 私钥
- 导入成功后只会写入身份文件，需重启进程后新身份才会真正生效

## 2. 导出私钥

`GET /api/v1/identity/private-key/export`

返回示例：

```json
{
  "encoding": "base58",
  "peer_id": "12D3KooW...",
  "private_key_base58": "3mJr7AoUXx2Wqd...",
  "requires_restart": false
}
```

说明：

- `private_key_base58` 是当前运行中节点身份私钥的 base58 字符串
- `peer_id` 是当前运行中身份对应的 peer ID

## 3. 导入私钥

`POST /api/v1/identity/private-key/import`

请求体：

```json
{
  "private_key_base58": "3mJr7AoUXx2Wqd..."
}
```

返回示例：

```json
{
  "ok": true,
  "encoding": "base58",
  "peer_id": "12D3KooW...",
  "requires_restart": true
}
```

说明：

- `peer_id` 是导入后私钥对应的 peer ID
- `requires_restart=true` 表示必须重启 `mesh-proxy` 后，新身份才会切换为导入的私钥
- 当前实现不会在运行中热切换 libp2p host 身份
