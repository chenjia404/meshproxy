package chat

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const archivedGroupPurgeAfter = 24 * time.Hour

type groupRetryDelivery struct {
	MsgID          string
	GroupID        string
	PeerID         string
	Epoch          uint64
	SenderPeerID   string
	SenderSeq      uint64
	MsgType        string
	Plaintext      string
	FileName       string
	MIMEType       string
	FileSize       int64
	FileCID        string
	CiphertextBlob []byte
	Signature      []byte
	SentAtUnix     int64
	RetryCount     int
}

type groupRetryEventDelivery struct {
	EventID       string
	GroupID       string
	PeerID        string
	EventSeq      uint64
	EventType     string
	ActorPeerID   string
	SignerPeerID  string
	PayloadJSON   string
	Signature     []byte
	CreatedAtUnix int64
	RetryCount    int
}

type groupSyncFileMessage struct {
	MsgID        string
	GroupID      string
	Epoch        uint64
	SenderPeerID string
	SenderSeq    uint64
	FileName     string
	MIMEType     string
	FileSize     int64
	FileCID      string
	PlainBlob    []byte
	SentAtUnix   int64
	Signature    []byte
}

func (s *Store) ensureGroupTables() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS groups (
			group_id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			avatar TEXT NOT NULL DEFAULT '',
			controller_peer_id TEXT NOT NULL,
			current_epoch INTEGER NOT NULL,
			retention_minutes INTEGER NOT NULL DEFAULT 0,
			state TEXT NOT NULL,
			last_event_seq INTEGER NOT NULL DEFAULT 0,
			last_message_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS group_members (
			group_id TEXT NOT NULL,
			peer_id TEXT NOT NULL,
			role TEXT NOT NULL,
			state TEXT NOT NULL,
			invited_by TEXT NOT NULL DEFAULT '',
			joined_epoch INTEGER NOT NULL DEFAULT 0,
			left_epoch INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(group_id, peer_id)
		);`,
		`CREATE TABLE IF NOT EXISTS group_events (
			event_id TEXT PRIMARY KEY,
			group_id TEXT NOT NULL,
			event_seq INTEGER NOT NULL,
			event_type TEXT NOT NULL,
			actor_peer_id TEXT NOT NULL,
			signer_peer_id TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL,
			signature BLOB NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(group_id, event_seq)
		);`,
		`CREATE TABLE IF NOT EXISTS group_epochs (
			group_id TEXT NOT NULL,
			epoch INTEGER NOT NULL,
			wrapped_key_for_local BLOB NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(group_id, epoch)
		);`,
		`CREATE TABLE IF NOT EXISTS group_messages (
			msg_id TEXT PRIMARY KEY,
			group_id TEXT NOT NULL,
			epoch INTEGER NOT NULL,
			sender_peer_id TEXT NOT NULL,
			sender_seq INTEGER NOT NULL,
			msg_type TEXT NOT NULL,
			plaintext TEXT NOT NULL,
			file_name TEXT NOT NULL DEFAULT '',
			mime_type TEXT NOT NULL DEFAULT '',
			file_size INTEGER NOT NULL DEFAULT 0,
			file_cid TEXT NOT NULL DEFAULT '',
			ciphertext_blob BLOB NOT NULL,
			signature BLOB NOT NULL,
			state TEXT NOT NULL,
			sent_at TEXT NOT NULL,
			delivered_summary TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS group_message_deliveries (
			msg_id TEXT NOT NULL,
			peer_id TEXT NOT NULL,
			transport_mode TEXT NOT NULL,
			state TEXT NOT NULL,
			retry_count INTEGER NOT NULL DEFAULT 0,
			next_retry_at TEXT NOT NULL DEFAULT '',
			delivered_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL,
			PRIMARY KEY(msg_id, peer_id)
		);`,
		`CREATE TABLE IF NOT EXISTS group_sync_cursors (
			group_id TEXT NOT NULL,
			peer_id TEXT NOT NULL,
			max_sender_seq INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(group_id, peer_id)
		);`,
		`CREATE TABLE IF NOT EXISTS group_event_deliveries (
			event_id TEXT NOT NULL,
			peer_id TEXT NOT NULL,
			state TEXT NOT NULL,
			retry_count INTEGER NOT NULL DEFAULT 0,
			next_retry_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL,
			PRIMARY KEY(event_id, peer_id)
		);`,
		`CREATE TABLE IF NOT EXISTS group_message_revocations (
			group_id TEXT NOT NULL,
			msg_id TEXT NOT NULL,
			sender_peer_id TEXT NOT NULL DEFAULT '',
			revoked_by_peer_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			PRIMARY KEY(group_id, msg_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_groups_updated_at ON groups(updated_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_group_members_state ON group_members(group_id, state);`,
		`CREATE INDEX IF NOT EXISTS idx_group_events_group_seq ON group_events(group_id, event_seq);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_group_messages_sender_seq ON group_messages(group_id, sender_peer_id, sender_seq);`,
		`CREATE INDEX IF NOT EXISTS idx_group_messages_group_time ON group_messages(group_id, sent_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_group_deliveries_retry ON group_message_deliveries(state, next_retry_at);`,
		`CREATE INDEX IF NOT EXISTS idx_group_event_deliveries_retry ON group_event_deliveries(state, next_retry_at);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate group tables: %w", err)
		}
	}
	if err := s.ensureGroupEventSignerColumn(); err != nil {
		return err
	}
	if err := s.ensureGroupRetentionColumn(); err != nil {
		return err
	}
	if err := s.ensureGroupMessageFileCIDColumn(); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureGroupMessageFileCIDColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(group_messages)`)
	if err != nil {
		return err
	}
	hasFileCID := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "file_cid" {
			hasFileCID = true
			break
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasFileCID {
		return nil
	}
	_, err = s.db.Exec(`ALTER TABLE group_messages ADD COLUMN file_cid TEXT NOT NULL DEFAULT ''`)
	return err
}

func (s *Store) ensureGroupEventSignerColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(group_events)`)
	if err != nil {
		return err
	}
	hasSigner := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "signer_peer_id" {
			hasSigner = true
			break
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !hasSigner {
		if _, err := s.db.Exec(`ALTER TABLE group_events ADD COLUMN signer_peer_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	_, err = s.db.Exec(`UPDATE group_events SET signer_peer_id=actor_peer_id WHERE signer_peer_id=''`)
	return err
}

func (s *Store) ensureGroupRetentionColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(groups)`)
	if err != nil {
		return err
	}
	hasRetention := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "retention_minutes" {
			hasRetention = true
			break
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasRetention {
		return nil
	}
	_, err = s.db.Exec(`ALTER TABLE groups ADD COLUMN retention_minutes INTEGER NOT NULL DEFAULT 0`)
	return err
}

func (s *Store) ListGroups() ([]Group, error) {
	rows, err := s.db.Query(`
		SELECT
			g.group_id,
			g.title,
			g.avatar,
			g.controller_peer_id,
			g.current_epoch,
			g.retention_minutes,
			g.state,
			g.last_event_seq,
			g.last_message_at,
			COALESCE((
				SELECT COUNT(*)
				FROM group_members gm
				WHERE gm.group_id = g.group_id
				  AND gm.state IN (?, ?)
			), 0) AS member_count,
			COALESCE((
				SELECT gm_self.role
				FROM group_members gm_self
				WHERE gm_self.group_id = g.group_id
				  AND gm_self.peer_id = (SELECT peer_id FROM profile LIMIT 1)
				LIMIT 1
			), '') AS local_member_role,
			COALESCE((
				SELECT gm_self.state
				FROM group_members gm_self
				WHERE gm_self.group_id = g.group_id
				  AND gm_self.peer_id = (SELECT peer_id FROM profile LIMIT 1)
				LIMIT 1
			), '') AS local_member_state,
			g.created_at,
			g.updated_at
		FROM groups g
		ORDER BY g.updated_at DESC
	`, GroupMemberStateActive, GroupMemberStateInvited)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		group, err := scanGroup(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, group)
	}
	return out, rows.Err()
}

func (s *Store) GetGroup(groupID string) (Group, error) {
	row := s.db.QueryRow(`
		SELECT
			g.group_id,
			g.title,
			g.avatar,
			g.controller_peer_id,
			g.current_epoch,
			g.retention_minutes,
			g.state,
			g.last_event_seq,
			g.last_message_at,
			COALESCE((
				SELECT COUNT(*)
				FROM group_members gm
				WHERE gm.group_id = g.group_id
				  AND gm.state IN (?, ?)
			), 0) AS member_count,
			COALESCE((
				SELECT gm_self.role
				FROM group_members gm_self
				WHERE gm_self.group_id = g.group_id
				  AND gm_self.peer_id = (SELECT peer_id FROM profile LIMIT 1)
				LIMIT 1
			), '') AS local_member_role,
			COALESCE((
				SELECT gm_self.state
				FROM group_members gm_self
				WHERE gm_self.group_id = g.group_id
				  AND gm_self.peer_id = (SELECT peer_id FROM profile LIMIT 1)
				LIMIT 1
			), '') AS local_member_state,
			g.created_at,
			g.updated_at
		FROM groups g
		WHERE g.group_id = ?
	`, GroupMemberStateActive, GroupMemberStateInvited, groupID)
	return scanGroup(row.Scan)
}

func (s *Store) ListGroupMembers(groupID string) ([]GroupMember, error) {
	rows, err := s.db.Query(`
		SELECT group_id, peer_id, role, state, invited_by, joined_epoch, left_epoch, updated_at
		FROM group_members
		WHERE group_id=?
		ORDER BY
			CASE role
				WHEN ? THEN 0
				WHEN ? THEN 1
				ELSE 2
			END,
			updated_at ASC,
			peer_id ASC
	`, groupID, GroupRoleController, GroupRoleAdmin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupMember
	for rows.Next() {
		member, err := scanGroupMember(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, member)
	}
	return out, rows.Err()
}

func (s *Store) GetGroupMember(groupID, peerID string) (GroupMember, error) {
	row := s.db.QueryRow(`
		SELECT group_id, peer_id, role, state, invited_by, joined_epoch, left_epoch, updated_at
		FROM group_members
		WHERE group_id=? AND peer_id=?
	`, groupID, peerID)
	return scanGroupMember(row.Scan)
}

func (s *Store) GetGroupEpoch(groupID string, epoch uint64) (GroupEpoch, error) {
	var (
		groupEpoch GroupEpoch
		createdAt  string
	)
	err := s.db.QueryRow(`
		SELECT group_id, epoch, wrapped_key_for_local, created_at
		FROM group_epochs
		WHERE group_id=? AND epoch=?
	`, groupID, epoch).Scan(&groupEpoch.GroupID, &groupEpoch.Epoch, &groupEpoch.WrappedKeyForLocal, &createdAt)
	if err != nil {
		return GroupEpoch{}, err
	}
	groupEpoch.CreatedAt = parseDBTime(createdAt)
	return groupEpoch, nil
}

func (s *Store) UpsertGroupEpoch(epoch GroupEpoch) error {
	_, err := s.db.Exec(`
		INSERT INTO group_epochs(group_id,epoch,wrapped_key_for_local,created_at)
		VALUES(?,?,?,?)
		ON CONFLICT(group_id, epoch) DO UPDATE SET
			wrapped_key_for_local=excluded.wrapped_key_for_local,
			created_at=excluded.created_at
	`, epoch.GroupID, epoch.Epoch, epoch.WrappedKeyForLocal, formatDBTime(epoch.CreatedAt))
	return err
}

func (s *Store) NextGroupSenderSeq(groupID, senderPeerID string) (uint64, error) {
	var maxSeq sql.NullInt64
	if err := s.db.QueryRow(`
		SELECT COALESCE(MAX(sender_seq), 0)
		FROM group_messages
		WHERE group_id=? AND sender_peer_id=?
	`, groupID, senderPeerID).Scan(&maxSeq); err != nil {
		return 0, err
	}
	return uint64(maxSeq.Int64 + 1), nil
}

func (s *Store) AddGroupMessage(msg GroupMessage, ciphertext []byte, deliveries []GroupMessageDelivery) (GroupMessage, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return GroupMessage{}, err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(`
		INSERT OR REPLACE INTO group_messages(msg_id,group_id,epoch,sender_peer_id,sender_seq,msg_type,plaintext,file_name,mime_type,file_size,file_cid,ciphertext_blob,signature,state,sent_at,delivered_summary)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, msg.MsgID, msg.GroupID, msg.Epoch, msg.SenderPeerID, msg.SenderSeq, msg.MsgType, msg.Plaintext, msg.FileName, msg.MIMEType, msg.FileSize, msg.FileCID, ciphertext, msg.Signature, msg.State, formatDBTime(msg.CreatedAt), ""); err != nil {
		return GroupMessage{}, err
	}
	for _, delivery := range deliveries {
		if _, err := tx.Exec(`
			INSERT OR REPLACE INTO group_message_deliveries(msg_id,peer_id,transport_mode,state,retry_count,next_retry_at,delivered_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?)
		`, delivery.MsgID, delivery.PeerID, delivery.TransportMode, delivery.State, delivery.RetryCount, formatDBTime(delivery.NextRetryAt), formatDBTime(delivery.DeliveredAt), formatDBTime(delivery.UpdatedAt)); err != nil {
			return GroupMessage{}, err
		}
	}
	if _, err := tx.Exec(`UPDATE groups SET last_message_at=?, updated_at=? WHERE group_id=?`,
		formatDBTime(msg.CreatedAt), formatDBTime(msg.CreatedAt), msg.GroupID); err != nil {
		return GroupMessage{}, err
	}
	if _, err := tx.Exec(`
		INSERT INTO group_sync_cursors(group_id,peer_id,max_sender_seq,updated_at)
		VALUES(?,?,?,?)
		ON CONFLICT(group_id, peer_id) DO UPDATE SET
			max_sender_seq=CASE
				WHEN excluded.max_sender_seq > group_sync_cursors.max_sender_seq THEN excluded.max_sender_seq
				ELSE group_sync_cursors.max_sender_seq
			END,
			updated_at=excluded.updated_at
	`, msg.GroupID, msg.SenderPeerID, msg.SenderSeq, formatDBTime(msg.CreatedAt)); err != nil {
		return GroupMessage{}, err
	}
	if err := tx.Commit(); err != nil {
		return GroupMessage{}, err
	}
	if len(deliveries) > 0 {
		if err := s.refreshGroupMessageState(msg.MsgID); err != nil {
			return GroupMessage{}, err
		}
		return s.GetGroupMessage(msg.MsgID)
	}
	return msg, nil
}

func (s *Store) ListGroupMessages(groupID string) ([]GroupMessage, error) {
	rows, err := s.db.Query(`
		SELECT msg_id,group_id,epoch,sender_peer_id,sender_seq,msg_type,plaintext,file_name,mime_type,file_size,file_cid,signature,state,sent_at,delivered_summary
		FROM group_messages
		WHERE group_id=?
		ORDER BY sent_at ASC
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupMessage
	for rows.Next() {
		var (
			msg              GroupMessage
			sentAt           string
			deliveredSummary string
		)
		if err := rows.Scan(&msg.MsgID, &msg.GroupID, &msg.Epoch, &msg.SenderPeerID, &msg.SenderSeq, &msg.MsgType, &msg.Plaintext, &msg.FileName, &msg.MIMEType, &msg.FileSize, &msg.FileCID, &msg.Signature, &msg.State, &sentAt, &deliveredSummary); err != nil {
			return nil, err
		}
		msg.CreatedAt = parseDBTime(sentAt)
		msg.DeliverySummary = parseGroupDeliverySummary(deliveredSummary)
		out = append(out, msg)
	}
	return out, rows.Err()
}

func (s *Store) GetGroupMessage(msgID string) (GroupMessage, error) {
	var (
		msg              GroupMessage
		sentAt           string
		deliveredSummary string
	)
	err := s.db.QueryRow(`
		SELECT msg_id,group_id,epoch,sender_peer_id,sender_seq,msg_type,plaintext,file_name,mime_type,file_size,file_cid,signature,state,sent_at,delivered_summary
		FROM group_messages
		WHERE msg_id=?
	`, msgID).Scan(&msg.MsgID, &msg.GroupID, &msg.Epoch, &msg.SenderPeerID, &msg.SenderSeq, &msg.MsgType, &msg.Plaintext, &msg.FileName, &msg.MIMEType, &msg.FileSize, &msg.FileCID, &msg.Signature, &msg.State, &sentAt, &deliveredSummary)
	if err != nil {
		return GroupMessage{}, err
	}
	msg.CreatedAt = parseDBTime(sentAt)
	msg.DeliverySummary = parseGroupDeliverySummary(deliveredSummary)
	return msg, nil
}

func (s *Store) DeleteGroupMessage(groupID, msgID string) error {
	if groupID == "" || msgID == "" {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(`DELETE FROM group_message_deliveries WHERE msg_id=?`, msgID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM group_messages WHERE group_id=? AND msg_id=?`, groupID, msgID); err != nil {
		return err
	}
	if err := refreshGroupMessageSummaryTx(tx, groupID, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetGroupMessageBlob(msgID string) ([]byte, error) {
	var blob []byte
	if err := s.db.QueryRow(`SELECT ciphertext_blob FROM group_messages WHERE msg_id=?`, msgID).Scan(&blob); err != nil {
		return nil, err
	}
	return blob, nil
}

func (s *Store) IsGroupMessageRevoked(groupID, msgID string) (bool, error) {
	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM group_message_revocations WHERE group_id=? AND msg_id=? LIMIT 1`, groupID, msgID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Store) RevokeGroupMessage(groupID, msgID, senderPeerID, revokedByPeerID string, event GroupEvent) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(`
		INSERT INTO group_message_revocations(group_id,msg_id,sender_peer_id,revoked_by_peer_id,created_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(group_id, msg_id) DO UPDATE SET
			sender_peer_id=excluded.sender_peer_id,
			revoked_by_peer_id=excluded.revoked_by_peer_id,
			created_at=excluded.created_at
	`, groupID, msgID, senderPeerID, revokedByPeerID, formatDBTime(event.CreatedAt)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM group_message_deliveries WHERE msg_id=?`, msgID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM group_messages WHERE group_id=? AND msg_id=?`, groupID, msgID); err != nil {
		return err
	}
	if err := insertGroupEventTx(tx, event); err != nil {
		return err
	}
	if err := updateGroupMetaTx(tx, groupID, event.EventSeq, nil, nil, event.CreatedAt); err != nil {
		return err
	}
	if err := refreshGroupMessageSummaryTx(tx, groupID, event.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MarkGroupDeliveryState(msgID, peerID, state string, deliveredAt time.Time) error {
	_, err := s.db.Exec(`
		UPDATE group_message_deliveries
		SET state=?, delivered_at=?, next_retry_at='', updated_at=?
		WHERE msg_id=? AND peer_id=?
	`, state, formatDBTime(deliveredAt), formatDBTime(time.Now().UTC()), msgID, peerID)
	if err != nil {
		return err
	}
	return s.refreshGroupMessageState(msgID)
}

func (s *Store) QueueGroupDeliveryRetry(msgID, peerID string, retryCount int, nextRetryAt time.Time) error {
	_, err := s.db.Exec(`
		UPDATE group_message_deliveries
		SET state=?, retry_count=?, next_retry_at=?, delivered_at='', updated_at=?
		WHERE msg_id=? AND peer_id=?
	`, GroupDeliveryStateQueuedForRetry, retryCount, formatDBTime(nextRetryAt), formatDBTime(time.Now().UTC()), msgID, peerID)
	if err != nil {
		return err
	}
	return s.refreshGroupMessageState(msgID)
}

func (s *Store) ListGroupDeliveriesForRetry(now time.Time, limit int) ([]groupRetryDelivery, error) {
	if limit <= 0 {
		limit = 32
	}
	rows, err := s.db.Query(`
		SELECT
			d.msg_id,
			m.group_id,
			d.peer_id,
			m.epoch,
			m.sender_peer_id,
			m.sender_seq,
			m.msg_type,
			m.plaintext,
			m.file_name,
			m.mime_type,
			m.file_size,
			m.file_cid,
			m.ciphertext_blob,
			m.signature,
			m.sent_at,
			d.retry_count
		FROM group_message_deliveries d
		INNER JOIN group_messages m ON m.msg_id = d.msg_id
		WHERE d.state=? AND d.next_retry_at != '' AND d.next_retry_at <= ?
		ORDER BY d.next_retry_at ASC, d.updated_at ASC
		LIMIT ?
	`, GroupDeliveryStateQueuedForRetry, formatDBTime(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []groupRetryDelivery
	for rows.Next() {
		var item groupRetryDelivery
		var sentAt string
		if err := rows.Scan(
			&item.MsgID,
			&item.GroupID,
			&item.PeerID,
			&item.Epoch,
			&item.SenderPeerID,
			&item.SenderSeq,
			&item.MsgType,
			&item.Plaintext,
			&item.FileName,
			&item.MIMEType,
			&item.FileSize,
			&item.FileCID,
			&item.CiphertextBlob,
			&item.Signature,
			&sentAt,
			&item.RetryCount,
		); err != nil {
			return nil, err
		}
		item.SentAtUnix = parseDBTime(sentAt).UnixMilli()
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListGroupDeliveriesAwaitingAck(now time.Time, ackTimeout time.Duration, limit int) ([]groupRetryDelivery, error) {
	if limit <= 0 {
		limit = 32
	}
	cutoff := now.Add(-ackTimeout)
	rows, err := s.db.Query(`
		SELECT
			d.msg_id,
			m.group_id,
			d.peer_id,
			m.epoch,
			m.sender_peer_id,
			m.sender_seq,
			m.msg_type,
			m.plaintext,
			m.file_name,
			m.mime_type,
			m.file_size,
			m.file_cid,
			m.ciphertext_blob,
			m.signature,
			m.sent_at,
			d.retry_count
		FROM group_message_deliveries d
		INNER JOIN group_messages m ON m.msg_id = d.msg_id
		WHERE d.state=? AND d.updated_at != '' AND d.updated_at <= ?
		ORDER BY d.updated_at ASC
		LIMIT ?
	`, GroupDeliveryStateSentToTransport, formatDBTime(cutoff), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []groupRetryDelivery
	for rows.Next() {
		var item groupRetryDelivery
		var sentAt string
		if err := rows.Scan(
			&item.MsgID,
			&item.GroupID,
			&item.PeerID,
			&item.Epoch,
			&item.SenderPeerID,
			&item.SenderSeq,
			&item.MsgType,
			&item.Plaintext,
			&item.FileName,
			&item.MIMEType,
			&item.FileSize,
			&item.FileCID,
			&item.CiphertextBlob,
			&item.Signature,
			&sentAt,
			&item.RetryCount,
		); err != nil {
			return nil, err
		}
		item.SentAtUnix = parseDBTime(sentAt).UnixMilli()
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) MarkGroupEventDeliverySent(eventID, peerID string) error {
	_, err := s.db.Exec(`
		INSERT INTO group_event_deliveries(event_id,peer_id,state,retry_count,next_retry_at,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(event_id, peer_id) DO UPDATE SET
			state=excluded.state,
			next_retry_at='',
			updated_at=excluded.updated_at
	`, eventID, peerID, GroupDeliveryStateSentToTransport, 0, "", formatDBTime(time.Now().UTC()))
	return err
}

func (s *Store) QueueGroupEventDeliveryRetry(eventID, peerID string, retryCount int, nextRetryAt time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO group_event_deliveries(event_id,peer_id,state,retry_count,next_retry_at,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(event_id, peer_id) DO UPDATE SET
			state=excluded.state,
			retry_count=excluded.retry_count,
			next_retry_at=excluded.next_retry_at,
			updated_at=excluded.updated_at
	`, eventID, peerID, GroupDeliveryStateQueuedForRetry, retryCount, formatDBTime(nextRetryAt), formatDBTime(time.Now().UTC()))
	return err
}

func (s *Store) ListGroupEventDeliveriesForRetry(now time.Time, limit int) ([]groupRetryEventDelivery, error) {
	if limit <= 0 {
		limit = 32
	}
	rows, err := s.db.Query(`
		SELECT
			d.event_id,
			e.group_id,
			d.peer_id,
			e.event_seq,
			e.event_type,
			e.actor_peer_id,
			e.signer_peer_id,
			e.payload_json,
			e.signature,
			e.created_at,
			d.retry_count
		FROM group_event_deliveries d
		INNER JOIN group_events e ON e.event_id = d.event_id
		WHERE d.state=? AND d.next_retry_at != '' AND d.next_retry_at <= ?
		ORDER BY d.next_retry_at ASC, d.updated_at ASC
		LIMIT ?
	`, GroupDeliveryStateQueuedForRetry, formatDBTime(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []groupRetryEventDelivery
	for rows.Next() {
		var item groupRetryEventDelivery
		var createdAt string
		if err := rows.Scan(
			&item.EventID,
			&item.GroupID,
			&item.PeerID,
			&item.EventSeq,
			&item.EventType,
			&item.ActorPeerID,
			&item.SignerPeerID,
			&item.PayloadJSON,
			&item.Signature,
			&createdAt,
			&item.RetryCount,
		); err != nil {
			return nil, err
		}
		item.CreatedAtUnix = parseDBTime(createdAt).UnixMilli()
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) refreshGroupMessageState(msgID string) error {
	rows, err := s.db.Query(`SELECT state FROM group_message_deliveries WHERE msg_id=?`, msgID)
	if err != nil {
		return err
	}
	defer rows.Close()
	summary := GroupDeliverySummary{}
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			return err
		}
		summary.Total++
		switch state {
		case GroupDeliveryStatePending:
			summary.Pending++
		case GroupDeliveryStateDeliveredRemote:
			summary.DeliveredRemote++
		case GroupDeliveryStateSentToTransport:
			summary.SentToTransport++
		case GroupDeliveryStateQueuedForRetry:
			summary.QueuedForRetry++
		case GroupDeliveryStateFailed:
			summary.Failed++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	nextState := GroupMessageStateLocalOnly
	switch {
	case summary.Total == 0:
		nextState = GroupMessageStateLocalOnly
	case summary.DeliveredRemote == summary.Total:
		nextState = GroupMessageStateDeliveredRemote
	case summary.DeliveredRemote > 0:
		nextState = GroupMessageStatePartiallyDelivered
	case summary.QueuedForRetry == summary.Total:
		nextState = GroupMessageStateQueuedForRetry
	case summary.QueuedForRetry > 0 && (summary.SentToTransport > 0 || summary.Pending > 0):
		nextState = GroupMessageStatePartiallySent
	case summary.Failed > 0 && summary.SentToTransport > 0:
		nextState = GroupMessageStateFailedPartial
	case summary.Failed > 0:
		nextState = GroupMessageStateFailedPartial
	case summary.SentToTransport == summary.Total:
		nextState = GroupMessageStateSentToTransport
	case summary.SentToTransport > 0 || summary.Pending > 0:
		nextState = GroupMessageStatePartiallySent
	}
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE group_messages SET state=?, delivered_summary=? WHERE msg_id=?`, nextState, string(summaryJSON), msgID)
	return err
}

func (s *Store) ListGroupEventsAfter(groupID string, lastEventSeq uint64) ([]GroupControlEnvelope, error) {
	rows, err := s.db.Query(`
		SELECT event_id, group_id, event_seq, event_type, actor_peer_id, signer_peer_id, payload_json, signature, created_at
		FROM group_events
		WHERE group_id=? AND event_seq > ?
		ORDER BY event_seq ASC
	`, groupID, lastEventSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupControlEnvelope
	for rows.Next() {
		var (
			eventID, gid, eventType, actorPeerID, signerPeerID, payloadJSON, createdAt string
			eventSeq                                                                   uint64
			signature                                                                  []byte
		)
		if err := rows.Scan(&eventID, &gid, &eventSeq, &eventType, &actorPeerID, &signerPeerID, &payloadJSON, &signature, &createdAt); err != nil {
			return nil, err
		}
		out = append(out, GroupControlEnvelope{
			Type:          MessageTypeGroupControl,
			EventType:     eventType,
			GroupID:       gid,
			EventID:       eventID,
			EventSeq:      eventSeq,
			ActorPeerID:   actorPeerID,
			SignerPeerID:  signerPeerID,
			CreatedAtUnix: parseDBTime(createdAt).UnixMilli(),
			Payload:       json.RawMessage(payloadJSON),
			Signature:     signature,
		})
	}
	return out, rows.Err()
}

func (s *Store) ListRecentGroupEvents(groupID string, limit int) ([]GroupEventView, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT
			e.event_id,
			e.group_id,
			e.event_seq,
			e.event_type,
			e.actor_peer_id,
			e.signer_peer_id,
			e.payload_json,
			e.created_at,
			COALESCE(SUM(CASE WHEN d.state = ? THEN 1 ELSE 0 END), 0) AS sent_to_transport,
			COALESCE(SUM(CASE WHEN d.state = ? THEN 1 ELSE 0 END), 0) AS queued_for_retry,
			COALESCE(SUM(CASE WHEN d.state = ? THEN 1 ELSE 0 END), 0) AS failed,
			COUNT(d.peer_id) AS total
		FROM group_events e
		LEFT JOIN group_event_deliveries d ON d.event_id = e.event_id
		WHERE e.group_id = ?
		GROUP BY e.event_id, e.group_id, e.event_seq, e.event_type, e.actor_peer_id, e.signer_peer_id, e.payload_json, e.created_at
		ORDER BY e.event_seq DESC
		LIMIT ?
	`, GroupDeliveryStateSentToTransport, GroupDeliveryStateQueuedForRetry, GroupDeliveryStateFailed, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupEventView
	for rows.Next() {
		var (
			item      GroupEventView
			createdAt string
		)
		if err := rows.Scan(
			&item.EventID,
			&item.GroupID,
			&item.EventSeq,
			&item.EventType,
			&item.ActorPeerID,
			&item.SignerPeerID,
			&item.PayloadJSON,
			&createdAt,
			&item.DeliverySummary.SentToTransport,
			&item.DeliverySummary.QueuedForRetry,
			&item.DeliverySummary.Failed,
			&item.DeliverySummary.Total,
		); err != nil {
			return nil, err
		}
		item.CreatedAt = parseDBTime(createdAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListGroupSenderCursors(groupID string) (map[string]uint64, error) {
	rows, err := s.db.Query(`
		SELECT peer_id, max_sender_seq
		FROM group_sync_cursors
		WHERE group_id=?
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uint64{}
	for rows.Next() {
		var peerID string
		var maxSenderSeq uint64
		if err := rows.Scan(&peerID, &maxSenderSeq); err != nil {
			return nil, err
		}
		out[peerID] = maxSenderSeq
	}
	return out, rows.Err()
}

func (s *Store) ListGroupMessagesForSync(groupID string, cursors map[string]uint64) ([]GroupChatText, error) {
	rows, err := s.db.Query(`
		SELECT msg_id, group_id, epoch, sender_peer_id, sender_seq, msg_type, ciphertext_blob, signature, sent_at
		FROM group_messages
		WHERE group_id=? AND msg_type=?
		ORDER BY sent_at ASC
	`, groupID, MessageTypeGroupChatText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupChatText
	for rows.Next() {
		var (
			msg       GroupChatText
			msgType   string
			signature []byte
			sentAt    string
		)
		if err := rows.Scan(&msg.MsgID, &msg.GroupID, &msg.Epoch, &msg.SenderPeerID, &msg.SenderSeq, &msgType, &msg.Ciphertext, &signature, &sentAt); err != nil {
			return nil, err
		}
		if cursors[msg.SenderPeerID] >= msg.SenderSeq {
			continue
		}
		msg.Type = msgType
		msg.SentAtUnix = parseDBTime(sentAt).UnixMilli()
		msg.Signature = signature
		out = append(out, msg)
	}
	return out, rows.Err()
}

func (s *Store) ListGroupFileMessagesForSync(groupID string, cursors map[string]uint64) ([]groupSyncFileMessage, error) {
	rows, err := s.db.Query(`
		SELECT msg_id, group_id, epoch, sender_peer_id, sender_seq, file_name, mime_type, file_size, file_cid, ciphertext_blob, signature, sent_at
		FROM group_messages
		WHERE group_id=? AND msg_type=?
		ORDER BY sent_at ASC
	`, groupID, MessageTypeGroupChatFile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []groupSyncFileMessage
	for rows.Next() {
		var (
			msg    groupSyncFileMessage
			sentAt string
		)
		if err := rows.Scan(&msg.MsgID, &msg.GroupID, &msg.Epoch, &msg.SenderPeerID, &msg.SenderSeq, &msg.FileName, &msg.MIMEType, &msg.FileSize, &msg.FileCID, &msg.PlainBlob, &msg.Signature, &sentAt); err != nil {
			return nil, err
		}
		if cursors[msg.SenderPeerID] >= msg.SenderSeq {
			continue
		}
		msg.SentAtUnix = parseDBTime(sentAt).UnixMilli()
		out = append(out, msg)
	}
	return out, rows.Err()
}

func parseGroupDeliverySummary(v string) GroupDeliverySummary {
	if strings.TrimSpace(v) == "" {
		return GroupDeliverySummary{}
	}
	var summary GroupDeliverySummary
	if err := json.Unmarshal([]byte(v), &summary); err != nil {
		return GroupDeliverySummary{}
	}
	return summary
}

func (s *Store) CreateGroup(group Group, epoch GroupEpoch, members []GroupMember, event GroupEvent) (Group, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Group{}, err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(`
		INSERT INTO groups(group_id,title,avatar,controller_peer_id,current_epoch,retention_minutes,state,last_event_seq,last_message_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
	`, group.GroupID, group.Title, group.Avatar, group.ControllerPeerID, group.CurrentEpoch, group.RetentionMinutes, group.State, group.LastEventSeq, formatDBTime(group.LastMessageAt), formatDBTime(group.CreatedAt), formatDBTime(group.UpdatedAt)); err != nil {
		return Group{}, err
	}
	for _, member := range members {
		if _, err := tx.Exec(`
			INSERT INTO group_members(group_id,peer_id,role,state,invited_by,joined_epoch,left_epoch,updated_at)
			VALUES(?,?,?,?,?,?,?,?)
		`, member.GroupID, member.PeerID, member.Role, member.State, member.InvitedBy, member.JoinedEpoch, member.LeftEpoch, formatDBTime(member.UpdatedAt)); err != nil {
			return Group{}, err
		}
	}
	if err := insertGroupEventTx(tx, event); err != nil {
		return Group{}, err
	}
	if _, err := tx.Exec(`
		INSERT INTO group_epochs(group_id,epoch,wrapped_key_for_local,created_at)
		VALUES(?,?,?,?)
	`, epoch.GroupID, epoch.Epoch, epoch.WrappedKeyForLocal, formatDBTime(epoch.CreatedAt)); err != nil {
		return Group{}, err
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return s.GetGroup(group.GroupID)
}

func (s *Store) UpsertGroupMember(groupID string, member GroupMember, event GroupEvent) (Group, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Group{}, err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(`
		INSERT INTO group_members(group_id,peer_id,role,state,invited_by,joined_epoch,left_epoch,updated_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(group_id, peer_id) DO UPDATE SET
			role=excluded.role,
			state=excluded.state,
			invited_by=excluded.invited_by,
			joined_epoch=excluded.joined_epoch,
			left_epoch=excluded.left_epoch,
			updated_at=excluded.updated_at
	`, groupID, member.PeerID, member.Role, member.State, member.InvitedBy, member.JoinedEpoch, member.LeftEpoch, formatDBTime(member.UpdatedAt)); err != nil {
		return Group{}, err
	}
	if err := insertGroupEventTx(tx, event); err != nil {
		return Group{}, err
	}
	if err := updateGroupMetaTx(tx, groupID, event.EventSeq, nil, nil, event.CreatedAt); err != nil {
		return Group{}, err
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return s.GetGroup(groupID)
}

func (s *Store) ActivateGroupMember(groupID, peerID string, joinedEpoch uint64, event GroupEvent) (Group, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Group{}, err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(`
		UPDATE group_members
		SET state=?, joined_epoch=?, left_epoch=0, updated_at=?
		WHERE group_id=? AND peer_id=?
	`, GroupMemberStateActive, joinedEpoch, formatDBTime(event.CreatedAt), groupID, peerID); err != nil {
		return Group{}, err
	}
	if err := insertGroupEventTx(tx, event); err != nil {
		return Group{}, err
	}
	if err := updateGroupMetaTx(tx, groupID, event.EventSeq, nil, nil, event.CreatedAt); err != nil {
		return Group{}, err
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return s.GetGroup(groupID)
}

func (s *Store) MarkGroupMemberLeft(groupID, peerID string, leftEpoch uint64, nextEpoch GroupEpoch, event GroupEvent) (Group, error) {
	return s.finishGroupMembershipChange(groupID, peerID, GroupMemberStateLeft, leftEpoch, nextEpoch, event)
}

func (s *Store) MarkGroupMemberRemoved(groupID, peerID string, leftEpoch uint64, nextEpoch GroupEpoch, event GroupEvent) (Group, error) {
	return s.finishGroupMembershipChange(groupID, peerID, GroupMemberStateRemoved, leftEpoch, nextEpoch, event)
}

func (s *Store) UpdateGroupTitle(groupID, title string, event GroupEvent) (Group, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Group{}, err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(`UPDATE groups SET title=?, updated_at=?, last_event_seq=? WHERE group_id=?`,
		title, formatDBTime(event.CreatedAt), event.EventSeq, groupID); err != nil {
		return Group{}, err
	}
	if err := insertGroupEventTx(tx, event); err != nil {
		return Group{}, err
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return s.GetGroup(groupID)
}

func (s *Store) UpdateGroupRetention(groupID string, minutes int, event GroupEvent) (Group, error) {
	if minutes != 0 && (minutes < MinRetentionMinutes || minutes > MaxRetentionMinutes) {
		return Group{}, fmt.Errorf("retention_minutes must be 0 or between %d and %d", MinRetentionMinutes, MaxRetentionMinutes)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Group{}, err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(`UPDATE groups SET retention_minutes=?, updated_at=?, last_event_seq=? WHERE group_id=?`,
		minutes, formatDBTime(event.CreatedAt), event.EventSeq, groupID); err != nil {
		return Group{}, err
	}
	if err := insertGroupEventTx(tx, event); err != nil {
		return Group{}, err
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	if minutes > 0 {
		if err := s.cleanupExpiredGroupMessages(groupID, minutes, time.Now().UTC()); err != nil {
			return Group{}, err
		}
	}
	return s.GetGroup(groupID)
}

func (s *Store) DissolveGroup(groupID string, event GroupEvent) (Group, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Group{}, err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(`UPDATE groups SET state=?, last_message_at='', updated_at=?, last_event_seq=? WHERE group_id=?`,
		GroupStateArchived, formatDBTime(event.CreatedAt), event.EventSeq, groupID); err != nil {
		return Group{}, err
	}
	if err := purgeGroupMessagesTx(tx, groupID, event.CreatedAt); err != nil {
		return Group{}, err
	}
	if err := insertGroupEventTx(tx, event); err != nil {
		return Group{}, err
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return s.GetGroup(groupID)
}

func (s *Store) TransferGroupController(groupID, fromPeerID, toPeerID string, nextEpoch GroupEpoch, event GroupEvent) (Group, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Group{}, err
	}
	defer rollbackTx(tx)
	ts := formatDBTime(event.CreatedAt)
	if _, err := tx.Exec(`
		UPDATE group_members
		SET role=?, updated_at=?
		WHERE group_id=? AND peer_id=?
	`, GroupRoleMember, ts, groupID, fromPeerID); err != nil {
		return Group{}, err
	}
	if _, err := tx.Exec(`
		UPDATE group_members
		SET role=?, updated_at=?
		WHERE group_id=? AND peer_id=?
	`, GroupRoleController, ts, groupID, toPeerID); err != nil {
		return Group{}, err
	}
	if _, err := tx.Exec(`
		INSERT INTO group_epochs(group_id,epoch,wrapped_key_for_local,created_at)
		VALUES(?,?,?,?)
	`, nextEpoch.GroupID, nextEpoch.Epoch, nextEpoch.WrappedKeyForLocal, formatDBTime(nextEpoch.CreatedAt)); err != nil {
		return Group{}, err
	}
	currentEpoch := nextEpoch.Epoch
	if err := updateGroupMetaTx(tx, groupID, event.EventSeq, &toPeerID, &currentEpoch, event.CreatedAt); err != nil {
		return Group{}, err
	}
	if err := insertGroupEventTx(tx, event); err != nil {
		return Group{}, err
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return s.GetGroup(groupID)
}

func (s *Store) finishGroupMembershipChange(groupID, peerID, state string, leftEpoch uint64, nextEpoch GroupEpoch, event GroupEvent) (Group, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Group{}, err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(`
		UPDATE group_members
		SET state=?, left_epoch=?, updated_at=?
		WHERE group_id=? AND peer_id=?
	`, state, leftEpoch, formatDBTime(event.CreatedAt), groupID, peerID); err != nil {
		return Group{}, err
	}
	if _, err := tx.Exec(`
		INSERT INTO group_epochs(group_id,epoch,wrapped_key_for_local,created_at)
		VALUES(?,?,?,?)
	`, nextEpoch.GroupID, nextEpoch.Epoch, nextEpoch.WrappedKeyForLocal, formatDBTime(nextEpoch.CreatedAt)); err != nil {
		return Group{}, err
	}
	if err := insertGroupEventTx(tx, event); err != nil {
		return Group{}, err
	}
	currentEpoch := nextEpoch.Epoch
	if err := updateGroupMetaTx(tx, groupID, event.EventSeq, nil, &currentEpoch, event.CreatedAt); err != nil {
		return Group{}, err
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return s.GetGroup(groupID)
}

func insertGroupEventTx(tx *sql.Tx, event GroupEvent) error {
	_, err := tx.Exec(`
		INSERT INTO group_events(event_id,group_id,event_seq,event_type,actor_peer_id,signer_peer_id,payload_json,signature,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)
	`, event.EventID, event.GroupID, event.EventSeq, event.EventType, event.ActorPeerID, event.SignerPeerID, event.PayloadJSON, event.Signature, formatDBTime(event.CreatedAt))
	return err
}

func updateGroupMetaTx(tx *sql.Tx, groupID string, eventSeq uint64, controllerPeerID *string, currentEpoch *uint64, updatedAt time.Time) error {
	var (
		query string
		args  []any
	)
	switch {
	case controllerPeerID != nil && currentEpoch != nil:
		query = `UPDATE groups SET controller_peer_id=?, current_epoch=?, last_event_seq=?, updated_at=? WHERE group_id=?`
		args = []any{*controllerPeerID, *currentEpoch, eventSeq, formatDBTime(updatedAt), groupID}
	case controllerPeerID != nil:
		query = `UPDATE groups SET controller_peer_id=?, last_event_seq=?, updated_at=? WHERE group_id=?`
		args = []any{*controllerPeerID, eventSeq, formatDBTime(updatedAt), groupID}
	case currentEpoch != nil:
		query = `UPDATE groups SET current_epoch=?, last_event_seq=?, updated_at=? WHERE group_id=?`
		args = []any{*currentEpoch, eventSeq, formatDBTime(updatedAt), groupID}
	default:
		query = `UPDATE groups SET last_event_seq=?, updated_at=? WHERE group_id=?`
		args = []any{eventSeq, formatDBTime(updatedAt), groupID}
	}
	_, err := tx.Exec(query, args...)
	return err
}

func (s *Store) CleanupExpiredGroupMessages(now time.Time) error {
	rows, err := s.db.Query(`SELECT group_id, retention_minutes FROM groups WHERE retention_minutes > 0`)
	if err != nil {
		return err
	}
	type retentionJob struct {
		groupID string
		minutes int
	}
	var jobs []retentionJob
	for rows.Next() {
		var groupID string
		var minutes int
		if err := rows.Scan(&groupID, &minutes); err != nil {
			_ = rows.Close()
			return err
		}
		jobs = append(jobs, retentionJob{groupID: groupID, minutes: minutes})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, job := range jobs {
		if err := s.cleanupExpiredGroupMessages(job.groupID, job.minutes, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CleanupArchivedGroups(now time.Time) error {
	cutoff := formatDBTime(now.Add(-archivedGroupPurgeAfter).UTC())
	rows, err := s.db.Query(`SELECT group_id FROM groups WHERE state=? AND updated_at != '' AND updated_at <= ?`, GroupStateArchived, cutoff)
	if err != nil {
		return err
	}
	defer rows.Close()
	var groupIDs []string
	for rows.Next() {
		var groupID string
		if err := rows.Scan(&groupID); err != nil {
			return err
		}
		groupIDs = append(groupIDs, groupID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, groupID := range groupIDs {
		if err := s.deleteGroupRecords(groupID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) cleanupExpiredGroupMessages(groupID string, minutes int, now time.Time) error {
	cutoff := formatDBTime(now.Add(-time.Duration(minutes) * time.Minute).UTC())
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(`
		DELETE FROM group_message_deliveries
		WHERE msg_id IN (
			SELECT msg_id
			FROM group_messages
			WHERE group_id=? AND sent_at <= ?
		)
	`, groupID, cutoff); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM group_messages WHERE group_id=? AND sent_at <= ?`, groupID, cutoff); err != nil {
		return err
	}
	if err := refreshGroupMessageSummaryTx(tx, groupID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) deleteGroupRecords(groupID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(`DELETE FROM group_event_deliveries WHERE event_id IN (SELECT event_id FROM group_events WHERE group_id=?)`, groupID); err != nil {
		return err
	}
	if err := purgeGroupMessagesTx(tx, groupID, time.Now().UTC()); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM group_events WHERE group_id=?`, groupID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM group_sync_cursors WHERE group_id=?`, groupID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM group_message_revocations WHERE group_id=?`, groupID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM group_epochs WHERE group_id=?`, groupID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM group_members WHERE group_id=?`, groupID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM groups WHERE group_id=?`, groupID); err != nil {
		return err
	}
	return tx.Commit()
}

func purgeGroupMessagesTx(tx *sql.Tx, groupID string, updatedAt time.Time) error {
	if _, err := tx.Exec(`DELETE FROM group_message_deliveries WHERE msg_id IN (SELECT msg_id FROM group_messages WHERE group_id=?)`, groupID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM group_messages WHERE group_id=?`, groupID); err != nil {
		return err
	}
	return refreshGroupMessageSummaryTx(tx, groupID, updatedAt)
}

func refreshGroupMessageSummaryTx(tx *sql.Tx, groupID string, updatedAt time.Time) error {
	var lastMessageAt sql.NullString
	if err := tx.QueryRow(`SELECT COALESCE(MAX(sent_at), '') FROM group_messages WHERE group_id=?`, groupID).Scan(&lastMessageAt); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE groups SET last_message_at=?, updated_at=? WHERE group_id=?`,
		lastMessageAt.String, formatDBTime(updatedAt.UTC()), groupID)
	return err
}

func rollbackTx(tx *sql.Tx) {
	if tx != nil {
		_ = tx.Rollback()
	}
}

func scanGroup(scan func(dest ...any) error) (Group, error) {
	var (
		group         Group
		lastMessageAt string
		createdAt     string
		updatedAt     string
	)
	err := scan(
		&group.GroupID,
		&group.Title,
		&group.Avatar,
		&group.ControllerPeerID,
		&group.CurrentEpoch,
		&group.RetentionMinutes,
		&group.State,
		&group.LastEventSeq,
		&lastMessageAt,
		&group.MemberCount,
		&group.LocalMemberRole,
		&group.LocalMemberState,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return Group{}, err
	}
	group.LastMessageAt = parseDBTime(lastMessageAt)
	group.CreatedAt = parseDBTime(createdAt)
	group.UpdatedAt = parseDBTime(updatedAt)
	return group, nil
}

func scanGroupMember(scan func(dest ...any) error) (GroupMember, error) {
	var (
		member    GroupMember
		updatedAt string
	)
	err := scan(
		&member.GroupID,
		&member.PeerID,
		&member.Role,
		&member.State,
		&member.InvitedBy,
		&member.JoinedEpoch,
		&member.LeftEpoch,
		&updatedAt,
	)
	if err != nil {
		return GroupMember{}, err
	}
	member.UpdatedAt = parseDBTime(updatedAt)
	return member, nil
}
