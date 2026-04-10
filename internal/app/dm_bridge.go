package app

import (
	"context"
	"log"

	"github.com/chenjia404/meshproxy/internal/meshserver"
	sessionv1 "github.com/chenjia404/meshproxy/internal/meshserver/sessionv1"
)

// startDMBridges 為每個 meshserver 連線訂閱下行 DIRECT_MESSAGE_EVENT / DIRECT_PEER_ACK_EVENT，先寫本地庫再廣播。
func (a *App) startDMBridges(ctx context.Context) {
	if a == nil || a.meshServer == nil || a.dmStore == nil {
		return
	}
	for _, info := range a.meshServer.List() {
		name := info.Name
		cli, err := a.meshServer.Get(name)
		if err != nil || cli == nil {
			continue
		}
		localUID := ""
		if ar := cli.AuthResult(); ar != nil {
			localUID = ar.UserId
		}
		go a.runDMBridge(ctx, name, cli, localUID)
	}
}

func (a *App) runDMBridge(ctx context.Context, connectionName string, cli *meshserver.Client, localUserID string) {
	dmCh := cli.DMEvents()
	ackCh := cli.DMPeerAckEvents()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-dmCh:
			if !ok {
				return
			}
			if ev == nil {
				continue
			}
			if err := a.dmStore.ApplyDirectMessageEvent(ctx, connectionName, localUserID, ev); err != nil {
				log.Printf("[meshserver_dm] apply message: %v", err)
			}
			a.broadcastDMEvent(dmMessageEventJSON(connectionName, ev))
		case ev, ok := <-ackCh:
			if !ok {
				return
			}
			if ev == nil {
				continue
			}
			if err := a.dmStore.ApplyPeerAck(ctx, connectionName, ev); err != nil {
				log.Printf("[meshserver_dm] apply peer ack: %v", err)
			}
			a.broadcastDMEvent(dmPeerAckJSON(connectionName, ev))
		}
	}
}

func dmMessageEventJSON(connection string, ev *sessionv1.DirectMessageEvent) map[string]any {
	return map[string]any{
		"type":                      "dm_message",
		"connection":                connection,
		"conversation_id":           ev.ConversationId,
		"message_id":                ev.MessageId,
		"seq":                       ev.Seq,
		"from_user_id":              ev.FromUserId,
		"to_user_id":                ev.ToUserId,
		"msg_type":                  ev.MessageType.String(),
		"plaintext":                 ev.Text,
		"created_at_unix_millis":    ev.CreatedAtMs,
	}
}

func dmPeerAckJSON(connection string, ev *sessionv1.DirectPeerAckEvent) map[string]any {
	return map[string]any{
		"type":            "dm_message_state",
		"connection":      connection,
		"conversation_id": ev.ConversationId,
		"message_id":      ev.MessageId,
		"acked_at_ms":     ev.AckedAtMs,
		"peer_user_id":    ev.PeerUserId,
	}
}
