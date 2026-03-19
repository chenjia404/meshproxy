package meshserver

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

// MyGroupsStore persists "joined groups" per user and per space.
//
// The cache is keyed by a meshserver connection name plus space_id, because
// membership/permissions are tied to the authenticated session.
type MyGroupsStore struct {
	mu   sync.Mutex
	path string
	db   *sql.DB
}

func NewMyGroupsStore(path string) *MyGroupsStore {
	return &MyGroupsStore{path: path}
}

func (s *MyGroupsStore) ensureDBLocked() (*sql.DB, error) {
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
CREATE TABLE IF NOT EXISTS my_meshserver_groups (
  cache_key TEXT PRIMARY KEY,
  data_json TEXT NOT NULL
);`); err != nil {
		_ = db.Close()
		return nil, err
	}
	s.db = db
	return s.db, nil
}

func (s *MyGroupsStore) Put(cacheKey string, resp *MyGroupsResp) error {
	if cacheKey == "" || resp == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.ensureDBLocked()
	if err != nil {
		return err
	}

	b, err := json.Marshal(resp)
	if err != nil {
		return err
	}

	_, err = db.Exec(`
INSERT INTO my_meshserver_groups(cache_key, data_json)
VALUES(?, ?)
ON CONFLICT(cache_key) DO UPDATE SET data_json = excluded.data_json;`, cacheKey, string(b))
	return err
}

func (s *MyGroupsStore) Get(cacheKey string) (*MyGroupsResp, bool) {
	if cacheKey == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.ensureDBLocked()
	if err != nil {
		return nil, false
	}

	var dataJSON string
	row := db.QueryRow(`SELECT data_json FROM my_meshserver_groups WHERE cache_key = ?`, cacheKey)
	if err := row.Scan(&dataJSON); err != nil {
		return nil, false
	}

	var resp MyGroupsResp
	if err := json.Unmarshal([]byte(dataJSON), &resp); err != nil {
		return nil, false
	}
	return &resp, true
}

