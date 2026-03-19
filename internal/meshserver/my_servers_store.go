package meshserver

import (
	"path/filepath"
	"sync"

	"database/sql"
	"encoding/json"
	"os"

	_ "modernc.org/sqlite"
)

// MyServersStore persists "joined servers" per meshserver server peer id.
//
// Cached list is stored as JSON inside SQLite to avoid writing JSON files.
// Key: meshserverPeerID (ConnectionInfo.name by default).
type MyServersStore struct {
	mu   sync.Mutex
	path string
	db   *sql.DB
}

func NewMyServersStore(path string) *MyServersStore {
	return &MyServersStore{
		path: path,
	}
}

func (s *MyServersStore) ensureDBLocked() error {
	if s.db != nil {
		return nil
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 15000`); err != nil {
		_ = db.Close()
		return err
	}

	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS my_meshserver_servers (
  cache_key TEXT PRIMARY KEY,
  data_json TEXT NOT NULL
);`); err != nil {
		_ = db.Close()
		return err
	}

	s.db = db
	return nil
}

func (s *MyServersStore) Get(meshserverPeerID string) (*MyServersResp, bool) {
	if meshserverPeerID == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDBLocked(); err != nil {
		return nil, false
	}

	var dataJSON string
	row := s.db.QueryRow(`SELECT data_json FROM my_meshserver_servers WHERE cache_key = ?`, meshserverPeerID)
	if err := row.Scan(&dataJSON); err != nil {
		return nil, false
	}

	var resp MyServersResp
	if err := json.Unmarshal([]byte(dataJSON), &resp); err != nil {
		return nil, false
	}
	return &resp, true
}

func (s *MyServersStore) Put(meshserverPeerID string, resp *MyServersResp) error {
	if meshserverPeerID == "" || resp == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureDBLocked(); err != nil {
		return err
	}

	b, err := json.Marshal(resp)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
INSERT INTO my_meshserver_servers(cache_key, data_json)
VALUES(?, ?)
ON CONFLICT(cache_key) DO UPDATE SET data_json = excluded.data_json;`, meshserverPeerID, string(b))
	return err
}

