package publicchannel

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func NewStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create public channel db dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open public channel sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 15000`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set sqlite busy_timeout: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
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
		`CREATE TABLE IF NOT EXISTS public_channels (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id TEXT NOT NULL UNIQUE,
			owner_peer_id TEXT NOT NULL,
			owner_version INTEGER NOT NULL,
			name TEXT NOT NULL,
			avatar_json TEXT NOT NULL DEFAULT '',
			bio TEXT NOT NULL DEFAULT '',
			profile_version INTEGER NOT NULL,
			last_message_id INTEGER NOT NULL DEFAULT 0,
			last_seq INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			head_updated_at INTEGER NOT NULL DEFAULT 0,
			profile_signature TEXT NOT NULL,
			head_signature TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS public_channel_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_db_id INTEGER NOT NULL,
			message_id INTEGER NOT NULL,
			version INTEGER NOT NULL,
			seq INTEGER NOT NULL,
			owner_version INTEGER NOT NULL,
			creator_peer_id TEXT NOT NULL,
			author_peer_id TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			is_deleted INTEGER NOT NULL DEFAULT 0,
			message_type TEXT NOT NULL,
			content_json TEXT NOT NULL DEFAULT '',
			signature TEXT NOT NULL,
			UNIQUE(channel_db_id, message_id),
			UNIQUE(channel_db_id, seq)
		)`,
		`CREATE TABLE IF NOT EXISTS public_channel_changes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_db_id INTEGER NOT NULL,
			seq INTEGER NOT NULL,
			change_type TEXT NOT NULL,
			message_id INTEGER,
			version INTEGER,
			is_deleted INTEGER,
			profile_version INTEGER,
			created_at INTEGER NOT NULL,
			UNIQUE(channel_db_id, seq)
		)`,
		`CREATE TABLE IF NOT EXISTS public_channel_sync_state (
			channel_db_id INTEGER PRIMARY KEY,
			last_seen_seq INTEGER NOT NULL DEFAULT 0,
			last_synced_seq INTEGER NOT NULL DEFAULT 0,
			latest_loaded_message_id INTEGER NOT NULL DEFAULT 0,
			oldest_loaded_message_id INTEGER NOT NULL DEFAULT 0,
			subscribed INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS public_channel_providers (
			channel_db_id INTEGER NOT NULL,
			peer_id TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL,
			PRIMARY KEY(channel_db_id, peer_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_public_channels_owner ON public_channels(owner_peer_id)`,
		`CREATE INDEX IF NOT EXISTS idx_public_channel_messages_channel_msg ON public_channel_messages(channel_db_id, message_id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_public_channel_messages_channel_seq ON public_channel_messages(channel_db_id, seq ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_public_channel_changes_channel_seq ON public_channel_changes(channel_db_id, seq ASC)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate public channel db: %w", err)
		}
	}
	if _, err := s.db.Exec(`ALTER TABLE public_channels ADD COLUMN head_updated_at INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return fmt.Errorf("migrate public channel db add head_updated_at: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE public_channels SET head_updated_at=updated_at WHERE head_updated_at=0`); err != nil {
		return fmt.Errorf("migrate public channel db backfill head_updated_at: %w", err)
	}
	return nil
}

func marshalAvatar(v Avatar) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func unmarshalAvatar(raw string) Avatar {
	if raw == "" {
		return Avatar{}
	}
	var out Avatar
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func marshalContent(v MessageContent) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func unmarshalContent(raw string) MessageContent {
	if raw == "" {
		return MessageContent{}
	}
	var out MessageContent
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func (s *Store) ensureSyncStateTx(tx *sql.Tx, channelDBID int64, now int64) error {
	_, err := tx.Exec(`
		INSERT INTO public_channel_sync_state(channel_db_id, updated_at)
		VALUES(?, ?)
		ON CONFLICT(channel_db_id) DO NOTHING
	`, channelDBID, now)
	return err
}

func (s *Store) getChannelRowTx(tx *sql.Tx, channelID string) (int64, ChannelProfile, ChannelHead, error) {
	var (
		id            int64
		avatarJSON    string
		profileSig    string
		headSig       string
		profile       ChannelProfile
		head          ChannelHead
	)
	err := tx.QueryRow(`
		SELECT id, owner_peer_id, owner_version, name, avatar_json, bio, profile_version,
		       last_message_id, last_seq, created_at, updated_at, head_updated_at, profile_signature, head_signature
		FROM public_channels WHERE channel_id=?
	`, channelID).Scan(
		&id,
		&profile.OwnerPeerID,
		&profile.OwnerVersion,
		&profile.Name,
		&avatarJSON,
		&profile.Bio,
		&profile.ProfileVersion,
		&head.LastMessageID,
		&head.LastSeq,
		&profile.CreatedAt,
		&profile.UpdatedAt,
		&head.UpdatedAt,
		&profileSig,
		&headSig,
	)
	if err != nil {
		return 0, ChannelProfile{}, ChannelHead{}, err
	}
	profile.ChannelID = channelID
	profile.Avatar = unmarshalAvatar(avatarJSON)
	profile.Signature = profileSig
	head.ChannelID = channelID
	head.OwnerPeerID = profile.OwnerPeerID
	head.OwnerVersion = profile.OwnerVersion
	head.ProfileVersion = profile.ProfileVersion
	head.Signature = headSig
	return id, profile, head, nil
}

func (s *Store) getChannelDBID(channelID string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM public_channels WHERE channel_id=?`, channelID).Scan(&id)
	return id, err
}

func (s *Store) CreateOwnedChannel(profile ChannelProfile, head ChannelHead, now int64, localPeerID string) error {
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
	res, err := tx.Exec(`
		INSERT INTO public_channels(
			channel_id, owner_peer_id, owner_version, name, avatar_json, bio, profile_version,
			last_message_id, last_seq, created_at, updated_at, head_updated_at, profile_signature, head_signature
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, profile.ChannelID, profile.OwnerPeerID, profile.OwnerVersion, profile.Name, marshalAvatar(profile.Avatar), profile.Bio,
		profile.ProfileVersion, head.LastMessageID, head.LastSeq, profile.CreatedAt, profile.UpdatedAt, head.UpdatedAt, profile.Signature, head.Signature)
	if err != nil {
		return err
	}
	channelDBID, err := res.LastInsertId()
	if err != nil {
		return err
	}
	if err := s.ensureSyncStateTx(tx, channelDBID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE public_channel_sync_state
		SET last_seen_seq=?, last_synced_seq=?, subscribed=1, updated_at=?
		WHERE channel_db_id=?
	`, head.LastSeq, head.LastSeq, now, channelDBID); err != nil {
		return err
	}
	if localPeerID != "" {
		if _, err := tx.Exec(`
			INSERT INTO public_channel_providers(channel_db_id, peer_id, source, updated_at)
			VALUES(?,?,?,?)
			ON CONFLICT(channel_db_id, peer_id) DO UPDATE SET source=excluded.source, updated_at=excluded.updated_at
		`, channelDBID, localPeerID, "local", now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) EnsureSubscribedChannel(channelID string, lastSeenSeq, now int64) error {
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
	var channelDBID int64
	err = tx.QueryRow(`SELECT id FROM public_channels WHERE channel_id=?`, channelID).Scan(&channelDBID)
	switch {
	case err == sql.ErrNoRows:
		res, err := tx.Exec(`
			INSERT INTO public_channels(
				channel_id, owner_peer_id, owner_version, name, avatar_json, bio, profile_version,
				last_message_id, last_seq, created_at, updated_at, head_updated_at, profile_signature, head_signature
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		`, channelID, "", 0, "", "", "", 0, 0, 0, now, now, now, "", "")
		if err != nil {
			return err
		}
		channelDBID, err = res.LastInsertId()
		if err != nil {
			return err
		}
	case err != nil:
		return err
	}
	if err := s.ensureSyncStateTx(tx, channelDBID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE public_channel_sync_state
		SET last_seen_seq=CASE
				WHEN last_seen_seq < ? THEN ? ELSE last_seen_seq END,
			subscribed=1,
			updated_at=?
		WHERE channel_db_id=?
	`, lastSeenSeq, lastSeenSeq, now, channelDBID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) CommitOwnedProfileChange(profile ChannelProfile, head ChannelHead, change ChannelChange) error {
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
	channelDBID, currentProfile, _, err := s.getChannelRowTx(tx, profile.ChannelID)
	if err != nil {
		return err
	}
	if currentProfile.OwnerPeerID != profile.OwnerPeerID {
		return fmt.Errorf("owner mismatch for channel %s", profile.ChannelID)
	}
	if _, err := tx.Exec(`
		UPDATE public_channels
		SET owner_version=?, name=?, avatar_json=?, bio=?, profile_version=?, last_message_id=?, last_seq=?, created_at=?, updated_at=?, head_updated_at=?, profile_signature=?, head_signature=?
		WHERE id=?
	`, profile.OwnerVersion, profile.Name, marshalAvatar(profile.Avatar), profile.Bio, profile.ProfileVersion,
		head.LastMessageID, head.LastSeq, profile.CreatedAt, profile.UpdatedAt, head.UpdatedAt, profile.Signature, head.Signature, channelDBID); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO public_channel_changes(channel_db_id, seq, change_type, message_id, version, is_deleted, profile_version, created_at)
		VALUES(?,?,?,?,?,?,?,?)
	`, channelDBID, change.Seq, change.ChangeType, nil, nil, nil, nullableInt64(change.ProfileVersion), change.CreatedAt); err != nil {
		return err
	}
	if err := s.ensureSyncStateTx(tx, channelDBID, change.CreatedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE public_channel_sync_state
		SET last_seen_seq=?, last_synced_seq=?, subscribed=1, updated_at=?
		WHERE channel_db_id=?
	`, head.LastSeq, head.LastSeq, change.CreatedAt, channelDBID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) CommitOwnedMessageChange(message ChannelMessage, head ChannelHead, change ChannelChange) error {
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
	channelDBID, profile, _, err := s.getChannelRowTx(tx, message.ChannelID)
	if err != nil {
		return err
	}
	if profile.OwnerPeerID != message.AuthorPeerID {
		return fmt.Errorf("author peer must equal owner peer")
	}
	var existing int64
	err = tx.QueryRow(`
		SELECT version
		FROM public_channel_messages
		WHERE channel_db_id=? AND message_id=?
	`, channelDBID, message.MessageID).Scan(&existing)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		if _, err := tx.Exec(`
			UPDATE public_channel_messages
			SET version=?, seq=?, owner_version=?, creator_peer_id=?, author_peer_id=?, created_at=?, updated_at=?, is_deleted=?, message_type=?, content_json=?, signature=?
			WHERE channel_db_id=? AND message_id=?
		`, message.Version, message.Seq, message.OwnerVersion, message.CreatorPeerID, message.AuthorPeerID, message.CreatedAt, message.UpdatedAt,
			boolToInt(message.IsDeleted), message.MessageType, marshalContent(message.Content), message.Signature, channelDBID, message.MessageID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`
			INSERT INTO public_channel_messages(
				channel_db_id, message_id, version, seq, owner_version, creator_peer_id, author_peer_id,
				created_at, updated_at, is_deleted, message_type, content_json, signature
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		`, channelDBID, message.MessageID, message.Version, message.Seq, message.OwnerVersion, message.CreatorPeerID, message.AuthorPeerID,
			message.CreatedAt, message.UpdatedAt, boolToInt(message.IsDeleted), message.MessageType, marshalContent(message.Content), message.Signature); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		UPDATE public_channels
		SET owner_version=?, profile_version=?, last_message_id=?, last_seq=?, head_updated_at=?, head_signature=?
		WHERE id=?
	`, head.OwnerVersion, head.ProfileVersion, head.LastMessageID, head.LastSeq, head.UpdatedAt, head.Signature, channelDBID); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO public_channel_changes(channel_db_id, seq, change_type, message_id, version, is_deleted, profile_version, created_at)
		VALUES(?,?,?,?,?,?,?,?)
	`, channelDBID, change.Seq, change.ChangeType, nullableInt64(change.MessageID), nullableInt64(change.Version),
		nullableBool(change.IsDeleted), nullableInt64(change.ProfileVersion), change.CreatedAt); err != nil {
		return err
	}
	if err := s.ensureSyncStateTx(tx, channelDBID, change.CreatedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE public_channel_sync_state
		SET last_seen_seq=?, last_synced_seq=?, latest_loaded_message_id=CASE
				WHEN latest_loaded_message_id = 0 OR latest_loaded_message_id < ? THEN ? ELSE latest_loaded_message_id END,
			oldest_loaded_message_id=CASE
				WHEN oldest_loaded_message_id = 0 OR oldest_loaded_message_id > ? THEN ? ELSE oldest_loaded_message_id END,
			subscribed=1,
			updated_at=?
		WHERE channel_db_id=?
	`, head.LastSeq, head.LastSeq, message.MessageID, message.MessageID, message.MessageID, message.MessageID, change.CreatedAt, channelDBID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) ApplyProfile(profile ChannelProfile) error {
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
	id, currentProfile, currentHead, err := s.getChannelRowTx(tx, profile.ChannelID)
	switch {
	case err == sql.ErrNoRows:
		head := ChannelHead{
			ChannelID:      profile.ChannelID,
			OwnerPeerID:    profile.OwnerPeerID,
			OwnerVersion:   profile.OwnerVersion,
			LastMessageID:  0,
			ProfileVersion: profile.ProfileVersion,
			LastSeq:        0,
			UpdatedAt:      profile.UpdatedAt,
			Signature:      "",
		}
		res, err := tx.Exec(`
			INSERT INTO public_channels(
				channel_id, owner_peer_id, owner_version, name, avatar_json, bio, profile_version,
				last_message_id, last_seq, created_at, updated_at, head_updated_at, profile_signature, head_signature
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		`, profile.ChannelID, profile.OwnerPeerID, profile.OwnerVersion, profile.Name, marshalAvatar(profile.Avatar), profile.Bio,
			profile.ProfileVersion, head.LastMessageID, head.LastSeq, profile.CreatedAt, profile.UpdatedAt, head.UpdatedAt, profile.Signature, head.Signature)
		if err != nil {
			return err
		}
		channelDBID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := s.ensureSyncStateTx(tx, channelDBID, profile.UpdatedAt); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if currentProfile.OwnerPeerID != "" && profile.OwnerPeerID != currentProfile.OwnerPeerID {
			return fmt.Errorf("owner mismatch for channel %s", profile.ChannelID)
		}
		if profile.ProfileVersion < currentProfile.ProfileVersion {
			return nil
		}
		if profile.ProfileVersion == currentProfile.ProfileVersion && profile.Signature == currentProfile.Signature {
			return nil
		}
		if _, err := tx.Exec(`
			UPDATE public_channels
			SET owner_peer_id=?, owner_version=?, name=?, avatar_json=?, bio=?, profile_version=?, created_at=?, updated_at=?, profile_signature=?, head_signature=CASE WHEN head_signature='' THEN ? ELSE head_signature END
			WHERE id=?
		`, profile.OwnerPeerID, profile.OwnerVersion, profile.Name, marshalAvatar(profile.Avatar), profile.Bio, profile.ProfileVersion,
			profile.CreatedAt, profile.UpdatedAt, profile.Signature, currentHead.Signature, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) ApplyHead(head ChannelHead) error {
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
	id, profile, currentHead, err := s.getChannelRowTx(tx, head.ChannelID)
	if err != nil {
		return err
	}
	if head.OwnerPeerID != profile.OwnerPeerID {
		return fmt.Errorf("head owner mismatch for channel %s", head.ChannelID)
	}
	if head.LastSeq < currentHead.LastSeq {
		return nil
	}
	if head.LastSeq == currentHead.LastSeq && head.Signature == currentHead.Signature {
		return nil
	}
	if _, err := tx.Exec(`
		UPDATE public_channels
		SET owner_version=?, last_message_id=?, profile_version=?, last_seq=?, head_updated_at=?, head_signature=?
		WHERE id=?
	`, head.OwnerVersion, head.LastMessageID, head.ProfileVersion, head.LastSeq, head.UpdatedAt, head.Signature, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) ApplyMessage(message ChannelMessage) error {
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
	channelDBID, profile, head, err := s.getChannelRowTx(tx, message.ChannelID)
	if err != nil {
		return err
	}
	if message.AuthorPeerID != profile.OwnerPeerID {
		return fmt.Errorf("author peer must equal owner peer")
	}
	if message.OwnerVersion != profile.OwnerVersion {
		return fmt.Errorf("owner_version mismatch")
	}
	var existingVersion int64
	var existingSignature string
	err = tx.QueryRow(`
		SELECT version, signature
		FROM public_channel_messages
		WHERE channel_db_id=? AND message_id=?
	`, channelDBID, message.MessageID).Scan(&existingVersion, &existingSignature)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		if message.Version < existingVersion {
			return nil
		}
		if message.Version == existingVersion && message.Signature == existingSignature {
			return nil
		}
		if message.Version == existingVersion && message.Signature != existingSignature {
			return errors.New("same version with different signature")
		}
		if _, err := tx.Exec(`
			UPDATE public_channel_messages
			SET version=?, seq=?, owner_version=?, creator_peer_id=?, author_peer_id=?, created_at=?, updated_at=?, is_deleted=?, message_type=?, content_json=?, signature=?
			WHERE channel_db_id=? AND message_id=?
		`, message.Version, message.Seq, message.OwnerVersion, message.CreatorPeerID, message.AuthorPeerID, message.CreatedAt, message.UpdatedAt,
			boolToInt(message.IsDeleted), message.MessageType, marshalContent(message.Content), message.Signature, channelDBID, message.MessageID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`
			INSERT INTO public_channel_messages(
				channel_db_id, message_id, version, seq, owner_version, creator_peer_id, author_peer_id,
				created_at, updated_at, is_deleted, message_type, content_json, signature
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		`, channelDBID, message.MessageID, message.Version, message.Seq, message.OwnerVersion, message.CreatorPeerID, message.AuthorPeerID,
			message.CreatedAt, message.UpdatedAt, boolToInt(message.IsDeleted), message.MessageType, marshalContent(message.Content), message.Signature); err != nil {
			return err
		}
	}
	lastMessageID := head.LastMessageID
	if message.MessageID > lastMessageID {
		lastMessageID = message.MessageID
	}
	lastSeq := head.LastSeq
	if message.Seq > lastSeq {
		lastSeq = message.Seq
	}
	if _, err := tx.Exec(`
		UPDATE public_channels
		SET last_message_id=?, last_seq=?, head_updated_at=CASE WHEN head_updated_at < ? THEN ? ELSE head_updated_at END
		WHERE id=?
	`, lastMessageID, lastSeq, message.UpdatedAt, message.UpdatedAt, channelDBID); err != nil {
		return err
	}
	if err := s.ensureSyncStateTx(tx, channelDBID, message.UpdatedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) RecordChange(change ChannelChange) error {
	channelDBID, err := s.getChannelDBID(change.ChannelID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO public_channel_changes(channel_db_id, seq, change_type, message_id, version, is_deleted, profile_version, created_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(channel_db_id, seq) DO NOTHING
	`, channelDBID, change.Seq, change.ChangeType, nullableInt64(change.MessageID), nullableInt64(change.Version),
		nullableBool(change.IsDeleted), nullableInt64(change.ProfileVersion), change.CreatedAt)
	return err
}

func (s *Store) UpdateSyncState(channelID string, lastSeenSeq, lastSyncedSeq int64, subscribed bool, now int64) error {
	channelDBID, err := s.getChannelDBID(channelID)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`
		INSERT INTO public_channel_sync_state(channel_db_id, last_seen_seq, last_synced_seq, subscribed, updated_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(channel_db_id) DO UPDATE SET
			last_seen_seq=CASE WHEN excluded.last_seen_seq > public_channel_sync_state.last_seen_seq THEN excluded.last_seen_seq ELSE public_channel_sync_state.last_seen_seq END,
			last_synced_seq=CASE WHEN excluded.last_synced_seq > public_channel_sync_state.last_synced_seq THEN excluded.last_synced_seq ELSE public_channel_sync_state.last_synced_seq END,
			subscribed=excluded.subscribed,
			updated_at=excluded.updated_at
	`, channelDBID, lastSeenSeq, lastSyncedSeq, boolToInt(subscribed), now); err != nil {
		return err
	}
	return nil
}

func (s *Store) UpdateLoadedRange(channelID string, messages []ChannelMessage, now int64) error {
	if len(messages) == 0 {
		return nil
	}
	channelDBID, err := s.getChannelDBID(channelID)
	if err != nil {
		return err
	}
	latest := int64(0)
	oldest := int64(0)
	for _, item := range messages {
		if latest == 0 || item.MessageID > latest {
			latest = item.MessageID
		}
		if oldest == 0 || item.MessageID < oldest {
			oldest = item.MessageID
		}
	}
	if _, err := s.db.Exec(`
		INSERT INTO public_channel_sync_state(channel_db_id, latest_loaded_message_id, oldest_loaded_message_id, updated_at)
		VALUES(?,?,?,?)
		ON CONFLICT(channel_db_id) DO UPDATE SET
			latest_loaded_message_id=CASE
				WHEN public_channel_sync_state.latest_loaded_message_id = 0 OR excluded.latest_loaded_message_id > public_channel_sync_state.latest_loaded_message_id
				THEN excluded.latest_loaded_message_id ELSE public_channel_sync_state.latest_loaded_message_id END,
			oldest_loaded_message_id=CASE
				WHEN public_channel_sync_state.oldest_loaded_message_id = 0 OR excluded.oldest_loaded_message_id < public_channel_sync_state.oldest_loaded_message_id
				THEN excluded.oldest_loaded_message_id ELSE public_channel_sync_state.oldest_loaded_message_id END,
			updated_at=excluded.updated_at
	`, channelDBID, latest, oldest, now); err != nil {
		return err
	}
	return nil
}

func (s *Store) UpsertProvider(channelID, peerID, source string, now int64) error {
	if stringsTrim(peerID) == "" {
		return nil
	}
	channelDBID, err := s.getChannelDBID(channelID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO public_channel_providers(channel_db_id, peer_id, source, updated_at)
		VALUES(?,?,?,?)
		ON CONFLICT(channel_db_id, peer_id) DO UPDATE SET source=excluded.source, updated_at=excluded.updated_at
	`, channelDBID, stringsTrim(peerID), stringsTrim(source), now)
	return err
}

func (s *Store) GetChannelProfile(channelID string) (ChannelProfile, error) {
	var (
		profile    ChannelProfile
		avatarJSON string
	)
	err := s.db.QueryRow(`
		SELECT owner_peer_id, owner_version, name, avatar_json, bio, profile_version, created_at, updated_at, profile_signature
		FROM public_channels WHERE channel_id=?
	`, channelID).Scan(
		&profile.OwnerPeerID,
		&profile.OwnerVersion,
		&profile.Name,
		&avatarJSON,
		&profile.Bio,
		&profile.ProfileVersion,
		&profile.CreatedAt,
		&profile.UpdatedAt,
		&profile.Signature,
	)
	if err != nil {
		return ChannelProfile{}, err
	}
	profile.ChannelID = channelID
	profile.Avatar = unmarshalAvatar(avatarJSON)
	return profile, nil
}

func (s *Store) GetChannelHead(channelID string) (ChannelHead, error) {
	var head ChannelHead
	err := s.db.QueryRow(`
		SELECT owner_peer_id, owner_version, last_message_id, profile_version, last_seq, head_updated_at, head_signature
		FROM public_channels WHERE channel_id=?
	`, channelID).Scan(
		&head.OwnerPeerID,
		&head.OwnerVersion,
		&head.LastMessageID,
		&head.ProfileVersion,
		&head.LastSeq,
		&head.UpdatedAt,
		&head.Signature,
	)
	if err != nil {
		return ChannelHead{}, err
	}
	head.ChannelID = channelID
	return head, nil
}

func (s *Store) GetChannelSummary(channelID string) (ChannelSummary, error) {
	profile, err := s.GetChannelProfile(channelID)
	if err != nil {
		return ChannelSummary{}, err
	}
	head, err := s.GetChannelHead(channelID)
	if err != nil {
		return ChannelSummary{}, err
	}
	syncState, err := s.GetChannelSyncState(channelID)
	if err != nil && err != sql.ErrNoRows {
		return ChannelSummary{}, err
	}
	return ChannelSummary{Profile: profile, Head: head, Sync: syncState}, nil
}

func (s *Store) GetChannelMessage(channelID string, messageID int64) (ChannelMessage, error) {
	channelDBID, err := s.getChannelDBID(channelID)
	if err != nil {
		return ChannelMessage{}, err
	}
	var (
		msg         ChannelMessage
		contentJSON string
		isDeleted   int
	)
	err = s.db.QueryRow(`
		SELECT version, seq, owner_version, creator_peer_id, author_peer_id, created_at, updated_at,
		       is_deleted, message_type, content_json, signature
		FROM public_channel_messages
		WHERE channel_db_id=? AND message_id=?
	`, channelDBID, messageID).Scan(
		&msg.Version,
		&msg.Seq,
		&msg.OwnerVersion,
		&msg.CreatorPeerID,
		&msg.AuthorPeerID,
		&msg.CreatedAt,
		&msg.UpdatedAt,
		&isDeleted,
		&msg.MessageType,
		&contentJSON,
		&msg.Signature,
	)
	if err != nil {
		return ChannelMessage{}, err
	}
	msg.ChannelID = channelID
	msg.MessageID = messageID
	msg.IsDeleted = isDeleted != 0
	msg.Content = unmarshalContent(contentJSON)
	return msg, nil
}

func (s *Store) GetChannelMessages(channelID string, beforeMessageID int64, limit int) ([]ChannelMessage, error) {
	channelDBID, err := s.getChannelDBID(channelID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	query := `
		SELECT message_id, version, seq, owner_version, creator_peer_id, author_peer_id, created_at, updated_at,
		       is_deleted, message_type, content_json, signature
		FROM public_channel_messages
		WHERE channel_db_id=?
	`
	args := []any{channelDBID}
	if beforeMessageID > 0 {
		query += ` AND message_id < ?`
		args = append(args, beforeMessageID)
	}
	query += ` ORDER BY message_id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelMessage
	for rows.Next() {
		var (
			msg         ChannelMessage
			contentJSON string
			isDeleted   int
		)
		if err := rows.Scan(
			&msg.MessageID,
			&msg.Version,
			&msg.Seq,
			&msg.OwnerVersion,
			&msg.CreatorPeerID,
			&msg.AuthorPeerID,
			&msg.CreatedAt,
			&msg.UpdatedAt,
			&isDeleted,
			&msg.MessageType,
			&contentJSON,
			&msg.Signature,
		); err != nil {
			return nil, err
		}
		msg.ChannelID = channelID
		msg.IsDeleted = isDeleted != 0
		msg.Content = unmarshalContent(contentJSON)
		out = append(out, msg)
	}
	return out, rows.Err()
}

func (s *Store) GetChannelChanges(channelID string, afterSeq int64, limit int) (GetChangesResponse, error) {
	channelDBID, err := s.getChannelDBID(channelID)
	if err != nil {
		return GetChangesResponse{}, err
	}
	if limit <= 0 {
		limit = DefaultChangesLimit
	}
	head, err := s.GetChannelHead(channelID)
	if err != nil {
		return GetChangesResponse{}, err
	}
	rows, err := s.db.Query(`
		SELECT seq, change_type, message_id, version, is_deleted, profile_version, created_at
		FROM public_channel_changes
		WHERE channel_db_id=? AND seq > ?
		ORDER BY seq ASC
		LIMIT ?
	`, channelDBID, afterSeq, limit+1)
	if err != nil {
		return GetChangesResponse{}, err
	}
	defer rows.Close()
	resp := GetChangesResponse{ChannelID: channelID, CurrentLastSeq: head.LastSeq}
	for rows.Next() {
		var (
			item          ChannelChange
			messageID     sql.NullInt64
			version       sql.NullInt64
			isDeleted     sql.NullInt64
			profileVer    sql.NullInt64
		)
		if err := rows.Scan(&item.Seq, &item.ChangeType, &messageID, &version, &isDeleted, &profileVer, &item.CreatedAt); err != nil {
			return GetChangesResponse{}, err
		}
		item.ChannelID = channelID
		if messageID.Valid {
			v := messageID.Int64
			item.MessageID = &v
		}
		if version.Valid {
			v := version.Int64
			item.Version = &v
		}
		if isDeleted.Valid {
			v := isDeleted.Int64 != 0
			item.IsDeleted = &v
		}
		if profileVer.Valid {
			v := profileVer.Int64
			item.ProfileVersion = &v
		}
		resp.Items = append(resp.Items, item)
	}
	if len(resp.Items) > limit {
		resp.HasMore = true
		resp.Items = resp.Items[:limit]
	}
	if len(resp.Items) > 0 {
		resp.NextAfterSeq = resp.Items[len(resp.Items)-1].Seq
	} else {
		resp.NextAfterSeq = afterSeq
	}
	return resp, rows.Err()
}

func (s *Store) GetChannelSyncState(channelID string) (ChannelSyncState, error) {
	channelDBID, err := s.getChannelDBID(channelID)
	if err != nil {
		return ChannelSyncState{}, err
	}
	var (
		state      ChannelSyncState
		subscribed int
	)
	err = s.db.QueryRow(`
		SELECT last_seen_seq, last_synced_seq, latest_loaded_message_id, oldest_loaded_message_id, subscribed, updated_at
		FROM public_channel_sync_state
		WHERE channel_db_id=?
	`, channelDBID).Scan(
		&state.LastSeenSeq,
		&state.LastSyncedSeq,
		&state.LatestLoadedMessageID,
		&state.OldestLoadedMessageID,
		&subscribed,
		&state.UpdatedAt,
	)
	if err != nil {
		return ChannelSyncState{}, err
	}
	state.ChannelID = channelID
	state.Subscribed = subscribed != 0
	return state, nil
}

func (s *Store) ListProviders(channelID string) ([]ChannelProvider, error) {
	channelDBID, err := s.getChannelDBID(channelID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT peer_id, source, updated_at
		FROM public_channel_providers
		WHERE channel_db_id=?
		ORDER BY updated_at DESC, peer_id ASC
	`, channelDBID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelProvider
	for rows.Next() {
		var item ChannelProvider
		if err := rows.Scan(&item.PeerID, &item.Source, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.ChannelID = channelID
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListChannelsByOwner(ownerPeerID string) ([]ChannelSummary, error) {
	rows, err := s.db.Query(`
		SELECT channel_id
		FROM public_channels
		WHERE owner_peer_id=?
		ORDER BY updated_at DESC, channel_id DESC
	`, ownerPeerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return nil, err
		}
		ids = append(ids, channelID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]ChannelSummary, 0, len(ids))
	for _, channelID := range ids {
		item, err := s.GetChannelSummary(channelID)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Store) ListSubscribedChannels(localOwnerPeerID string) ([]ChannelSummary, error) {
	rows, err := s.db.Query(`
		SELECT c.channel_id
		FROM public_channels c
		LEFT JOIN public_channel_sync_state s ON s.channel_db_id = c.id
		WHERE COALESCE(s.subscribed, 0) = 1 OR c.owner_peer_id = ?
		ORDER BY CASE
			WHEN COALESCE(s.subscribed, 0) = 1 THEN s.updated_at
			ELSE c.updated_at
		END DESC, c.id DESC
	`, stringsTrim(localOwnerPeerID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return nil, err
		}
		ids = append(ids, channelID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]ChannelSummary, 0, len(ids))
	for _, channelID := range ids {
		item, err := s.GetChannelSummary(channelID)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullableInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullableBool(v *bool) any {
	if v == nil {
		return nil
	}
	if *v {
		return 1
	}
	return 0
}

func stringsTrim(v string) string {
	return strings.TrimSpace(v)
}
