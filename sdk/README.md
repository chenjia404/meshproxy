# meshproxy SDK

`meshproxy` 现在可以作为 Go SDK 嵌入到第三方程序里使用。

当前第一版 SDK 入口：

- 包路径：`github.com/chenjia404/meshproxy/sdk`
- 适合场景：
  - 在自己的 Go 程序里启动一个 meshproxy 节点
  - 关闭内置 `SOCKS5` 或 `Local API`
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

## 当前公开能力

- `sdk.New(...)`
- `sdk.LoadConfig(...)`
- `sdk.DefaultConfig()`
- `node.PeerID()`
- `node.Host()`
- `node.Chat()`
- `node.CheckForUpdate(...)`
- `node.ApplyUpdate(...)`
- `node.Close()`

聊天包装当前已覆盖：

- 个人资料
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
