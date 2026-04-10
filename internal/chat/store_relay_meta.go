package chat

import (
	"database/sql"
	"errors"
)

// SetConversationUpstreamMeta 写入上游会话 UUID 与已同步 seq。
func (s *Store) SetConversationUpstreamMeta(localConversationID, upstreamConversationID string, lastSeq uint64) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`UPDATE conversations SET upstream_conversation_id=?, last_upstream_sync_seq=? WHERE conversation_id=?`,
		upstreamConversationID, lastSeq, localConversationID)
	return err
}

// GetConversationByUpstreamID 按上游 conversation_id 查本地会话 ID。
func (s *Store) GetConversationByUpstreamID(upstreamID string) (localConversationID string, err error) {
	err = s.db.QueryRow(`SELECT conversation_id FROM conversations WHERE upstream_conversation_id=?`, upstreamID).Scan(&localConversationID)
	return
}

// FindMessageByUpstreamID 若已存在则返回本地 msg_id。
func (s *Store) FindMessageByUpstreamID(upstreamMsgID string) (msgID string, err error) {
	err = s.db.QueryRow(`SELECT msg_id FROM messages WHERE upstream_message_id=? LIMIT 1`, upstreamMsgID).Scan(&msgID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return msgID, err
}
