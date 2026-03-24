package app

import (
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/network"

	"github.com/chenjia404/meshproxy/internal/chatrelay"
	"github.com/chenjia404/meshproxy/internal/config"
	"github.com/chenjia404/meshproxy/internal/relay"
	"github.com/chenjia404/meshproxy/internal/tunnel"
)

func (a *App) relayModeEnabled() bool {
	if a == nil {
		return false
	}
	return a.cfg.Mode == config.ModeRelay || a.cfg.Mode == config.ModeRelayExit
}

func (a *App) dispatchRelayV1Connect(str network.Stream) {
	_ = str.SetDeadline(time.Now().Add(45 * time.Second))
	var req chatrelay.RelayConnectRequest
	if err := tunnel.ReadJSONFrame(str, &req); err != nil {
		_ = str.Close()
		return
	}
	local := a.Host().ID().String()
	if strings.TrimSpace(req.DstID) == local {
		if a.chat != nil {
			a.chat.ServeRelayConnectAsB(str, &req)
		} else {
			_ = str.Close()
		}
		return
	}
	if a.relayModeEnabled() && a.chatRelayV1Table != nil {
		relay.ForwardRelayConnect(a.ctx, a.Host(), a.chatRelayV1Table, a.traffic, str, &req)
		return
	}
	_ = str.Close()
}

func (a *App) dispatchRelayV1Handshake(str network.Stream) {
	_ = str.SetDeadline(time.Now().Add(45 * time.Second))
	var req chatrelay.RelayHandshakeRequest
	if err := tunnel.ReadJSONFrame(str, &req); err != nil {
		_ = str.Close()
		return
	}
	local := a.Host().ID().String()
	if strings.TrimSpace(req.DstID) == local {
		if a.chat != nil {
			a.chat.ServeRelayHandshakeAsB(str, &req)
		} else {
			_ = str.Close()
		}
		return
	}
	if a.relayModeEnabled() && a.chatRelayV1Table != nil {
		relay.ForwardRelayHandshake(a.ctx, a.Host(), a.traffic, str, &req)
		return
	}
	_ = str.Close()
}

func (a *App) dispatchRelayV1Data(str network.Stream) {
	_ = str.SetDeadline(time.Now().Add(45 * time.Second))
	var frame chatrelay.RelayDataFrame
	if err := tunnel.ReadJSONFrame(str, &frame); err != nil {
		_ = str.Close()
		return
	}
	local := a.Host().ID().String()
	if strings.TrimSpace(frame.DstID) == local {
		if a.chat != nil {
			a.chat.ServeRelayDataAsB(str, &frame)
		} else {
			_ = str.Close()
		}
		return
	}
	if a.relayModeEnabled() && a.chatRelayV1Table != nil {
		relay.ForwardRelayData(a.ctx, a.Host(), a.chatRelayV1Table, a.traffic, str, &frame)
		return
	}
	_ = str.Close()
}

func (a *App) dispatchRelayV1Heartbeat(str network.Stream) {
	_ = str.SetDeadline(time.Now().Add(25 * time.Second))
	var ping chatrelay.RelayHeartbeat
	if err := tunnel.ReadJSONFrame(str, &ping); err != nil {
		_ = str.Close()
		return
	}
	local := a.Host().ID().String()
	if strings.TrimSpace(ping.DstID) == local {
		if a.chat != nil {
			a.chat.ServeRelayHeartbeatAsB(str, &ping)
		} else {
			_ = str.Close()
		}
		return
	}
	if a.relayModeEnabled() && a.chatRelayV1Table != nil {
		relay.ForwardRelayHeartbeat(a.ctx, a.Host(), a.chatRelayV1Table, a.traffic, str, &ping)
		return
	}
	_ = str.Close()
}
