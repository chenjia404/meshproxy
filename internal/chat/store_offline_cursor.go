package chat

import (
	"database/sql"
	"fmt"
)

func (s *Store) ensureOfflineStoreCursorTable() error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS offline_store_cursor (
			store_peer_id TEXT PRIMARY KEY,
			last_ack_seq INTEGER NOT NULL DEFAULT 0
		);
	`)
	if err != nil {
		return fmt.Errorf("offline_store_cursor: %w", err)
	}
	return nil
}

// GetOfflineStoreLastAckSeq 返回針對某 store 節點已 ACK 的最大序號（作為下次 fetch 的 after_seq）。
func (s *Store) GetOfflineStoreLastAckSeq(storePeerID string) (int64, error) {
	if s == nil || s.db == nil || storePeerID == "" {
		return 0, nil
	}
	var v sql.NullInt64
	err := s.db.QueryRow(`SELECT last_ack_seq FROM offline_store_cursor WHERE store_peer_id=?`, storePeerID).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return v.Int64, nil
}

// SetOfflineStoreLastAckSeq 持久化某 store 的 ACK 游標。
func (s *Store) SetOfflineStoreLastAckSeq(storePeerID string, lastAckSeq int64) error {
	if s == nil || s.db == nil || storePeerID == "" {
		return nil
	}
	_, err := s.db.Exec(`
		INSERT INTO offline_store_cursor(store_peer_id, last_ack_seq) VALUES(?,?)
		ON CONFLICT(store_peer_id) DO UPDATE SET last_ack_seq=excluded.last_ack_seq
	`, storePeerID, lastAckSeq)
	return err
}
