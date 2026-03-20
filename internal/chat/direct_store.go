package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// HasAcceptedRequest reports whether there exists at least one accepted
// friend request from `fromPeerID` to `toPeerID`.
func (s *Store) HasAcceptedRequest(fromPeerID, toPeerID string) (bool, error) {
	var existsInt int
	err := s.db.QueryRow(
		`SELECT EXISTS(
			SELECT 1 FROM requests
			WHERE from_peer_id=? AND to_peer_id=? AND state=?
			LIMIT 1
		)`,
		fromPeerID,
		toPeerID,
		RequestStateAccepted,
	).Scan(&existsInt)
	return existsInt != 0, err
}

func (s *Store) LatestRequestBetweenPeers(peerA, peerB string) (Request, error) {
	var r Request
	var createdAt, updatedAt string
	var nextRetryAt string
	err := s.db.QueryRow(`
		SELECT
			r.request_id,
			r.from_peer_id,
			r.to_peer_id,
			r.state,
			r.intro_text,
			r.nickname,
			r.bio,
			r.avatar,
			r.retention_minutes,
			r.remote_chat_kex_pub,
			r.conversation_id,
			r.last_transport_mode,
			COALESCE(fj.retry_count, 0) AS retry_count,
			COALESCE(fj.next_retry_at, '') AS next_retry_at,
			COALESCE(fj.status, '') AS retry_job_status,
			r.created_at,
			r.updated_at
		FROM requests r
		LEFT JOIN friend_request_jobs fj ON fj.job_id = r.request_id
		WHERE (r.from_peer_id=? AND r.to_peer_id=?) OR (r.from_peer_id=? AND r.to_peer_id=?)
		ORDER BY r.updated_at DESC, r.created_at DESC
		LIMIT 1
	`, peerA, peerB, peerB, peerA).Scan(
		&r.RequestID,
		&r.FromPeerID,
		&r.ToPeerID,
		&r.State,
		&r.IntroText,
		&r.Nickname,
		&r.Bio,
		&r.Avatar,
		&r.RetentionMinutes,
		&r.RemoteChatKexPub,
		&r.ConversationID,
		&r.LastTransportMode,
		&r.RetryCount,
		&nextRetryAt,
		&r.RetryJobStatus,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return Request{}, err
	}
	r.CreatedAt = parseDBTime(createdAt)
	r.UpdatedAt = parseDBTime(updatedAt)
	r.NextRetryAt = parseDBTime(nextRetryAt)
	return r, nil
}

func (s *Store) MigrateConversationID(oldConversationID, newConversationID string) error {
	oldConversationID = strings.TrimSpace(oldConversationID)
	newConversationID = strings.TrimSpace(newConversationID)
	if oldConversationID == "" || newConversationID == "" || oldConversationID == newConversationID {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	var peerID, state, lastMessageAt, lastTransportMode string
	var unreadCount, retentionMinutes int
	var retentionSyncState, retentionSyncedAt, createdAt string
	if err := tx.QueryRow(`
		SELECT peer_id,state,last_message_at,last_transport_mode,unread_count,retention_minutes,retention_sync_state,retention_synced_at,created_at
		FROM conversations
		WHERE conversation_id=?
	`, oldConversationID).Scan(
		&peerID,
		&state,
		&lastMessageAt,
		&lastTransportMode,
		&unreadCount,
		&retentionMinutes,
		&retentionSyncState,
		&retentionSyncedAt,
		&createdAt,
	); err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
		INSERT OR REPLACE INTO conversations(conversation_id,peer_id,state,last_message_at,last_transport_mode,unread_count,retention_minutes,retention_sync_state,retention_synced_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
	`, newConversationID, peerID, state, lastMessageAt, lastTransportMode, unreadCount, retentionMinutes, retentionSyncState, retentionSyncedAt, createdAt, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT OR REPLACE INTO session_states(conversation_id,peer_id,send_key,recv_key,send_counter,recv_counter)
		SELECT ?, peer_id, send_key, recv_key, send_counter, recv_counter
		FROM session_states
		WHERE conversation_id=?
	`, newConversationID, oldConversationID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE messages SET conversation_id=? WHERE conversation_id=?`, newConversationID, oldConversationID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE message_revoke_jobs SET conversation_id=? WHERE conversation_id=?`, newConversationID, oldConversationID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE requests SET conversation_id=?, updated_at=? WHERE conversation_id=?`, newConversationID, now, oldConversationID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM session_states WHERE conversation_id=?`, oldConversationID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM conversations WHERE conversation_id=?`, oldConversationID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func deriveStableConversationID(a, b string) string {
	parts := []string{a, b}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte("stable_v1:" + parts[0] + "\x00" + parts[1]))
	return "stable_v1:" + hex.EncodeToString(sum[:])
}
