package chat

import (
	"fmt"
	"time"
)

// AddInboundMessageAndAdvanceRecvCounter 在同一 SQLite 事务中：写入入站消息、更新会话时间戳、
// 推进 session_states.recv_counter。若 incrementUnread 为 true，同时将 conversations.unread_count 加 1（实时入站；
// 历史同步回放应传 false）。提交成功后，上层再发 delivery_ack，避免「已回执但游标/库不一致」。
func (s *Store) AddInboundMessageAndAdvanceRecvCounter(msg Message, ciphertext []byte, newRecvCounter uint64, incrementUnread bool) error {
	if ciphertext == nil {
		ciphertext = []byte{}
	}
	deliveredAt := ""
	if !msg.DeliveredAt.IsZero() {
		deliveredAt = msg.DeliveredAt.UTC().Format(time.RFC3339Nano)
	}
	createdAt := msg.CreatedAt.UTC().Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	var committed bool
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.Exec(`
		INSERT OR REPLACE INTO messages(msg_id,conversation_id,sender_peer_id,receiver_peer_id,direction,msg_type,plaintext,file_name,mime_type,file_size,ciphertext_blob,transport_mode,state,counter,created_at,delivered_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, msg.MsgID, msg.ConversationID, msg.SenderPeerID, msg.ReceiverPeerID, msg.Direction, msg.MsgType, msg.Plaintext, msg.FileName, msg.MIMEType, msg.FileSize, ciphertext, msg.TransportMode, msg.State, msg.Counter, createdAt, deliveredAt); err != nil {
		return err
	}
	if incrementUnread {
		if _, err := tx.Exec(`UPDATE conversations SET last_message_at=?, updated_at=?, last_transport_mode=?, unread_count=unread_count+1 WHERE conversation_id=?`,
			createdAt, createdAt, msg.TransportMode, msg.ConversationID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`UPDATE conversations SET last_message_at=?, updated_at=?, last_transport_mode=? WHERE conversation_id=?`,
			createdAt, createdAt, msg.TransportMode, msg.ConversationID); err != nil {
			return err
		}
	}
	res, err := tx.Exec(`UPDATE session_states SET recv_counter=? WHERE conversation_id=?`, newRecvCounter, msg.ConversationID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("session_states missing for conversation %s", msg.ConversationID)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) AddMessage(msg Message, ciphertext []byte) (Message, error) {
	if ciphertext == nil {
		ciphertext = []byte{}
	}
	deliveredAt := ""
	if !msg.DeliveredAt.IsZero() {
		deliveredAt = msg.DeliveredAt.UTC().Format(time.RFC3339Nano)
	}
	createdAt := msg.CreatedAt.UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO messages(msg_id,conversation_id,sender_peer_id,receiver_peer_id,direction,msg_type,plaintext,file_name,mime_type,file_size,ciphertext_blob,transport_mode,state,counter,created_at,delivered_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, msg.MsgID, msg.ConversationID, msg.SenderPeerID, msg.ReceiverPeerID, msg.Direction, msg.MsgType, msg.Plaintext, msg.FileName, msg.MIMEType, msg.FileSize, ciphertext, msg.TransportMode, msg.State, msg.Counter, createdAt, deliveredAt)
	if err != nil {
		return Message{}, err
	}
	if _, err := s.db.Exec(`UPDATE conversations SET last_message_at=?, updated_at=?, last_transport_mode=? WHERE conversation_id=?`,
		createdAt, createdAt, msg.TransportMode, msg.ConversationID); err != nil {
		return Message{}, err
	}
	return msg, nil
}

func (s *Store) ListMessages(conversationID string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT msg_id,conversation_id,sender_peer_id,receiver_peer_id,direction,msg_type,plaintext,file_name,mime_type,file_size,transport_mode,state,counter,created_at,delivered_at FROM messages WHERE conversation_id=? ORDER BY created_at ASC`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var createdAt, deliveredAt string
		if err := rows.Scan(&m.MsgID, &m.ConversationID, &m.SenderPeerID, &m.ReceiverPeerID, &m.Direction, &m.MsgType, &m.Plaintext, &m.FileName, &m.MIMEType, &m.FileSize, &m.TransportMode, &m.State, &m.Counter, &createdAt, &deliveredAt); err != nil {
			return nil, err
		}
		m.CreatedAt = parseDBTime(createdAt)
		m.DeliveredAt = parseDBTime(deliveredAt)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListMessagesPage returns messages ordered by created_at ascending, and the total message count
// for the conversation (for pagination metadata).
func (s *Store) ListMessagesPage(conversationID string, limit, offset int) ([]Message, int, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=?`, conversationID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`
		SELECT msg_id,conversation_id,sender_peer_id,receiver_peer_id,direction,msg_type,plaintext,file_name,mime_type,file_size,transport_mode,state,counter,created_at,delivered_at
		FROM messages WHERE conversation_id=? ORDER BY created_at ASC LIMIT ? OFFSET ?`,
		conversationID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var createdAt, deliveredAt string
		if err := rows.Scan(&m.MsgID, &m.ConversationID, &m.SenderPeerID, &m.ReceiverPeerID, &m.Direction, &m.MsgType, &m.Plaintext, &m.FileName, &m.MIMEType, &m.FileSize, &m.TransportMode, &m.State, &m.Counter, &createdAt, &deliveredAt); err != nil {
			return nil, 0, err
		}
		m.CreatedAt = parseDBTime(createdAt)
		m.DeliveredAt = parseDBTime(deliveredAt)
		out = append(out, m)
	}
	return out, total, rows.Err()
}

func (s *Store) MarkMessageDelivered(msgID string, deliveredAt time.Time) error {
	ts := deliveredAt.UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`UPDATE messages SET state=?, delivered_at=? WHERE msg_id=?`, MessageStateDeliveredRemote, ts, msgID)
	return err
}

func (s *Store) UpdateMessageState(msgID, state string) error {
	if msgID == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE messages SET state=? WHERE msg_id=?`, state, msgID)
	return err
}

func (s *Store) UpsertOutboxJob(msgID, peerID, status string, retryCount int, nextRetryAt, lastTransportAttempt time.Time) error {
	if msgID == "" || peerID == "" {
		return nil
	}
	_, err := s.db.Exec(`
		INSERT INTO outbox_jobs(job_id,peer_id,msg_id,status,retry_count,next_retry_at,last_transport_attempt)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(job_id) DO UPDATE SET
			peer_id=excluded.peer_id,
			msg_id=excluded.msg_id,
			status=excluded.status,
			retry_count=excluded.retry_count,
			next_retry_at=excluded.next_retry_at,
			last_transport_attempt=excluded.last_transport_attempt
	`, msgID, peerID, msgID, status, retryCount, formatDBTime(nextRetryAt), formatDBTime(lastTransportAttempt))
	return err
}

func (s *Store) DeleteOutboxJob(msgID string) error {
	if msgID == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM outbox_jobs WHERE job_id=? OR msg_id=?`, msgID, msgID)
	return err
}

func (s *Store) QueueMessageRevoke(conversationID, peerID, msgID string) error {
	if conversationID == "" || peerID == "" || msgID == "" {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	now := time.Now().UTC()
	if _, err := tx.Exec(`
		INSERT INTO message_revoke_jobs(job_id,conversation_id,peer_id,msg_id,status,retry_count,next_retry_at,last_transport_attempt,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(job_id) DO UPDATE SET
			conversation_id=excluded.conversation_id,
			peer_id=excluded.peer_id,
			msg_id=excluded.msg_id,
			status=excluded.status,
			retry_count=excluded.retry_count,
			next_retry_at=excluded.next_retry_at,
			last_transport_attempt=excluded.last_transport_attempt,
			created_at=excluded.created_at
	`, msgID, conversationID, peerID, msgID, MessageStateQueuedForRetry, 0, formatDBTime(now), formatDBTime(time.Time{}), formatDBTime(now)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM outbox_jobs WHERE job_id=? OR msg_id=?`, msgID, msgID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM messages WHERE conversation_id=? AND msg_id=?`, conversationID, msgID); err != nil {
		return err
	}
	if err := refreshConversationMessageSummaryTx(tx, conversationID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteMessageRevokeJob(msgID string) error {
	if msgID == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM message_revoke_jobs WHERE job_id=? OR msg_id=?`, msgID, msgID)
	return err
}

func (s *Store) ListMessageRevokeJobsForPeer(peerID string, limit int) ([]messageRevokeRetryItem, error) {
	if limit <= 0 {
		limit = 32
	}
	rows, err := s.db.Query(`
		SELECT msg_id, conversation_id, peer_id, retry_count
		FROM message_revoke_jobs
		WHERE peer_id=? AND status IN (?, ?)
		ORDER BY next_retry_at ASC, last_transport_attempt ASC, created_at ASC
		LIMIT ?
	`, peerID, MessageStateQueuedForRetry, MessageStateSentToTransport, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []messageRevokeRetryItem
	for rows.Next() {
		var item messageRevokeRetryItem
		if err := rows.Scan(&item.MsgID, &item.ConversationID, &item.PeerID, &item.RetryCount); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListOutboxJobsForRetry(now time.Time, limit int) ([]outboxRetryItem, error) {
	if limit <= 0 {
		limit = 32
	}
	rows, err := s.db.Query(`
		SELECT
			o.job_id,
			o.peer_id,
			o.msg_id,
			m.conversation_id,
			m.sender_peer_id,
			m.receiver_peer_id,
			m.msg_type,
			m.file_name,
			m.mime_type,
			m.file_size,
			m.counter,
			m.ciphertext_blob,
			m.created_at,
			o.retry_count
		FROM outbox_jobs o
		INNER JOIN messages m ON m.msg_id = o.msg_id
		WHERE o.status IN (?, ?) AND o.next_retry_at != '' AND o.next_retry_at <= ?
		ORDER BY o.next_retry_at ASC, o.last_transport_attempt ASC
		LIMIT ?
	`, MessageStateQueuedForRetry, MessageStateSentToTransport, formatDBTime(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []outboxRetryItem
	for rows.Next() {
		var item outboxRetryItem
		var createdAt string
		if err := rows.Scan(
			&item.JobID,
			&item.PeerID,
			&item.MsgID,
			&item.ConversationID,
			&item.SenderPeerID,
			&item.ReceiverPeerID,
			&item.MsgType,
			&item.FileName,
			&item.MIMEType,
			&item.FileSize,
			&item.Counter,
			&item.CiphertextBlob,
			&createdAt,
			&item.RetryCount,
		); err != nil {
			return nil, err
		}
		item.SentAtUnix = parseDBTime(createdAt).UnixMilli()
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListOutboxJobsForPeer(peerID string, limit int) ([]outboxRetryItem, error) {
	if limit <= 0 {
		limit = 64
	}
	rows, err := s.db.Query(`
		SELECT
			o.job_id,
			o.peer_id,
			o.msg_id,
			m.conversation_id,
			m.sender_peer_id,
			m.receiver_peer_id,
			m.msg_type,
			m.file_name,
			m.mime_type,
			m.file_size,
			m.counter,
			m.ciphertext_blob,
			m.created_at,
			o.retry_count
		FROM outbox_jobs o
		INNER JOIN messages m ON m.msg_id = o.msg_id
		WHERE o.peer_id=? AND o.status IN (?, ?)
		ORDER BY m.created_at ASC
		LIMIT ?
	`, peerID, MessageStateQueuedForRetry, MessageStateSentToTransport, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []outboxRetryItem
	for rows.Next() {
		var item outboxRetryItem
		var createdAt string
		if err := rows.Scan(
			&item.JobID,
			&item.PeerID,
			&item.MsgID,
			&item.ConversationID,
			&item.SenderPeerID,
			&item.ReceiverPeerID,
			&item.MsgType,
			&item.FileName,
			&item.MIMEType,
			&item.FileSize,
			&item.Counter,
			&item.CiphertextBlob,
			&createdAt,
			&item.RetryCount,
		); err != nil {
			return nil, err
		}
		item.SentAtUnix = parseDBTime(createdAt).UnixMilli()
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) GetMessage(msgID string) (Message, error) {
	var m Message
	var createdAt, deliveredAt string
	err := s.db.QueryRow(`SELECT msg_id,conversation_id,sender_peer_id,receiver_peer_id,direction,msg_type,plaintext,file_name,mime_type,file_size,transport_mode,state,counter,created_at,delivered_at FROM messages WHERE msg_id=?`, msgID).
		Scan(&m.MsgID, &m.ConversationID, &m.SenderPeerID, &m.ReceiverPeerID, &m.Direction, &m.MsgType, &m.Plaintext, &m.FileName, &m.MIMEType, &m.FileSize, &m.TransportMode, &m.State, &m.Counter, &createdAt, &deliveredAt)
	if err != nil {
		return Message{}, err
	}
	m.CreatedAt = parseDBTime(createdAt)
	m.DeliveredAt = parseDBTime(deliveredAt)
	return m, nil
}

func (s *Store) GetMessageBlob(msgID string) ([]byte, error) {
	var blob []byte
	if err := s.db.QueryRow(`SELECT ciphertext_blob FROM messages WHERE msg_id=?`, msgID).Scan(&blob); err != nil {
		return nil, err
	}
	return blob, nil
}

func (s *Store) ListOutgoingMessagesForSync(conversationID, senderPeerID string, nextCounter uint64, limit int) ([]chatSyncItem, error) {
	if limit <= 0 {
		limit = 128
	}
	rows, err := s.db.Query(`
		SELECT
			msg_id,
			conversation_id,
			sender_peer_id,
			receiver_peer_id,
			msg_type,
			file_name,
			mime_type,
			file_size,
			counter,
			ciphertext_blob,
			created_at
		FROM messages
		WHERE conversation_id=? AND sender_peer_id=? AND direction='outbound' AND counter >= ?
		ORDER BY counter ASC
		LIMIT ?
	`, conversationID, senderPeerID, nextCounter, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []chatSyncItem
	for rows.Next() {
		var item chatSyncItem
		var createdAt string
		if err := rows.Scan(
			&item.MsgID,
			&item.ConversationID,
			&item.SenderPeerID,
			&item.ReceiverPeerID,
			&item.MsgType,
			&item.FileName,
			&item.MIMEType,
			&item.FileSize,
			&item.Counter,
			&item.CiphertextBlob,
			&createdAt,
		); err != nil {
			return nil, err
		}
		item.SentAtUnix = parseDBTime(createdAt).UnixMilli()
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListMessagesMissingOutbox(limit int) ([]outboxRecoveryItem, error) {
	if limit <= 0 {
		limit = 256
	}
	rows, err := s.db.Query(`
		SELECT
			m.msg_id,
			m.conversation_id,
			m.receiver_peer_id,
			m.state,
			m.created_at
		FROM messages m
		LEFT JOIN outbox_jobs o ON o.msg_id = m.msg_id
		WHERE m.direction='outbound'
			AND m.transport_mode=?
			AND m.state IN (?, ?, ?)
			AND m.msg_type IN (?, ?, ?)
			AND o.msg_id IS NULL
		ORDER BY m.created_at ASC
		LIMIT ?
	`, TransportModeDirect, MessageStateLocalOnly, MessageStateQueuedForRetry, MessageStateSentToTransport, MessageTypeChatText, MessageTypeChatFile, MessageTypeGroupInviteNote, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []outboxRecoveryItem
	for rows.Next() {
		var item outboxRecoveryItem
		var createdAt string
		if err := rows.Scan(&item.MsgID, &item.ConversationID, &item.ReceiverPeerID, &item.State, &createdAt); err != nil {
			return nil, err
		}
		item.CreatedAt = parseDBTime(createdAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) DeleteMessage(conversationID, msgID string) error {
	if conversationID == "" || msgID == "" {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM messages WHERE conversation_id=? AND msg_id=?`, conversationID, msgID); err != nil {
		return err
	}
	return s.refreshConversationMessageSummary(conversationID)
}
