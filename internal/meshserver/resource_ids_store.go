package meshserver

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

// ResourceIDsStore provides stable, auto-increment int IDs for meshserver resources
// (space/server and channel/group), keyed by their string IDs (server_id/channel_id).
type ResourceIDsStore struct {
	mu   sync.Mutex
	path string
	db   *sql.DB
}

func NewResourceIDsStore(path string) *ResourceIDsStore {
	return &ResourceIDsStore{path: path}
}

func (s *ResourceIDsStore) ensureDBLocked() (*sql.DB, error) {
	if s.db != nil {
		return s.db, nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 15000`); err != nil {
		_ = db.Close()
		return nil, err
	}

	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS meshserver_space_ids (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id TEXT NOT NULL UNIQUE
);`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS meshserver_channel_ids (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id TEXT NOT NULL UNIQUE
);`); err != nil {
		_ = db.Close()
		return nil, err
	}

	s.db = db
	return s.db, nil
}

func (s *ResourceIDsStore) GetOrCreateSpaceID(serverID string) (int, error) {
	if serverID == "" {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.ensureDBLocked()
	if err != nil {
		return 0, err
	}

	// Insert if missing.
	if _, err := db.Exec(`INSERT OR IGNORE INTO meshserver_space_ids(server_id) VALUES(?)`, serverID); err != nil {
		return 0, err
	}

	var id int
	if err := db.QueryRow(`SELECT id FROM meshserver_space_ids WHERE server_id = ?`, serverID).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *ResourceIDsStore) GetOrCreateChannelID(channelID string) (int, error) {
	if channelID == "" {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.ensureDBLocked()
	if err != nil {
		return 0, err
	}

	// Insert if missing.
	if _, err := db.Exec(`INSERT OR IGNORE INTO meshserver_channel_ids(channel_id) VALUES(?)`, channelID); err != nil {
		return 0, err
	}

	var id int
	if err := db.QueryRow(`SELECT id FROM meshserver_channel_ids WHERE channel_id = ?`, channelID).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *ResourceIDsStore) GetServerIDBySpaceID(spaceID int) (string, error) {
	if spaceID <= 0 {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.ensureDBLocked()
	if err != nil {
		return "", err
	}

	var serverID string
	if err := db.QueryRow(`SELECT server_id FROM meshserver_space_ids WHERE id = ?`, spaceID).Scan(&serverID); err != nil {
		return "", err
	}
	return serverID, nil
}

func (s *ResourceIDsStore) GetChannelIDByChannelIDIntID(channelIntID int) (string, error) {
	if channelIntID <= 0 {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.ensureDBLocked()
	if err != nil {
		return "", err
	}

	var channelID string
	if err := db.QueryRow(`SELECT channel_id FROM meshserver_channel_ids WHERE id = ?`, channelIntID).Scan(&channelID); err != nil {
		return "", err
	}
	return channelID, nil
}

