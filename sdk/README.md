# meshproxy SDK

`meshproxy` 现在可以作为 Go SDK 嵌入到第三方程序里使用。

当前第一版 SDK 入口：

- 包路径：`github.com/chenjia404/meshproxy/sdk`
- 适合场景：
  - 在自己的 Go 程序里启动一个 meshproxy 节点
  - 关闭内置 `SOCKS5` 或 `Local API`
  - 注入现成的 `libp2p Host`
  - 调用聊天能力
  - 获取 `Peer ID`、`libp2p Host`

## 快速开始

```go
package main

import (
	"context"
	"log"

	"github.com/chenjia404/meshproxy/sdk"
)

func main() {
	cfg := sdk.DefaultConfig()
	cfg.DataDir = "data-sdk"
	cfg.Mode = sdk.ModeRelay

	node, err := sdk.New(context.Background(), cfg, sdk.Options{
		EnableSOCKS5:   false,
		EnableLocalAPI: false,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer node.Close()

	log.Println("peer id:", node.PeerID())
	log.Println("listen addrs:", node.P2PListenAddrs())

	select {}
}
```

## 使用配置文件启动

```go
package main

import (
	"context"
	"log"

	"github.com/chenjia404/meshproxy/sdk"
)

func main() {
	cfg, err := sdk.LoadConfig("config.yaml")
	if err != nil {
		log.Fatal(err)
	}

	node, err := sdk.New(context.Background(), cfg, sdk.Options{
		EnableSOCKS5:   true,
		EnableLocalAPI: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer node.Close()

	log.Println("peer id:", node.PeerID())
	log.Println("socks5:", node.Socks5Listen())
	log.Println("local api:", node.LocalAPIListen())

	if err := node.Run(); err != nil {
		log.Fatal(err)
	}
}
```

## 调用聊天能力

```go
package main

import (
	"context"
	"log"

	"github.com/chenjia404/meshproxy/sdk"
)

func main() {
	cfg := sdk.DefaultConfig()
	cfg.DataDir = "data-chat-sdk"

	node, err := sdk.New(context.Background(), cfg, sdk.Options{
		EnableSOCKS5:   false,
		EnableLocalAPI: false,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer node.Close()

	chat := node.Chat()

	profile, err := chat.GetProfile()
	if err != nil {
		log.Fatal(err)
	}
	log.Println("my nickname:", profile.Nickname)
	log.Println("my bio:", profile.Bio)

	contacts, err := chat.ListContacts()
	if err != nil {
		log.Fatal(err)
	}
	log.Println("contacts:", len(contacts))
}
```

## 手动连接某个 Peer

```go
err := node.Chat().ConnectPeer(peerID)
if err != nil {
	log.Fatal(err)
}
```

## 获取底层 libp2p Host

```go
h := node.Host()
log.Println(h.ID())
```

## 注入现成的 libp2p Host

如果你的程序已经自己创建了 `libp2p Host`，可以直接注入给 `meshproxy SDK` 复用：

```go
package main

import (
	"context"
	"log"

	libp2p "github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"

	"github.com/chenjia404/meshproxy/sdk"
)

func main() {
	ctx := context.Background()

	cfg := sdk.DefaultConfig()
	cfg.DataDir = "data-sdk"

	priv, err := sdk.LoadOrCreatePrivateKey(cfg.IdentityKeyPath)
	if err != nil {
		log.Fatal(err)
	}

	h, err := libp2p.New(libp2p.Identity(priv))
	if err != nil {
		log.Fatal(err)
	}
	defer h.Close()

	r, err := dht.New(ctx, h)
	if err != nil {
		log.Fatal(err)
	}

	node, err := sdk.New(ctx, cfg, sdk.Options{
		EnableSOCKS5:   false,
		EnableLocalAPI: false,
		Host:           h,
		Routing:        r,
		CloseHost:      false,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer node.Close()
}
```

如果你不想手动组 `sdk.Options`，也可以直接用更薄的 `sdk.NewFromHost(...)`：

```go
node, err := sdk.NewFromHost(ctx, cfg, h, r, sdk.HostOptions{
	EnableSOCKS5:   false,
	EnableLocalAPI: false,
	CloseHost:      false,
})
if err != nil {
	log.Fatal(err)
}
defer node.Close()
```

说明：

- 注入的 `Host` 必须和 `IdentityKeyPath` 对应的身份一致，否则会报错
- 可以先用 `sdk.LoadOrCreatePrivateKey(...)` 构造和 meshproxy 相同身份的 `Host`
- `Routing` 是可选的；不传也能启动，但 `FindPeer`、DHT discovery 这类能力会变弱
- `CloseHost: false` 表示 `node.Close()` 不会关闭你外部传入的 `Host`

## 把配置字段映射成命令行参数

如果你自己写一个 Go 入口，可以直接复用同一套配置映射：

```go
package main

import (
	"flag"
	"log"
	"os"

	"github.com/chenjia404/meshproxy/sdk"
)

func main() {
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	cfg := sdk.DefaultConfig()
	if err := sdk.RegisterFlags(fs, &cfg); err != nil {
		log.Fatal(err)
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
```

映射规则默认使用 YAML 路径名，比如：

- `--mode`
- `--p2p.listen_addrs`
- `--client.exit_selection.mode`
- `--exit.policy.allow_tcp`

数组参数用英文逗号分隔，例如：

- `--p2p.listen_addrs=/ip4/0.0.0.0/tcp/4001,/ip6/::/tcp/4001`
- `--p2p.bootstrap_peers=/ip4/1.2.3.4/tcp/1234/p2p/QmTest,/dnsaddr/bootstrap.libp2p.io/p2p/QmFoo`
- `--exit.policy.allowed_ports=80,443,8080`
- `--exit.policy.allowed_domains=example.com,openai.com`

老的短参数也保留了：

- `--socks5`
- `--api`
- `--nodisc`

## 当前 CLI 参数清单

下面是 `sdk.RegisterFlags(...)` 目前自动映射出来的参数。数组类型用英文逗号分隔。

### 顶层参数

| 参数 | 类型 | 说明 |
|---|---|---|
| `--config` | string | 配置文件路径 |
| `--mode` | string | 节点模式，`relay` 或 `relay+exit` |
| `--data_dir` | string | 数据目录 |
| `--identity_key_path` | string | 身份私钥路径 |
| `--auto_update` | bool | 是否开启自动更新 |

### P2P

| 参数 | 类型 | 说明 |
|---|---|---|
| `--p2p.listen_addrs` | []string | libp2p 监听地址 |
| `--p2p.bootstrap_peers` | []string | 启动时连接的 bootstrap peers |
| `--p2p.nodisc` | bool | 关闭 DHT rendezvous 发现 |
| `--p2p.discovery_tag` | string | 发现标签 |

### SOCKS5

| 参数 | 类型 | 说明 |
|---|---|---|
| `--socks5.listen` | string | 本地 SOCKS5 监听地址 |
| `--socks5.tunnel_to_exit` | bool | 是否透传到 exit 的 SOCKS5 |
| `--socks5.exit_upstream` | string | exit 节点上游 SOCKS5 地址 |
| `--socks5.allow_udp_associate` | bool | 是否允许 UDP ASSOCIATE |

### API

| 参数 | 类型 | 说明 |
|---|---|---|
| `--api.listen` | string | 本地 API 监听地址 |

### CircuitPool

| 参数 | 类型 | 说明 |
|---|---|---|
| `--circuit_pool.min_per_pool` | int | 每个池最小电路数 |
| `--circuit_pool.max_per_pool` | int | 每个池最大电路数 |
| `--circuit_pool.min_total` | int | 总最小电路数 |
| `--circuit_pool.max_total` | int | 总最大电路数 |
| `--circuit_pool.idle_timeout_seconds` | int | 空闲电路超时时间 |
| `--circuit_pool.replenish_interval_seconds` | int | 池维护周期 |

### Client

| 参数 | 类型 | 说明 |
|---|---|---|
| `--client.build_retries` | int | 构建 circuit 的重试次数 |
| `--client.begin_tcp_retries` | int | BEGIN_TCP 重试次数 |
| `--client.begin_connect_timeout_seconds` | int | 等待 CONNECTED 的超时 |
| `--client.heartbeat_enabled` | bool | 是否启用心跳 |
| `--client.heartbeat_interval_seconds` | int | 心跳间隔 |
| `--client.heartbeat_timeout_seconds` | int | 心跳超时 |
| `--client.heartbeat_failure_threshold` | int | 心跳失败阈值 |
| `--client.skip_heartbeat_when_active_seconds` | int | 最近有流量时跳过心跳的秒数 |

### Client Exit Selection

| 参数 | 类型 | 说明 |
|---|---|---|
| `--client.exit_selection.mode` | string | exit 选择策略 |
| `--client.exit_selection.allowed_countries` | []string | 允许国家 |
| `--client.exit_selection.preferred_countries` | []string | 优先国家 |
| `--client.exit_selection.fixed_exit_peer_id` | string | 固定 exit peer |
| `--client.exit_selection.exclude_countries` | []string | 排除国家 |
| `--client.exit_selection.exclude_peer_ids` | []string | 排除 peer |
| `--client.exit_selection.require_remote_dns` | bool | 是否要求远端 DNS |
| `--client.exit_selection.require_tcp_support` | bool | 是否要求 TCP 支持 |
| `--client.exit_selection.fallback_to_any` | bool | 是否允许回退到任意 exit |
| `--client.exit_selection.allow_direct_exit` | bool | 是否允许直连出口 |

### Client GeoIP

| 参数 | 类型 | 说明 |
|---|---|---|
| `--client.geoip.provider` | string | GeoIP 提供方 |
| `--client.geoip.cache_ttl_minutes` | int | GeoIP 缓存时间 |

### Exit

> `--exit.*` 只有在需要出口策略配置时才会用到；如果 `mode=relay+exit` 且未显式配置 `exit`，程序会使用内置默认值。

| 参数 | 类型 | 说明 |
|---|---|---|
| `--exit.enabled` | bool | 是否启用出口配置 |
| `--exit.policy.allow_tcp` | bool | 是否允许 TCP |
| `--exit.policy.allow_udp` | bool | 是否允许 UDP |
| `--exit.policy.remote_dns` | bool | 是否允许远端 DNS |
| `--exit.policy.allowed_ports` | []int | 允许端口 |
| `--exit.policy.denied_ports` | []int | 拒绝端口 |
| `--exit.policy.allowed_domains` | []string | 允许域名 |
| `--exit.policy.denied_domains` | []string | 拒绝域名 |
| `--exit.policy.allowed_domain_suffixes` | []string | 允许域名后缀 |
| `--exit.policy.denied_domain_suffixes` | []string | 拒绝域名后缀 |
| `--exit.policy.peer_whitelist` | []string | peer 白名单 |
| `--exit.policy.peer_blacklist` | []string | peer 黑名单 |
| `--exit.policy.allow_private_ip_targets` | bool | 是否允许私网目标 |
| `--exit.policy.allow_loopback_targets` | bool | 是否允许回环目标 |
| `--exit.policy.allow_link_local_targets` | bool | 是否允许链路本地目标 |
| `--exit.runtime.drain_mode` | bool | 是否进入排空模式 |
| `--exit.runtime.accept_new_streams` | bool | 是否接受新流 |

## 检查更新

```go
info, err := node.CheckForUpdate(context.Background())
if err != nil {
	log.Fatal(err)
}
if info.UpdateAvailable {
	log.Println("latest:", info.LatestVersion)
}
```

## 更新资料

```go
profile, err := node.Chat().UpdateProfile("Alice", "喜欢折腾去中心化网络")
if err != nil {
	log.Fatal(err)
}
log.Println(profile.Nickname, profile.Bio)
```

`bio` 最多 `140` 字，超出时会直接返回错误。

头像可以通过 `UpdateProfileAvatar(fileName, data)` 设置，大小最多 `512KB`，文件会按内容 `sha256` 落盘到 `data/avatar/`。

## 当前公开能力

- `sdk.New(...)`
- `sdk.NewFromHost(...)`
- `sdk.LoadConfig(...)`
- `sdk.DefaultConfig()`
- `sdk.LoadOrCreatePrivateKey(...)`
- `node.PeerID()`
- `node.Host()`
- `node.Chat()`
- `node.CheckForUpdate(...)`
- `node.ApplyUpdate(...)`
- `node.Close()`

聊天包装当前已覆盖：

- 个人资料和头像
- 联系人
- 好友请求
- 私聊消息
- 群聊消息
- 群管理基础能力

## 当前边界

这还是第一版 SDK，目前更偏“嵌入式节点启动 + 聊天能力调用”。

还没有单独包装成稳定公开接口的模块包括：

- discovery 细粒度接口
- circuit manager / pool 控制
- relay / exit operator 专用接口

如果后面要继续对外开放，建议优先补：

1. `Discovery()` 包装
2. `Circuits()` 包装
3. 更完整的事件回调接口
