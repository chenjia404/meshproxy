# 身份 challenge 签名接口

本接口用于给 `meshchat-server` 的 `POST /auth/login` 流程签名 `challenge`。

`meshchat-server` 的认证流程是：

1. `POST /auth/challenge`
2. 使用节点私钥对返回的 `challenge` 字符串签名
3. `POST /auth/login` 提交签名和公钥

本项目新增的本地接口：

## `POST /api/v1/identity/challenge/sign`

### 请求

```json
{
  "challenge": "meshchat login\n challenge_id=...\n peer_id=...\n expires_at=..."
}
```

说明：

- `challenge` 必须是原文
- 不要自行裁剪空格或换行
- 签名对象就是这个字符串的原始字节

### 响应

```json
{
  "peer_id": "12D3KooW...",
  "challenge": "meshchat login\n challenge_id=...\n peer_id=...\n expires_at=...",
  "signature_base64": "BASE64_SIGNATURE",
  "public_key_base64": "BASE64_MARSHALLED_LIBP2P_PUBLIC_KEY",
  "signature_encoding": "base64",
  "public_key_encoding": "base64"
}
```

### 兼容性说明

- `signature_base64` 可直接作为 `meshchat-server` 的 `signature`
- `public_key_base64` 可直接作为 `meshchat-server` 的 `public_key`
- 响应里同时提供 `signature` 和 `public_key` 别名，两个字段与 `*_base64` 内容一致
- 该接口使用本机 libp2p 私钥签名

### 常见错误

- `400 challenge is empty`
- `404 identity service not available`
- `500`：签名或公钥导出失败
