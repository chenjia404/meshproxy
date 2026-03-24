package chat

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"

	"github.com/chenjia404/meshproxy/internal/protocol"
)

type sessionState struct {
	ConversationID string
	PeerID         string
	SendKey        []byte
	RecvKey        []byte
	SendCounter    uint64
	RecvCounter    uint64
}

type outboxRetryItem struct {
	JobID          string
	PeerID         string
	MsgID          string
	ConversationID string
	SenderPeerID   string
	ReceiverPeerID string
	MsgType        string
	FileName       string
	MIMEType       string
	FileSize       int64
	FileCID        string
	Counter        uint64
	CiphertextBlob []byte
	SentAtUnix     int64
	RetryCount     int
}

type chatSyncItem struct {
	MsgID          string
	ConversationID string
	SenderPeerID   string
	ReceiverPeerID string
	MsgType        string
	FileName       string
	MIMEType       string
	FileSize       int64
	FileCID        string
	Counter        uint64
	CiphertextBlob []byte
	SentAtUnix     int64
}

type outboxRecoveryItem struct {
	MsgID          string
	ConversationID string
	ReceiverPeerID string
	State          string
	CreatedAt      time.Time
}

type friendRequestRetryItem struct {
	RequestID  string
	PeerID     string
	RetryCount int
}

type messageRevokeRetryItem struct {
	MsgID          string
	ConversationID string
	PeerID         string
	RetryCount     int
}

type Store struct {
	db          *sql.DB
	localPeerID string
}

const (
	MinRetentionMinutes = 1
	MaxRetentionMinutes = 60 * 24 * 365
	MaxChatFileBytes    = 64 << 20
)

func NewStore(path, localPeerID string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create chat db dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite works much more reliably in this app when all chat writes are
	// serialized through one shared connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 15000`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set sqlite busy_timeout: %w", err)
	}
	s := &Store{db: db, localPeerID: localPeerID}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureProfileBioColumn(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureProfileAvatarColumn(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensurePeerBioColumn(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensurePeerAvatarColumn(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureProfileAvatarCIDColumn(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensurePeerAvatarCIDColumn(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureProfileBindingColumns(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensurePeerBindingColumns(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureRequestBioColumn(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureRequestAvatarColumn(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureRequestRetentionColumn(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.upgradeLegacyAutoNicknames(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureProfile(localPeerID); err != nil {
		_ = db.Close()
		return nil, err
	}
	now := time.Now().UTC()
	if err := s.CleanupExpiredMessages(now); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("startup cleanup direct retention: %w", err)
	}
	if err := s.CleanupExpiredGroupMessages(now); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("startup cleanup group retention: %w", err)
	}
	if err := s.CleanupArchivedGroups(now); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("startup cleanup archived groups: %w", err)
	}
	if err := s.DedupeConversationsByPeer(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("dedupe conversations by peer: %w", err)
	}
	if err := s.DedupePeersByPeerID(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("dedupe peers by peer_id: %w", err)
	}
	if err := s.backfillConversationLastMessagePreviews(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("backfill conversation last_message: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS profile (
			peer_id TEXT PRIMARY KEY,
			nickname TEXT NOT NULL,
			bio TEXT NOT NULL DEFAULT '',
			avatar TEXT NOT NULL DEFAULT '',
			chat_kex_priv TEXT NOT NULL,
			chat_kex_pub TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS peers (
			peer_id TEXT PRIMARY KEY,
			nickname TEXT NOT NULL,
			bio TEXT NOT NULL DEFAULT '',
			avatar TEXT NOT NULL DEFAULT '',
			blocked INTEGER NOT NULL DEFAULT 0,
			last_seen_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS requests (
			request_id TEXT PRIMARY KEY,
			from_peer_id TEXT NOT NULL,
			to_peer_id TEXT NOT NULL,
			state TEXT NOT NULL,
			intro_text TEXT NOT NULL,
			nickname TEXT NOT NULL,
			bio TEXT NOT NULL DEFAULT '',
			avatar TEXT NOT NULL DEFAULT '',
			retention_minutes INTEGER NOT NULL DEFAULT 0,
			remote_chat_kex_pub TEXT NOT NULL,
			conversation_id TEXT NOT NULL DEFAULT '',
			last_transport_mode TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS conversations (
			conversation_id TEXT PRIMARY KEY,
			peer_id TEXT NOT NULL UNIQUE,
			state TEXT NOT NULL,
			last_message_at TEXT NOT NULL,
			last_message TEXT NOT NULL DEFAULT '',
			last_transport_mode TEXT NOT NULL,
			unread_count INTEGER NOT NULL,
			retention_minutes INTEGER NOT NULL DEFAULT 0,
			retention_sync_state TEXT NOT NULL DEFAULT 'synced',
			retention_synced_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS messages (
			msg_id TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL,
			sender_peer_id TEXT NOT NULL,
			receiver_peer_id TEXT NOT NULL,
			direction TEXT NOT NULL,
			msg_type TEXT NOT NULL,
			plaintext TEXT NOT NULL,
			file_name TEXT NOT NULL DEFAULT '',
			mime_type TEXT NOT NULL DEFAULT '',
			file_size INTEGER NOT NULL DEFAULT 0,
			file_cid TEXT NOT NULL DEFAULT '',
			ciphertext_blob BLOB NOT NULL,
			transport_mode TEXT NOT NULL,
			state TEXT NOT NULL,
			counter INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			delivered_at TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS session_states (
			conversation_id TEXT PRIMARY KEY,
			peer_id TEXT NOT NULL,
			send_key TEXT NOT NULL,
			recv_key TEXT NOT NULL,
			send_counter INTEGER NOT NULL,
			recv_counter INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS outbox_jobs (
			job_id TEXT PRIMARY KEY,
			peer_id TEXT NOT NULL,
			msg_id TEXT NOT NULL,
			status TEXT NOT NULL,
			retry_count INTEGER NOT NULL,
			next_retry_at TEXT NOT NULL,
			last_transport_attempt TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS message_revoke_jobs (
			job_id TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL,
			peer_id TEXT NOT NULL,
			msg_id TEXT NOT NULL,
			status TEXT NOT NULL,
			retry_count INTEGER NOT NULL,
			next_retry_at TEXT NOT NULL,
			last_transport_attempt TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS friend_request_jobs (
			job_id TEXT PRIMARY KEY,
			peer_id TEXT NOT NULL,
			status TEXT NOT NULL,
			retry_count INTEGER NOT NULL,
			next_retry_at TEXT NOT NULL,
			last_transport_attempt TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate chat db: %w", err)
		}
	}
	if err := s.ensureConversationRetentionColumn(); err != nil {
		return err
	}
	if err := s.ensureConversationRetentionSyncColumns(); err != nil {
		return err
	}
	if err := s.ensureConversationLastMessageColumn(); err != nil {
		return err
	}
	if err := s.ensureMessageFileColumns(); err != nil {
		return err
	}
	if err := s.ensureGroupTables(); err != nil {
		return err
	}
	if err := s.ensureMessageIndexes(); err != nil {
		return err
	}
	if err := s.ensureOfflineStoreCursorTable(); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureMessageIndexes() error {
	// Speed up per-conversation listing and counter-based sync queries (no index
	// on conversation_id caused full table scans as the messages table grew).
	stmts := []string{
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation_created_at ON messages(conversation_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation_counter ON messages(conversation_id, counter)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("create messages index: %w", err)
		}
	}
	return nil
}

func (s *Store) ensureConversationRetentionColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(conversations)`)
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
	_, err = s.db.Exec(`ALTER TABLE conversations ADD COLUMN retention_minutes INTEGER NOT NULL DEFAULT 0`)
	return err
}

func (s *Store) ensureConversationRetentionSyncColumns() error {
	rows, err := s.db.Query(`PRAGMA table_info(conversations)`)
	if err != nil {
		return err
	}
	hasState := false
	hasAt := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "retention_sync_state" {
			hasState = true
		}
		if name == "retention_synced_at" {
			hasAt = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !hasState {
		if _, err := s.db.Exec(`ALTER TABLE conversations ADD COLUMN retention_sync_state TEXT NOT NULL DEFAULT 'synced'`); err != nil {
			return err
		}
	}
	if !hasAt {
		if _, err := s.db.Exec(`ALTER TABLE conversations ADD COLUMN retention_synced_at TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureConversationLastMessageColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(conversations)`)
	if err != nil {
		return err
	}
	has := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "last_message" {
			has = true
			break
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = s.db.Exec(`ALTER TABLE conversations ADD COLUMN last_message TEXT NOT NULL DEFAULT ''`)
	return err
}

// backfillConversationLastMessagePreviews 僅填寫 last_message 預覽字串，不修改 updated_at。
func (s *Store) backfillConversationLastMessagePreviews() error {
	if s == nil || s.db == nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT conversation_id FROM conversations`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		var cur string
		if err := s.db.QueryRow(`SELECT last_message FROM conversations WHERE conversation_id=?`, id).Scan(&cur); err != nil {
			return err
		}
		if cur != "" {
			continue
		}
		var msgType, plaintext, fileName string
		err := s.db.QueryRow(`
			SELECT msg_type, plaintext, file_name FROM messages
			WHERE conversation_id=?
			ORDER BY created_at DESC, counter DESC
			LIMIT 1
		`, id).Scan(&msgType, &plaintext, &fileName)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		preview := messagePreviewFromMessage(Message{MsgType: msgType, Plaintext: plaintext, FileName: fileName})
		if _, err := s.db.Exec(`UPDATE conversations SET last_message=? WHERE conversation_id=?`, preview, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureMessageFileColumns() error {
	rows, err := s.db.Query(`PRAGMA table_info(messages)`)
	if err != nil {
		return err
	}
	hasFileName := false
	hasMime := false
	hasFileSize := false
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
		switch name {
		case "file_name":
			hasFileName = true
		case "mime_type":
			hasMime = true
		case "file_size":
			hasFileSize = true
		case "file_cid":
			hasFileCID = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !hasFileName {
		if _, err := s.db.Exec(`ALTER TABLE messages ADD COLUMN file_name TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !hasMime {
		if _, err := s.db.Exec(`ALTER TABLE messages ADD COLUMN mime_type TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !hasFileSize {
		if _, err := s.db.Exec(`ALTER TABLE messages ADD COLUMN file_size INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if !hasFileCID {
		if _, err := s.db.Exec(`ALTER TABLE messages ADD COLUMN file_cid TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureProfile(localPeerID string) error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM profile WHERE peer_id = ?`, localPeerID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	priv, pub, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	nickname := "peer-" + shortPeerID(localPeerID)
	_, err = s.db.Exec(`INSERT INTO profile(peer_id,nickname,bio,avatar,chat_kex_priv,chat_kex_pub,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		localPeerID, nickname, "", "", base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(pub), now, now)
	return err
}

func (s *Store) GetProfile(localPeerID string) (Profile, []byte, error) {
	var p Profile
	var privB64, createdAt string
	var bindingSeq uint64
	var bindingJSON, bindingUpdatedAt string
	err := s.db.QueryRow(`
		SELECT peer_id,nickname,bio,avatar,IFNULL(avatar_cid,''),chat_kex_priv,chat_kex_pub,created_at,
			IFNULL(binding_seq,0), IFNULL(binding_record_json,''), IFNULL(binding_updated_at,''), IFNULL(binding_eth_address,'')
		FROM profile WHERE peer_id = ?`, localPeerID).
		Scan(&p.PeerID, &p.Nickname, &p.Bio, &p.Avatar, &p.AvatarCID, &privB64, &p.ChatKexPub, &createdAt,
			&bindingSeq, &bindingJSON, &bindingUpdatedAt, &p.BindingEthAddress)
	if err != nil {
		return Profile{}, nil, err
	}
	priv, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil {
		return Profile{}, nil, err
	}
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		p.CreatedAt = t
	}
	scanProfileBinding(&p, bindingJSON, bindingUpdatedAt, bindingSeq)
	return p, priv, nil
}

func (s *Store) UpdateProfile(localPeerID, nickname, bio string) (Profile, error) {
	nickname = strings.TrimSpace(nickname)
	if nickname == "" {
		nickname = "peer-" + shortPeerID(localPeerID)
	}
	bio = strings.TrimSpace(bio)
	if utf8.RuneCountInString(bio) > MaxProfileBioLength {
		return Profile{}, fmt.Errorf("bio too long: max %d characters", MaxProfileBioLength)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`UPDATE profile SET nickname=?, bio=?, updated_at=? WHERE peer_id=?`, nickname, bio, now, localPeerID); err != nil {
		return Profile{}, err
	}
	p, _, err := s.GetProfile(localPeerID)
	return p, err
}

func (s *Store) UpdateProfileAvatar(localPeerID, avatar, avatarCID string) (Profile, error) {
	avatar = strings.TrimSpace(avatar)
	avatarCID = strings.TrimSpace(avatarCID)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`UPDATE profile SET avatar=?, avatar_cid=?, updated_at=? WHERE peer_id=?`, avatar, avatarCID, now, localPeerID); err != nil {
		return Profile{}, err
	}
	p, _, err := s.GetProfile(localPeerID)
	return p, err
}

func (s *Store) ensureProfileBioColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(profile)`)
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
	_, err = s.db.Exec(`ALTER TABLE profile ADD COLUMN bio TEXT NOT NULL DEFAULT ''`)
	return err
}

func (s *Store) ensureProfileAvatarColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(profile)`)
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
	_, err = s.db.Exec(`ALTER TABLE profile ADD COLUMN avatar TEXT NOT NULL DEFAULT ''`)
	return err
}

func (s *Store) ensureProfileAvatarCIDColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(profile)`)
	if err != nil {
		return err
	}
	hasCol := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "avatar_cid" {
			hasCol = true
			break
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasCol {
		return nil
	}
	_, err = s.db.Exec(`ALTER TABLE profile ADD COLUMN avatar_cid TEXT NOT NULL DEFAULT ''`)
	return err
}

func (s *Store) ensurePeerAvatarCIDColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(peers)`)
	if err != nil {
		return err
	}
	hasCol := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "avatar_cid" {
			hasCol = true
			break
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasCol {
		return nil
	}
	_, err = s.db.Exec(`ALTER TABLE peers ADD COLUMN avatar_cid TEXT NOT NULL DEFAULT ''`)
	return err
}

func (s *Store) UpsertPeer(peerID, nickname, bio string) error {
	if peerID == "" {
		return nil
	}
	nicknameTrim := strings.TrimSpace(nickname)
	bioTrim := strings.TrimSpace(bio)
	// 新列：空暱稱用 peer-xxxx；已存在列：空暱稱表示「不覆寫」（避免 SendRequest / SessionAccept 等只更新時間卻把暱稱打回 peer-xxxx）。
	insNick := nicknameTrim
	if insNick == "" {
		insNick = "peer-" + shortPeerID(peerID)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		INSERT INTO peers(peer_id,nickname,bio,avatar,updated_at,last_seen_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(peer_id) DO UPDATE SET
			nickname=CASE WHEN ? != '' THEN excluded.nickname ELSE peers.nickname END,
			bio=CASE WHEN ? != '' THEN excluded.bio ELSE peers.bio END,
			avatar=CASE WHEN excluded.avatar != '' THEN excluded.avatar ELSE peers.avatar END,
			last_seen_at=CASE WHEN excluded.last_seen_at != '' THEN excluded.last_seen_at ELSE peers.last_seen_at END,
			updated_at=excluded.updated_at
	`, peerID, insNick, bioTrim, "", now, now, nicknameTrim, bioTrim)
	return err
}

// UpdatePeerBindingEthAddress 僅更新 peers.binding_eth_address（例如 profile_sync 冗餘展示）。
func (s *Store) UpdatePeerBindingEthAddress(peerID, eth string) error {
	if peerID == "" {
		return nil
	}
	eth = strings.TrimSpace(eth)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`UPDATE peers SET binding_eth_address=?, updated_at=? WHERE peer_id=?`, eth, now, peerID)
	return err
}

func (s *Store) UpdatePeerAvatar(peerID, avatar string) error {
	return s.updatePeerAvatar(peerID, avatar, nil)
}

// updatePeerAvatar sets the peer's local avatar filename. If pinnedCID is non-nil, avatar_cid is set to *pinnedCID
// (typically after IPFS pin). If pinnedCID is nil, avatar_cid is cleared when the filename changes; otherwise it is kept.
func (s *Store) updatePeerAvatar(peerID, avatar string, pinnedCID *string) error {
	if peerID == "" {
		return sql.ErrNoRows
	}
	avatar = strings.TrimSpace(avatar)
	var oldAvatar sql.NullString
	var oldCID sql.NullString
	err := s.db.QueryRow(`SELECT avatar, avatar_cid FROM peers WHERE peer_id=?`, peerID).Scan(&oldAvatar, &oldCID)
	if err != nil {
		return err
	}
	var finalCID string
	if pinnedCID != nil {
		finalCID = strings.TrimSpace(*pinnedCID)
	} else {
		if oldAvatar.String != avatar {
			finalCID = ""
		} else if oldCID.Valid {
			finalCID = oldCID.String
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.Exec(`UPDATE peers SET avatar=?, avatar_cid=?, updated_at=? WHERE peer_id=?`, avatar, finalCID, now, peerID)
	return err
}

// DedupePeersByPeerID deletes duplicate rows in peers that share the same peer_id (legacy DBs
// without a working PRIMARY KEY). Keeps the newest row by updated_at and merges fields.
func (s *Store) DedupePeersByPeerID() error {
	if s == nil || s.db == nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT peer_id FROM peers GROUP BY peer_id HAVING COUNT(*) > 1`)
	if err != nil {
		return fmt.Errorf("list duplicate peer ids: %w", err)
	}
	var dupPeers []string
	for rows.Next() {
		var peerID string
		if err := rows.Scan(&peerID); err != nil {
			_ = rows.Close()
			return err
		}
		dupPeers = append(dupPeers, peerID)
	}
	_ = rows.Close()
	if len(dupPeers) == 0 {
		return nil
	}
	deleted := 0
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, peerID := range dupPeers {
		rrows, err := s.db.Query(`
			SELECT rowid, nickname, bio, avatar, IFNULL(avatar_cid,''), blocked, last_seen_at, updated_at
			FROM peers WHERE peer_id=? ORDER BY updated_at DESC, rowid DESC`, peerID)
		if err != nil {
			return err
		}
		type peerRow struct {
			rowid                                      int64
			nickname, bio, avatar, avatarCID, lastSeen, updatedAt string
			blocked                                    int
		}
		var list []peerRow
		for rrows.Next() {
			var r peerRow
			if err := rrows.Scan(&r.rowid, &r.nickname, &r.bio, &r.avatar, &r.avatarCID, &r.blocked, &r.lastSeen, &r.updatedAt); err != nil {
				_ = rrows.Close()
				return err
			}
			list = append(list, r)
		}
		_ = rrows.Close()
		if len(list) < 2 {
			continue
		}
		keeper := list[0]
		for _, r := range list[1:] {
			if strings.TrimSpace(keeper.nickname) == "" && strings.TrimSpace(r.nickname) != "" {
				keeper.nickname = r.nickname
			}
			if strings.TrimSpace(keeper.bio) == "" && strings.TrimSpace(r.bio) != "" {
				keeper.bio = r.bio
			}
			if strings.TrimSpace(keeper.avatar) == "" && strings.TrimSpace(r.avatar) != "" {
				keeper.avatar = r.avatar
			}
			if strings.TrimSpace(keeper.avatarCID) == "" && strings.TrimSpace(r.avatarCID) != "" {
				keeper.avatarCID = r.avatarCID
			}
			if r.blocked != 0 {
				keeper.blocked = 1
			}
			if parseDBTime(r.lastSeen).After(parseDBTime(keeper.lastSeen)) {
				keeper.lastSeen = r.lastSeen
			}
			if _, err := s.db.Exec(`DELETE FROM peers WHERE rowid=?`, r.rowid); err != nil {
				return fmt.Errorf("delete duplicate peer row peer_id=%s rowid=%d: %w", peerID, r.rowid, err)
			}
			deleted++
		}
		if _, err := s.db.Exec(`UPDATE peers SET nickname=?, bio=?, avatar=?, avatar_cid=?, blocked=?, last_seen_at=?, updated_at=? WHERE rowid=?`,
			keeper.nickname, keeper.bio, keeper.avatar, keeper.avatarCID, keeper.blocked, keeper.lastSeen, now, keeper.rowid); err != nil {
			return fmt.Errorf("merge deduped peer row peer_id=%s: %w", peerID, err)
		}
	}
	if deleted > 0 {
		log.Printf("[chat] dedupe peers: removed duplicate rows=%d peer_ids=%d", deleted, len(dupPeers))
	}
	return nil
}

// DeletePeerAndRelatedData removes the contact row, friend requests, and retry jobs for that peer.
// Call DeleteConversationData first for each conversation with this peer.
func (s *Store) DeletePeerAndRelatedData(peerID string) error {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return errors.New("peer_id is empty")
	}
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
	if _, err := tx.Exec(`DELETE FROM friend_request_jobs WHERE job_id IN (SELECT request_id FROM requests WHERE from_peer_id=? OR to_peer_id=?)`, peerID, peerID); err != nil {
		return fmt.Errorf("delete friend_request_jobs by request: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM requests WHERE from_peer_id=? OR to_peer_id=?`, peerID, peerID); err != nil {
		return fmt.Errorf("delete requests: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM friend_request_jobs WHERE peer_id=?`, peerID); err != nil {
		return fmt.Errorf("delete friend_request_jobs by peer: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM peers WHERE peer_id=?`, peerID); err != nil {
		return fmt.Errorf("delete peer: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) ListContacts() ([]Contact, error) {
	// JOIN must follow FROM immediately (SQLite does not allow WHERE between FROM and JOIN).
	// peers.peer_id is PRIMARY KEY, so no rowid disambiguation needed.
	rows, err := s.db.Query(`
		SELECT
			p.peer_id,
			p.nickname,
			p.bio,
			p.avatar,
			IFNULL(p.avatar_cid,''),
			p.blocked,
			p.last_seen_at,
			p.updated_at,
			c.retention_minutes,
			COALESCE((
				SELECT r.nickname
				FROM requests r
				WHERE r.nickname != ''
				  AND r.from_peer_id = p.peer_id AND r.to_peer_id = ?
				ORDER BY r.updated_at DESC, r.created_at DESC
				LIMIT 1
			), ''),
			IFNULL(p.binding_record_json,''),
			IFNULL(p.binding_eth_address,''),
			IFNULL(p.binding_status,''),
			IFNULL(p.binding_validated_at,''),
			IFNULL(p.binding_error,'')
		FROM peers p
		INNER JOIN conversations c ON c.peer_id = p.peer_id
			AND c.state = ?
			AND c.conversation_id = (
				SELECT c2.conversation_id FROM conversations c2
				WHERE c2.peer_id = p.peer_id AND c2.state = ?
				ORDER BY c2.updated_at DESC, c2.conversation_id DESC
				LIMIT 1
			)
			AND (
				EXISTS (
					SELECT 1 FROM requests r
					WHERE r.state = ?
					  AND (
						(r.from_peer_id = ? AND r.to_peer_id = p.peer_id)
						OR (r.from_peer_id = p.peer_id AND r.to_peer_id = ?)
					  )
				)
				OR EXISTS (
					SELECT 1 FROM session_states ss WHERE ss.conversation_id = c.conversation_id
				)
			)
		ORDER BY c.updated_at DESC, p.updated_at DESC
	`, s.localPeerID, ConversationStateActive, ConversationStateActive, RequestStateAccepted, s.localPeerID, s.localPeerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		var blocked int
		var lastSeenAt, updatedAt string
		var bindJSON, bindEth, bindStatus, bindValAt, bindErr string
		if err := rows.Scan(&c.PeerID, &c.Nickname, &c.Bio, &c.Avatar, &c.CID, &blocked, &lastSeenAt, &updatedAt, &c.RetentionMinutes, &c.RemoteNickname,
			&bindJSON, &bindEth, &bindStatus, &bindValAt, &bindErr); err != nil {
			return nil, err
		}
		c.Blocked = blocked != 0
		c.LastSeenAt = parseDBTime(lastSeenAt)
		c.UpdatedAt = parseDBTime(updatedAt)
		scanContactBinding(&c, bindJSON, bindEth, bindStatus, bindValAt, bindErr)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetPeer(peerID string) (Contact, error) {
	var c Contact
	var blocked int
	var lastSeenAt, updatedAt string
	var bindJSON, bindEth, bindStatus, bindValAt, bindErr string
	err := s.db.QueryRow(`
		SELECT
			p.peer_id,
			p.nickname,
			p.bio,
			p.avatar,
			IFNULL(p.avatar_cid,''),
			p.blocked,
			p.last_seen_at,
			p.updated_at,
			COALESCE(c.retention_minutes, 0),
			COALESCE((
				SELECT r.nickname
				FROM requests r
				WHERE r.nickname != ''
				  AND r.from_peer_id = p.peer_id AND r.to_peer_id = ?
				ORDER BY r.updated_at DESC, r.created_at DESC
				LIMIT 1
			), ''),
			IFNULL(p.binding_record_json,''),
			IFNULL(p.binding_eth_address,''),
			IFNULL(p.binding_status,''),
			IFNULL(p.binding_validated_at,''),
			IFNULL(p.binding_error,'')
		FROM peers p
		LEFT JOIN conversations c ON c.conversation_id = (
			SELECT c2.conversation_id FROM conversations c2
			WHERE c2.peer_id = p.peer_id
			ORDER BY c2.updated_at DESC, c2.conversation_id DESC
			LIMIT 1
		)
		WHERE p.peer_id=?
	`, s.localPeerID, peerID).
		Scan(&c.PeerID, &c.Nickname, &c.Bio, &c.Avatar, &c.CID, &blocked, &lastSeenAt, &updatedAt, &c.RetentionMinutes, &c.RemoteNickname,
			&bindJSON, &bindEth, &bindStatus, &bindValAt, &bindErr)
	if err != nil {
		return Contact{}, err
	}
	c.Blocked = blocked != 0
	c.LastSeenAt = parseDBTime(lastSeenAt)
	c.UpdatedAt = parseDBTime(updatedAt)
	scanContactBinding(&c, bindJSON, bindEth, bindStatus, bindValAt, bindErr)
	return c, nil
}

func (s *Store) UpdatePeerNickname(peerID, nickname string) (Contact, error) {
	if peerID == "" {
		return Contact{}, sql.ErrNoRows
	}
	nickname = strings.TrimSpace(nickname)
	if nickname == "" {
		nickname = "peer-" + shortPeerID(peerID)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`UPDATE peers SET nickname=?, updated_at=? WHERE peer_id=?`, nickname, now, peerID); err != nil {
		return Contact{}, err
	}
	return s.GetPeer(peerID)
}

func (s *Store) ensurePeerBioColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(peers)`)
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
	_, err = s.db.Exec(`ALTER TABLE peers ADD COLUMN bio TEXT NOT NULL DEFAULT ''`)
	return err
}

func (s *Store) ensurePeerAvatarColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(peers)`)
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
	_, err = s.db.Exec(`ALTER TABLE peers ADD COLUMN avatar TEXT NOT NULL DEFAULT ''`)
	return err
}

func (s *Store) upgradeLegacyAutoNicknames() error {
	rows, err := s.db.Query(`SELECT peer_id, nickname, chat_kex_priv, chat_kex_pub, created_at FROM profile`)
	if err != nil {
		return err
	}
	var profileUpdates []struct {
		peerID   string
		nickname string
	}
	for rows.Next() {
		var peerID, nickname, priv, pub, createdAt string
		if err := rows.Scan(&peerID, &nickname, &priv, &pub, &createdAt); err != nil {
			_ = rows.Close()
			return err
		}
		if isLegacyAutoNickname(peerID, nickname) {
			profileUpdates = append(profileUpdates, struct {
				peerID   string
				nickname string
			}{peerID: peerID, nickname: "peer-" + shortPeerID(peerID)})
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, upd := range profileUpdates {
		if _, err := s.db.Exec(`UPDATE profile SET nickname=?, updated_at=? WHERE peer_id=?`, upd.nickname, time.Now().UTC().Format(time.RFC3339Nano), upd.peerID); err != nil {
			return err
		}
	}

	rows, err = s.db.Query(`SELECT peer_id, nickname FROM peers`)
	if err != nil {
		return err
	}
	var peerUpdates []struct {
		peerID   string
		nickname string
	}
	for rows.Next() {
		var peerID, nickname string
		if err := rows.Scan(&peerID, &nickname); err != nil {
			_ = rows.Close()
			return err
		}
		if isLegacyAutoNickname(peerID, nickname) {
			peerUpdates = append(peerUpdates, struct {
				peerID   string
				nickname string
			}{peerID: peerID, nickname: "peer-" + shortPeerID(peerID)})
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, upd := range peerUpdates {
		if _, err := s.db.Exec(`UPDATE peers SET nickname=?, updated_at=? WHERE peer_id=?`, upd.nickname, time.Now().UTC().Format(time.RFC3339Nano), upd.peerID); err != nil {
			return err
		}
	}
	return nil
}

func isLegacyAutoNickname(peerID, nickname string) bool {
	if peerID == "" || nickname == "" {
		return false
	}
	if len(peerID) <= 8 {
		return false
	}
	return nickname == "peer-"+peerID[:8]
}

func (s *Store) SetPeerBlocked(peerID string, blocked bool) (Contact, error) {
	if peerID == "" {
		return Contact{}, sql.ErrNoRows
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	val := 0
	if blocked {
		val = 1
	}
	if _, err := s.db.Exec(`UPDATE peers SET blocked=?, updated_at=? WHERE peer_id=?`, val, now, peerID); err != nil {
		return Contact{}, err
	}
	return s.GetPeer(peerID)
}

func NormalizeChatFileName(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "" || base == "." || base == "/" {
		return "file"
	}
	base = strings.ReplaceAll(base, "\x00", "")
	if len(base) > 120 {
		base = base[:120]
	}
	return base
}

func ValidateChatFileData(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxChatFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxChatFileBytes {
		return nil, fmt.Errorf("file too large: max %d bytes", MaxChatFileBytes)
	}
	return data, nil
}

func parseDBTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

func formatDBTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func firstNonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func shortPeerID(v string) string {
	if len(v) <= 8 {
		return v
	}
	return v[len(v)-8:]
}

func deriveSessionState(conversationID, localPeerID, remotePeerID string, localPriv []byte, remotePubB64 string) (sessionState, error) {
	remotePubB64 = strings.TrimSpace(remotePubB64)
	if remotePubB64 == "" {
		return sessionState{}, fmt.Errorf("remote chat kex pub is empty")
	}
	remotePub, err := base64.StdEncoding.DecodeString(remotePubB64)
	if err != nil {
		return sessionState{}, err
	}
	shared, err := protocol.X25519SharedSecret(localPriv, remotePub)
	if err != nil {
		return sessionState{}, err
	}
	// 與電路 hop 的 DeriveHopKeys 分離，使用 mesh-proxy/chat/e2ee/v1/* HKDF info。
	k1, k2, err := protocol.DeriveChatSessionKeys(shared)
	if err != nil {
		return sessionState{}, err
	}
	sess := sessionState{
		ConversationID: conversationID,
		PeerID:         remotePeerID,
	}
	if localPeerID < remotePeerID {
		sess.SendKey = k1
		sess.RecvKey = k2
	} else {
		sess.SendKey = k2
		sess.RecvKey = k1
	}
	return sess, nil
}
