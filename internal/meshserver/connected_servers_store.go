package meshserver

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

// ConnectedMeshServersStore persists "connected meshservers" (i.e. meshserver connections)
// to a local SQLite DB.
//
// Key: connection name (default == peer id).
type ConnectedMeshServersStoreEntry struct {
	Name        string `json:"name"`
	PeerID      string `json:"peer_id"`
	ClientAgent string `json:"client_agent"`
	ProtocolID  string `json:"protocol_id"`
}

type ConnectedMeshServersStore struct {
	mu      sync.Mutex
	path    string
	db      *sql.DB
	openErr error
	ready   bool
}

func NewConnectedMeshServersStore(path string) *ConnectedMeshServersStore {
	return &ConnectedMeshServersStore{
		path: path,
	}
}

func (s *ConnectedMeshServersStore) ensureReadyLocked() error {
	if s.ready {
		return nil
	}
	if s.openErr != nil {
		return s.openErr
	}
	if s.db == nil {
		dir := filepath.Dir(s.path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			s.openErr = err
			return err
		}

		db, err := sql.Open("sqlite", s.path)
		if err != nil {
			s.openErr = err
			return err
		}
		// SQLite works much more reliably when all writes are serialized through one connection.
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		if _, err := db.Exec(`PRAGMA busy_timeout = 15000`); err != nil {
			_ = db.Close()
			s.openErr = err
			return err
		}
		s.db = db
	}

	if s.db == nil {
		s.ready = true
		return nil
	}

	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS connected_meshservers (
  name TEXT PRIMARY KEY,
  peer_id TEXT NOT NULL,
  client_agent TEXT,
  protocol_id TEXT
);`); err != nil {
		s.openErr = err
		return err
	}
	s.ready = true
	return nil
}

func (s *ConnectedMeshServersStore) Put(info ConnectionInfo) error {
	// Only persist stable identity fields. Session/auth info is user-specific and will be
	// refreshed from the live connection when listing.
	name := info.Name
	if name == "" {
		name = info.PeerID
	}
	if name == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureReadyLocked(); err != nil {
		return err
	}
	if s.db == nil {
		return nil
	}

	_, err := s.db.Exec(`
INSERT INTO connected_meshservers(name, peer_id, client_agent, protocol_id)
VALUES(?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
  peer_id = excluded.peer_id,
  client_agent = excluded.client_agent,
  protocol_id = excluded.protocol_id;`,
		name, info.PeerID, info.ClientAgent, info.ProtocolID,
	)
	return err
}

func (s *ConnectedMeshServersStore) ListEntries() []ConnectedMeshServersStoreEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureReadyLocked(); err != nil {
		return nil
	}
	if s.db == nil {
		return nil
	}

	rows, err := s.db.Query(`
SELECT name, peer_id, client_agent, protocol_id
FROM connected_meshservers
ORDER BY name ASC;`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]ConnectedMeshServersStoreEntry, 0)
	for rows.Next() {
		var e ConnectedMeshServersStoreEntry
		if err := rows.Scan(&e.Name, &e.PeerID, &e.ClientAgent, &e.ProtocolID); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out
}

