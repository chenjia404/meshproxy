package main

import (
	"context"
	"net"
	"os"
	"runtime"
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
	// GOOS=linux 交叉编译运行在 Android 上时，/etc/resolv.conf 通常不存在
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/etc/resolv.conf"); os.IsNotExist(err) {
			return true
		}
	}
	return false
}
