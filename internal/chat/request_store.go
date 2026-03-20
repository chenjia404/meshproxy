package chat

import (
	"database/sql"
	"strings"
	"time"
)

func (s *Store) ensureRequestBioColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(requests)`)
	if err != nil {
		return err
	}
	hasBio := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "bio" {
			hasBio = true
			break
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasBio {
		return nil
	}
	_, err = s.db.Exec(`ALTER TABLE requests ADD COLUMN bio TEXT NOT NULL DEFAULT ''`)
	return err
}

func (s *Store) ensureRequestAvatarColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(requests)`)
	if err != nil {
		return err
	}
	hasAvatar := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "avatar" {
			hasAvatar = true
			break
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasAvatar {
		return nil
	}
	_, err = s.db.Exec(`ALTER TABLE requests ADD COLUMN avatar TEXT NOT NULL DEFAULT ''`)
	return err
}

func (s *Store) ensureRequestRetentionColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(requests)`)
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
	_, err = s.db.Exec(`ALTER TABLE requests ADD COLUMN retention_minutes INTEGER NOT NULL DEFAULT 0`)
	return err
}

func (s *Store) UpsertIncomingRequest(req Request) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		INSERT INTO requests(request_id,from_peer_id,to_peer_id,state,intro_text,nickname,bio,avatar,retention_minutes,remote_chat_kex_pub,conversation_id,last_transport_mode,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(request_id) DO UPDATE SET
			state=excluded.state,
			intro_text=excluded.intro_text,
			nickname=excluded.nickname,
			bio=excluded.bio,
			avatar=excluded.avatar,
			retention_minutes=excluded.retention_minutes,
			remote_chat_kex_pub=excluded.remote_chat_kex_pub,
			updated_at=excluded.updated_at
	`, req.RequestID, req.FromPeerID, req.ToPeerID, req.State, req.IntroText, req.Nickname, req.Bio, req.Avatar, req.RetentionMinutes, req.RemoteChatKexPub, req.ConversationID, req.LastTransportMode, now, now)
	return err
}

func (s *Store) SaveOutgoingRequest(req Request) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO requests(request_id,from_peer_id,to_peer_id,state,intro_text,nickname,bio,avatar,retention_minutes,remote_chat_kex_pub,conversation_id,last_transport_mode,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, req.RequestID, req.FromPeerID, req.ToPeerID, req.State, req.IntroText, req.Nickname, req.Bio, req.Avatar, req.RetentionMinutes, req.RemoteChatKexPub, req.ConversationID, req.LastTransportMode, now, now)
	return err
}

func (s *Store) ListRequests(localPeerID string) ([]Request, error) {
	rows, err := s.db.Query(`
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
		WHERE r.from_peer_id=? OR r.to_peer_id=?
		ORDER BY r.created_at DESC
	`, localPeerID, localPeerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Request
	for rows.Next() {
		var r Request
		var createdAt, updatedAt string
		var nextRetryAt string
		if err := rows.Scan(
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
		); err != nil {
			return nil, err
		}
		r.CreatedAt = parseDBTime(createdAt)
		r.UpdatedAt = parseDBTime(updatedAt)
		r.NextRetryAt = parseDBTime(nextRetryAt)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetRequest(requestID string) (Request, error) {
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
		WHERE r.request_id=?
	`, requestID).Scan(
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

func (s *Store) UpdateRequestAvatar(requestID, avatar string) error {
	_, err := s.db.Exec(`UPDATE requests SET avatar=?, updated_at=? WHERE request_id=?`,
		strings.TrimSpace(avatar), time.Now().UTC().Format(time.RFC3339Nano), requestID)
	return err
}

func (s *Store) UpdateRequestRemoteChatKexPub(requestID, remoteChatKexPub string) error {
	_, err := s.db.Exec(`UPDATE requests SET remote_chat_kex_pub=?, updated_at=? WHERE request_id=?`,
		strings.TrimSpace(remoteChatKexPub), time.Now().UTC().Format(time.RFC3339Nano), requestID)
	return err
}

func (s *Store) UpdateRequestsAvatar(peerID, avatar string) error {
	_, err := s.db.Exec(`UPDATE requests SET avatar=?, updated_at=? WHERE from_peer_id=? OR to_peer_id=?`,
		strings.TrimSpace(avatar), time.Now().UTC().Format(time.RFC3339Nano), peerID, peerID)
	return err
}

func (s *Store) UpdateRequestState(requestID, state, conversationID string) error {
	_, err := s.db.Exec(`UPDATE requests SET state=?, conversation_id=?, updated_at=? WHERE request_id=?`,
		state, conversationID, time.Now().UTC().Format(time.RFC3339Nano), requestID)
	return err
}

func (s *Store) UpsertFriendRequestJob(requestID, peerID, status string, retryCount int, nextRetryAt, lastTransportAttempt time.Time) error {
	if requestID == "" || peerID == "" {
		return nil
	}
	now := time.Now().UTC()
	_, err := s.db.Exec(`
		INSERT INTO friend_request_jobs(job_id,peer_id,status,retry_count,next_retry_at,last_transport_attempt,created_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(job_id) DO UPDATE SET
			peer_id=excluded.peer_id,
			status=excluded.status,
			retry_count=excluded.retry_count,
			next_retry_at=excluded.next_retry_at,
			last_transport_attempt=excluded.last_transport_attempt
	`, requestID, peerID, status, retryCount, formatDBTime(nextRetryAt), formatDBTime(lastTransportAttempt), formatDBTime(now))
	return err
}

func (s *Store) DeleteFriendRequestJob(requestID string) error {
	if requestID == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM friend_request_jobs WHERE job_id=?`, requestID)
	return err
}

func (s *Store) ListFriendRequestJobsForRetry(now time.Time, limit int) ([]friendRequestRetryItem, error) {
	if limit <= 0 {
		limit = 32
	}
	rows, err := s.db.Query(`
		SELECT job_id, peer_id, retry_count
		FROM friend_request_jobs
		WHERE status=? AND next_retry_at != '' AND next_retry_at <= ?
		ORDER BY next_retry_at ASC, last_transport_attempt ASC
		LIMIT ?
	`, MessageStateQueuedForRetry, formatDBTime(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []friendRequestRetryItem
	for rows.Next() {
		var item friendRequestRetryItem
		if err := rows.Scan(&item.RequestID, &item.PeerID, &item.RetryCount); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
