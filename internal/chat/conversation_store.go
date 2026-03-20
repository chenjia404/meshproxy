package chat

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"time"
)

func (s *Store) CreateConversation(conv Conversation, sess sessionState) (Conversation, error) {
	if existing, err := s.GetConversation(conv.ConversationID); err == nil {
		if conv.LastMessageAt.IsZero() {
			conv.LastMessageAt = existing.LastMessageAt
		}
		if conv.UnreadCount == 0 && existing.UnreadCount > 0 {
			conv.UnreadCount = existing.UnreadCount
		}
		if conv.RetentionMinutes == 0 && existing.RetentionMinutes > 0 {
			conv.RetentionMinutes = existing.RetentionMinutes
		}
		if conv.RetentionSyncState == "" {
			conv.RetentionSyncState = existing.RetentionSyncState
		}
		if conv.RetentionSyncedAt.IsZero() && !existing.RetentionSyncedAt.IsZero() {
			conv.RetentionSyncedAt = existing.RetentionSyncedAt
		}
		if conv.CreatedAt.IsZero() && !existing.CreatedAt.IsZero() {
			conv.CreatedAt = existing.CreatedAt
		}
	} else if err != sql.ErrNoRows {
		return Conversation{}, err
	} else if existingByPeer, err := s.GetConversationByPeer(conv.PeerID); err == nil {
		if conv.RetentionMinutes == 0 && existingByPeer.RetentionMinutes > 0 {
			conv.RetentionMinutes = existingByPeer.RetentionMinutes
		}
		if conv.RetentionSyncState == "" {
			conv.RetentionSyncState = existingByPeer.RetentionSyncState
		}
		if conv.RetentionSyncedAt.IsZero() && !existingByPeer.RetentionSyncedAt.IsZero() {
			conv.RetentionSyncedAt = existingByPeer.RetentionSyncedAt
		}
	} else if err != sql.ErrNoRows {
		return Conversation{}, err
	}
	nowTime := time.Now().UTC()
	now := nowTime.Format(time.RFC3339Nano)
	lastMessageAt := now
	if !conv.LastMessageAt.IsZero() {
		lastMessageAt = conv.LastMessageAt.UTC().Format(time.RFC3339Nano)
	}
	createdAt := now
	if !conv.CreatedAt.IsZero() {
		createdAt = conv.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO conversations(conversation_id,peer_id,state,last_message_at,last_transport_mode,unread_count,retention_minutes,retention_sync_state,retention_synced_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
	`, conv.ConversationID, conv.PeerID, conv.State, lastMessageAt, conv.LastTransportMode, conv.UnreadCount, conv.RetentionMinutes, firstNonEmpty(conv.RetentionSyncState, "synced"), formatDBTime(conv.RetentionSyncedAt), createdAt, now)
	if err != nil {
		return Conversation{}, err
	}
	_, err = s.db.Exec(`
		INSERT OR REPLACE INTO session_states(conversation_id,peer_id,send_key,recv_key,send_counter,recv_counter)
		VALUES(?,?,?,?,?,?)
	`, sess.ConversationID, sess.PeerID, base64.StdEncoding.EncodeToString(sess.SendKey), base64.StdEncoding.EncodeToString(sess.RecvKey), sess.SendCounter, sess.RecvCounter)
	if err != nil {
		return Conversation{}, err
	}
	return s.GetConversation(conv.ConversationID)
}

func (s *Store) GetConversation(id string) (Conversation, error) {
	var c Conversation
	var lastMessageAt, createdAt, updatedAt string
	var retentionSyncedAt string
	err := s.db.QueryRow(`SELECT conversation_id,peer_id,state,last_message_at,last_transport_mode,unread_count,retention_minutes,retention_sync_state,retention_synced_at,created_at,updated_at FROM conversations WHERE conversation_id=?`, id).
		Scan(&c.ConversationID, &c.PeerID, &c.State, &lastMessageAt, &c.LastTransportMode, &c.UnreadCount, &c.RetentionMinutes, &c.RetentionSyncState, &retentionSyncedAt, &createdAt, &updatedAt)
	if err != nil {
		return Conversation{}, err
	}
	c.LastMessageAt = parseDBTime(lastMessageAt)
	c.RetentionSyncedAt = parseDBTime(retentionSyncedAt)
	c.CreatedAt = parseDBTime(createdAt)
	c.UpdatedAt = parseDBTime(updatedAt)
	return c, nil
}

func (s *Store) GetConversationByPeer(peerID string) (Conversation, error) {
	var c Conversation
	var lastMessageAt, createdAt, updatedAt string
	var retentionSyncedAt string
	err := s.db.QueryRow(`SELECT conversation_id,peer_id,state,last_message_at,last_transport_mode,unread_count,retention_minutes,retention_sync_state,retention_synced_at,created_at,updated_at FROM conversations WHERE peer_id=?`, peerID).
		Scan(&c.ConversationID, &c.PeerID, &c.State, &lastMessageAt, &c.LastTransportMode, &c.UnreadCount, &c.RetentionMinutes, &c.RetentionSyncState, &retentionSyncedAt, &createdAt, &updatedAt)
	if err != nil {
		return Conversation{}, err
	}
	c.LastMessageAt = parseDBTime(lastMessageAt)
	c.RetentionSyncedAt = parseDBTime(retentionSyncedAt)
	c.CreatedAt = parseDBTime(createdAt)
	c.UpdatedAt = parseDBTime(updatedAt)
	return c, nil
}

func (s *Store) ListConversations() ([]Conversation, error) {
	rows, err := s.db.Query(`SELECT conversation_id,peer_id,state,last_message_at,last_transport_mode,unread_count,retention_minutes,retention_sync_state,retention_synced_at,created_at,updated_at FROM conversations ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		var lastMessageAt, retentionSyncedAt, createdAt, updatedAt string
		if err := rows.Scan(&c.ConversationID, &c.PeerID, &c.State, &lastMessageAt, &c.LastTransportMode, &c.UnreadCount, &c.RetentionMinutes, &c.RetentionSyncState, &retentionSyncedAt, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		c.LastMessageAt = parseDBTime(lastMessageAt)
		c.RetentionSyncedAt = parseDBTime(retentionSyncedAt)
		c.CreatedAt = parseDBTime(createdAt)
		c.UpdatedAt = parseDBTime(updatedAt)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateConversationRetention(conversationID string, minutes int) (Conversation, error) {
	if minutes != 0 && (minutes < MinRetentionMinutes || minutes > MaxRetentionMinutes) {
		return Conversation{}, fmt.Errorf("retention_minutes must be 0 or between %d and %d", MinRetentionMinutes, MaxRetentionMinutes)
	}
	_, err := s.db.Exec(`UPDATE conversations SET retention_minutes=?, updated_at=? WHERE conversation_id=?`,
		minutes, time.Now().UTC().Format(time.RFC3339Nano), conversationID)
	if err != nil {
		return Conversation{}, err
	}
	if minutes > 0 {
		if err := s.cleanupConversationExpiredMessages(conversationID, minutes, time.Now().UTC()); err != nil {
			return Conversation{}, err
		}
	}
	return s.GetConversation(conversationID)
}

// UpdateConversationState flips conversation state (e.g. no_friend).
// It does not delete messages; it only updates the conversations.state column.
func (s *Store) UpdateConversationState(conversationID string, state string) error {
	if conversationID == "" {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE conversations SET state=?, updated_at=? WHERE conversation_id=?`,
		state,
		time.Now().UTC().Format(time.RFC3339Nano),
		conversationID,
	)
	return err
}

func (s *Store) UpdateConversationRetentionSync(conversationID, syncState string, syncedAt time.Time) error {
	if syncState == "" {
		syncState = "synced"
	}
	_, err := s.db.Exec(`UPDATE conversations SET retention_sync_state=?, retention_synced_at=?, updated_at=? WHERE conversation_id=?`,
		syncState, formatDBTime(syncedAt), time.Now().UTC().Format(time.RFC3339Nano), conversationID)
	return err
}

func (s *Store) CleanupExpiredMessages(now time.Time) error {
	rows, err := s.db.Query(`SELECT conversation_id, retention_minutes FROM conversations WHERE retention_minutes > 0`)
	if err != nil {
		return err
	}
	type retentionJob struct {
		conversationID string
		minutes        int
	}
	var jobs []retentionJob
	for rows.Next() {
		var conversationID string
		var minutes int
		if err := rows.Scan(&conversationID, &minutes); err != nil {
			_ = rows.Close()
			return err
		}
		jobs = append(jobs, retentionJob{conversationID: conversationID, minutes: minutes})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, job := range jobs {
		if err := s.cleanupConversationExpiredMessages(job.conversationID, job.minutes, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) cleanupConversationExpiredMessages(conversationID string, minutes int, now time.Time) error {
	cutoff := now.Add(-time.Duration(minutes) * time.Minute).UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`DELETE FROM messages WHERE conversation_id=? AND created_at <= ?`, conversationID, cutoff); err != nil {
		return err
	}
	return s.refreshConversationMessageSummary(conversationID)
}

func (s *Store) refreshConversationMessageSummary(conversationID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	if err := refreshConversationMessageSummaryTx(tx, conversationID, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func refreshConversationMessageSummaryTx(tx *sql.Tx, conversationID string, updatedAt time.Time) error {
	var lastMessageAt sql.NullString
	if err := tx.QueryRow(`SELECT COALESCE(MAX(created_at), '') FROM messages WHERE conversation_id=?`, conversationID).Scan(&lastMessageAt); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE conversations SET last_message_at=?, updated_at=? WHERE conversation_id=?`,
		lastMessageAt.String, formatDBTime(updatedAt), conversationID)
	return err
}

func (s *Store) GetSessionState(conversationID string) (sessionState, error) {
	var sess sessionState
	var sendKeyB64, recvKeyB64 string
	err := s.db.QueryRow(`SELECT conversation_id,peer_id,send_key,recv_key,send_counter,recv_counter FROM session_states WHERE conversation_id=?`, conversationID).
		Scan(&sess.ConversationID, &sess.PeerID, &sendKeyB64, &recvKeyB64, &sess.SendCounter, &sess.RecvCounter)
	if err != nil {
		return sessionState{}, err
	}
	sess.SendKey, err = base64.StdEncoding.DecodeString(sendKeyB64)
	if err != nil {
		return sessionState{}, err
	}
	sess.RecvKey, err = base64.StdEncoding.DecodeString(recvKeyB64)
	if err != nil {
		return sessionState{}, err
	}
	return sess, nil
}

func (s *Store) UpdateSendCounter(conversationID string, counter uint64) error {
	_, err := s.db.Exec(`UPDATE session_states SET send_counter=? WHERE conversation_id=?`, counter, conversationID)
	return err
}

func (s *Store) UpdateRecvCounter(conversationID string, counter uint64) error {
	_, err := s.db.Exec(`UPDATE session_states SET recv_counter=? WHERE conversation_id=?`, counter, conversationID)
	return err
}
