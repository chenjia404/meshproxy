package main

import (
	"bufio"
	"context"
	"log"
	"net"
	"os"
	"runtime"
	"strings"

	_ "github.com/wlynxg/anet" // 替换 net.Interfaces/InterfaceAddrs，修复 Android netlink 权限问题
)

// Android 上 CGO_ENABLED=0 时，Go 纯 Go DNS 解析器读 /etc/resolv.conf
// 获取 nameserver，但 Android 该文件缺失或指向 ::1（无本地 DNS 服务），
// 导致所有域名解析失败。这里覆盖 net.DefaultResolver 使用公共 DNS。
//
// 使用运行时检测而非构建标签，兼容 GOOS=android 和 GOOS=linux 两种交叉编译方式。
func init() {
	if !needsPublicDNS() {
		return
	}
	log.Println("[dns] 系统 DNS 不可用，使用公共 DNS (8.8.8.8, 1.1.1.1, 223.5.5.5)")
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{}
			for _, dns := range []string{"8.8.8.8:53", "1.1.1.1:53", "223.5.5.5:53"} {
				conn, err := d.DialContext(ctx, "udp", dns)
				if err == nil {
					return conn, nil
				}
			}
			return d.DialContext(ctx, network, address)
		},
	}
}

func needsPublicDNS() bool {
	if runtime.GOOS == "android" {
		return true
	}
	if runtime.GOOS != "linux" {
		return false
	}
	// GOOS=linux 交叉编译运行在 Android 上：
	// 1) 检测 Android 特征环境变量
	if os.Getenv("ANDROID_ROOT") != "" || os.Getenv("ANDROID_DATA") != "" {
		return true
	}
	// 2) /etc/resolv.conf 不存在
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return true
	}
	// 3) resolv.conf 存在但 nameserver 全是 localhost（::1 / 127.x.x.x）→ 不可用
	return !hasUsableNameserver(string(data))
}

func hasUsableNameserver(content string) bool {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "nameserver") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ns := fields[1]
		if ns == "::1" || ns == "127.0.0.1" || strings.HasPrefix(ns, "127.") {
			continue
		}
		return true
	}
	return false
}
