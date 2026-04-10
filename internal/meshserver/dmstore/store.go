// Package dmstore 持久化 meshserver 中心化私聊的本地快取（SQLite）。
package dmstore

import (
	"context"
	"database/sql"
	"time"

	_ "modernc.org/sqlite"

	sessionv1 "github.com/chenjia404/meshproxy/internal/meshserver/sessionv1"
)

// Store 本地 DM 快取。
type Store struct {
	db *sql.DB
}

// Open 建立或開啟 SQLite。
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS dm_conversations (
			connection_name TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			peer_user_id TEXT NOT NULL,
			peer_display_name TEXT NOT NULL DEFAULT '',
			last_seq INTEGER NOT NULL DEFAULT 0,
			unread_count INTEGER NOT NULL DEFAULT 0,
			last_message TEXT NOT NULL DEFAULT '',
			updated_at_unix INTEGER NOT NULL,
			PRIMARY KEY (connection_name, conversation_id)
		)`,
		`CREATE TABLE IF NOT EXISTS dm_messages (
			connection_name TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			from_user_id TEXT NOT NULL,
			to_user_id TEXT NOT NULL,
			direction TEXT NOT NULL,
			msg_type TEXT NOT NULL,
			plaintext TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL DEFAULT 'delivered',
			created_at_unix INTEGER NOT NULL,
			acked_at_unix INTEGER,
			PRIMARY KEY (connection_name, message_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_dm_msg_conv_seq ON dm_messages(connection_name, conversation_id, seq)`,
		`CREATE TABLE IF NOT EXISTS dm_sync_cursor (
			connection_name TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			after_seq INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (connection_name, conversation_id)
		)`,
		`CREATE TABLE IF NOT EXISTS dm_pending_ack (
			connection_name TEXT NOT NULL,
			message_id TEXT NOT NULL,
			created_at_unix INTEGER NOT NULL,
			PRIMARY KEY (connection_name, message_id)
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// Close 關閉資料庫。
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// ConversationRow 列表用。
type ConversationRow struct {
	Connection      string `json:"connection"`
	ConversationID  string `json:"conversation_id"`
	PeerUserID      string `json:"peer_user_id"`
	PeerDisplayName string `json:"peer_display_name"`
	LastSeq         uint64 `json:"last_seq"`
	UnreadCount     int    `json:"unread_count"`
	LastMessage     string `json:"last_message"`
	UpdatedAtUnix   int64  `json:"updated_at_unix"`
}

// MessageRow 訊息。
type MessageRow struct {
	Connection     string `json:"connection"`
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	Seq            uint64 `json:"seq"`
	FromUserID     string `json:"from_user_id"`
	ToUserID       string `json:"to_user_id"`
	Direction      string `json:"direction"`
	MsgType        string `json:"msg_type"`
	Plaintext      string `json:"plaintext"`
	State          string `json:"state"`
	CreatedAtUnix  int64  `json:"created_at_unix"`
	AckedAtUnix    *int64 `json:"acked_at_unix,omitempty"`
}

// UpsertConversationMeta 更新會話摘要。
func (s *Store) UpsertConversationMeta(ctx context.Context, connectionName, conversationID, peerUserID, peerDisplayName string, lastSeq uint64, lastMessage string, bumpUnread int) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO dm_conversations (connection_name, conversation_id, peer_user_id, peer_display_name, last_seq, unread_count, last_message, updated_at_unix)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(connection_name, conversation_id) DO UPDATE SET
			peer_user_id = excluded.peer_user_id,
			peer_display_name = CASE WHEN excluded.peer_display_name != '' THEN excluded.peer_display_name ELSE dm_conversations.peer_display_name END,
			last_seq = MAX(dm_conversations.last_seq, excluded.last_seq),
			unread_count = dm_conversations.unread_count + excluded.unread_count,
			last_message = CASE WHEN excluded.last_message != '' THEN excluded.last_message ELSE dm_conversations.last_message END,
			updated_at_unix = excluded.updated_at_unix
	`, connectionName, conversationID, peerUserID, peerDisplayName, int64(lastSeq), bumpUnread, lastMessage, now)
	return err
}

// ApplyDirectMessageEvent 寫入下行訊息（先於 WebSocket 廣播呼叫）。
func (s *Store) ApplyDirectMessageEvent(ctx context.Context, connectionName string, localUserID string, ev *sessionv1.DirectMessageEvent) error {
	if ev == nil {
		return nil
	}
	dir := "in"
	if ev.FromUserId == localUserID {
		dir = "out"
	}
	ts := int64(ev.CreatedAtMs / 1000)
	if ts <= 0 {
		ts = time.Now().Unix()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO dm_messages (connection_name, conversation_id, message_id, seq, from_user_id, to_user_id, direction, msg_type, plaintext, state, created_at_unix, acked_at_unix)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'delivered', ?, NULL)
		ON CONFLICT(connection_name, message_id) DO NOTHING
	`, connectionName, ev.ConversationId, ev.MessageId, int64(ev.Seq), ev.FromUserId, ev.ToUserId, dir, msgTypeToString(ev.MessageType), ev.Text, ts)
	if err != nil {
		return err
	}
	bump := 0
	if dir == "in" {
		bump = 1
	}
	peer := ev.FromUserId
	if dir == "out" {
		peer = ev.ToUserId
	}
	return s.UpsertConversationMeta(ctx, connectionName, ev.ConversationId, peer, "", ev.Seq, ev.Text, bump)
}

func msgTypeToString(t sessionv1.MessageType) string {
	switch t {
	case sessionv1.MessageType_IMAGE:
		return "image"
	case sessionv1.MessageType_FILE:
		return "file"
	case sessionv1.MessageType_SYSTEM:
		return "system"
	default:
		return "text"
	}
}

// ApplySendAck 發送成功後寫入本地（樂觀路徑）。
func (s *Store) ApplySendAck(ctx context.Context, connectionName, localUserID, peerUserID string, ack *sessionv1.SendDirectMessageAck, text string) error {
	if ack == nil {
		return nil
	}
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO dm_messages (connection_name, conversation_id, message_id, seq, from_user_id, to_user_id, direction, msg_type, plaintext, state, created_at_unix, acked_at_unix)
		VALUES (?, ?, ?, ?, ?, ?, 'out', 'text', ?, 'sent', ?, NULL)
		ON CONFLICT(connection_name, message_id) DO UPDATE SET
			state = 'sent', seq = excluded.seq
	`, connectionName, ack.ConversationId, ack.MessageId, int64(ack.Seq), localUserID, peerUserID, text, now)
	if err != nil {
		return err
	}
	return s.UpsertConversationMeta(ctx, connectionName, ack.ConversationId, peerUserID, "", ack.Seq, text, 0)
}

// ApplyPeerAck 更新訊息狀態為已確認。
func (s *Store) ApplyPeerAck(ctx context.Context, connectionName string, ev *sessionv1.DirectPeerAckEvent) error {
	if ev == nil {
		return nil
	}
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		UPDATE dm_messages SET state = 'acked', acked_at_unix = ? WHERE connection_name = ? AND message_id = ?
	`, now, connectionName, ev.MessageId)
	return err
}

// ListConversations 列出會話。
func (s *Store) ListConversations(ctx context.Context, connectionName string) ([]ConversationRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT connection_name, conversation_id, peer_user_id, peer_display_name, last_seq, unread_count, last_message, updated_at_unix
		FROM dm_conversations WHERE connection_name = ? ORDER BY updated_at_unix DESC
	`, connectionName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConversationRow
	for rows.Next() {
		var r ConversationRow
		if err := rows.Scan(&r.Connection, &r.ConversationID, &r.PeerUserID, &r.PeerDisplayName, &r.LastSeq, &r.UnreadCount, &r.LastMessage, &r.UpdatedAtUnix); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListMessages 列出某會話訊息。
func (s *Store) ListMessages(ctx context.Context, connectionName, conversationID string, limit int) ([]MessageRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT connection_name, conversation_id, message_id, seq, from_user_id, to_user_id, direction, msg_type, plaintext, state, created_at_unix, acked_at_unix
		FROM dm_messages WHERE connection_name = ? AND conversation_id = ?
		ORDER BY seq DESC LIMIT ?
	`, connectionName, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageRow
	for rows.Next() {
		var r MessageRow
		var ack sql.NullInt64
		if err := rows.Scan(&r.Connection, &r.ConversationID, &r.MessageID, &r.Seq, &r.FromUserID, &r.ToUserID, &r.Direction, &r.MsgType, &r.Plaintext, &r.State, &r.CreatedAtUnix, &ack); err != nil {
			return nil, err
		}
		if ack.Valid {
			v := ack.Int64
			r.AckedAtUnix = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetSyncAfterSeq 讀取同步游標。
func (s *Store) GetSyncAfterSeq(ctx context.Context, connectionName, conversationID string) (uint64, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT after_seq FROM dm_sync_cursor WHERE connection_name = ? AND conversation_id = ?`, connectionName, conversationID).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return uint64(v.Int64), nil
}

// SetSyncAfterSeq 寫入同步游標。
func (s *Store) SetSyncAfterSeq(ctx context.Context, connectionName, conversationID string, afterSeq uint64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO dm_sync_cursor (connection_name, conversation_id, after_seq) VALUES (?, ?, ?)
		ON CONFLICT(connection_name, conversation_id) DO UPDATE SET after_seq = excluded.after_seq
	`, connectionName, conversationID, int64(afterSeq))
	return err
}

// AddPendingAck 記錄待客戶端 ACK。
func (s *Store) AddPendingAck(ctx context.Context, connectionName, messageID string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO dm_pending_ack (connection_name, message_id, created_at_unix) VALUES (?, ?, ?)
		ON CONFLICT(connection_name, message_id) DO NOTHING
	`, connectionName, messageID, now)
	return err
}

// RemovePendingAck 移除待 ACK。
func (s *Store) RemovePendingAck(ctx context.Context, connectionName, messageID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM dm_pending_ack WHERE connection_name = ? AND message_id = ?`, connectionName, messageID)
	return err
}

// MergeListFromServer 用伺服端列表覆蓋/補全本地 meta。
func (s *Store) MergeListFromServer(ctx context.Context, connectionName string, items []*sessionv1.DirectConversationSummary) error {
	for _, it := range items {
		if it == nil {
			continue
		}
		if err := s.UpsertConversationMeta(ctx, connectionName, it.ConversationId, it.PeerUserId, it.PeerDisplayName, it.LastSeq, "", 0); err != nil {
			return err
		}
	}
	return nil
}

// ClearUnread 將未讀清零。
func (s *Store) ClearUnread(ctx context.Context, connectionName, conversationID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE dm_conversations SET unread_count = 0 WHERE connection_name = ? AND conversation_id = ?`, connectionName, conversationID)
	return err
}
