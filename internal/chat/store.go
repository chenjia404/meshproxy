package chat

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

type Store struct {
	db *sql.DB
}

func NewStore(path, localPeerID string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create chat db dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureProfile(localPeerID); err != nil {
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
		`CREATE TABLE IF NOT EXISTS profile (
			peer_id TEXT PRIMARY KEY,
			nickname TEXT NOT NULL,
			chat_kex_priv TEXT NOT NULL,
			chat_kex_pub TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS peers (
			peer_id TEXT PRIMARY KEY,
			nickname TEXT NOT NULL,
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
			last_transport_mode TEXT NOT NULL,
			unread_count INTEGER NOT NULL,
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
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate chat db: %w", err)
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
	_, err = s.db.Exec(`INSERT INTO profile(peer_id,nickname,chat_kex_priv,chat_kex_pub,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
		localPeerID, nickname, base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(pub), now, now)
	return err
}

func (s *Store) GetProfile(localPeerID string) (Profile, []byte, error) {
	var p Profile
	var privB64, createdAt string
	err := s.db.QueryRow(`SELECT peer_id,nickname,chat_kex_priv,chat_kex_pub,created_at FROM profile WHERE peer_id = ?`, localPeerID).
		Scan(&p.PeerID, &p.Nickname, &privB64, &p.ChatKexPub, &createdAt)
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
	return p, priv, nil
}

func (s *Store) UpdateProfileNickname(localPeerID, nickname string) (Profile, error) {
	nickname = strings.TrimSpace(nickname)
	if nickname == "" {
		nickname = "peer-" + shortPeerID(localPeerID)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`UPDATE profile SET nickname=?, updated_at=? WHERE peer_id=?`, nickname, now, localPeerID); err != nil {
		return Profile{}, err
	}
	p, _, err := s.GetProfile(localPeerID)
	return p, err
}

func (s *Store) UpsertPeer(peerID, nickname string) error {
	if peerID == "" {
		return nil
	}
	nickname = strings.TrimSpace(nickname)
	if nickname == "" {
		nickname = "peer-" + shortPeerID(peerID)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		INSERT INTO peers(peer_id,nickname,updated_at,last_seen_at)
		VALUES(?,?,?,?)
		ON CONFLICT(peer_id) DO UPDATE SET
			nickname=CASE WHEN excluded.nickname != '' THEN excluded.nickname ELSE peers.nickname END,
			last_seen_at=CASE WHEN excluded.last_seen_at != '' THEN excluded.last_seen_at ELSE peers.last_seen_at END,
			updated_at=excluded.updated_at
	`, peerID, nickname, now, now)
	return err
}

func (s *Store) ListContacts() ([]Contact, error) {
	rows, err := s.db.Query(`
		SELECT p.peer_id, p.nickname, p.blocked, p.last_seen_at, p.updated_at
		FROM peers p
		INNER JOIN conversations c ON c.peer_id = p.peer_id
		WHERE c.state = ?
		ORDER BY c.updated_at DESC, p.updated_at DESC
	`, ConversationStateActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		var blocked int
		var lastSeenAt, updatedAt string
		if err := rows.Scan(&c.PeerID, &c.Nickname, &blocked, &lastSeenAt, &updatedAt); err != nil {
			return nil, err
		}
		c.Blocked = blocked != 0
		c.LastSeenAt = parseDBTime(lastSeenAt)
		c.UpdatedAt = parseDBTime(updatedAt)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetPeer(peerID string) (Contact, error) {
	var c Contact
	var blocked int
	var lastSeenAt, updatedAt string
	err := s.db.QueryRow(`SELECT peer_id,nickname,blocked,last_seen_at,updated_at FROM peers WHERE peer_id=?`, peerID).
		Scan(&c.PeerID, &c.Nickname, &blocked, &lastSeenAt, &updatedAt)
	if err != nil {
		return Contact{}, err
	}
	c.Blocked = blocked != 0
	c.LastSeenAt = parseDBTime(lastSeenAt)
	c.UpdatedAt = parseDBTime(updatedAt)
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

func (s *Store) UpsertIncomingRequest(req Request) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		INSERT INTO requests(request_id,from_peer_id,to_peer_id,state,intro_text,nickname,remote_chat_kex_pub,conversation_id,last_transport_mode,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(request_id) DO UPDATE SET
			state=excluded.state,
			intro_text=excluded.intro_text,
			nickname=excluded.nickname,
			remote_chat_kex_pub=excluded.remote_chat_kex_pub,
			updated_at=excluded.updated_at
	`, req.RequestID, req.FromPeerID, req.ToPeerID, req.State, req.IntroText, req.Nickname, req.RemoteChatKexPub, req.ConversationID, req.LastTransportMode, now, now)
	return err
}

func (s *Store) SaveOutgoingRequest(req Request) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO requests(request_id,from_peer_id,to_peer_id,state,intro_text,nickname,remote_chat_kex_pub,conversation_id,last_transport_mode,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
	`, req.RequestID, req.FromPeerID, req.ToPeerID, req.State, req.IntroText, req.Nickname, req.RemoteChatKexPub, req.ConversationID, req.LastTransportMode, now, now)
	return err
}

func (s *Store) ListRequests(localPeerID string) ([]Request, error) {
	rows, err := s.db.Query(`SELECT request_id,from_peer_id,to_peer_id,state,intro_text,nickname,remote_chat_kex_pub,conversation_id,last_transport_mode,created_at,updated_at FROM requests WHERE from_peer_id=? OR to_peer_id=? ORDER BY created_at DESC`, localPeerID, localPeerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Request
	for rows.Next() {
		var r Request
		var createdAt, updatedAt string
		if err := rows.Scan(&r.RequestID, &r.FromPeerID, &r.ToPeerID, &r.State, &r.IntroText, &r.Nickname, &r.RemoteChatKexPub, &r.ConversationID, &r.LastTransportMode, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		r.CreatedAt = parseDBTime(createdAt)
		r.UpdatedAt = parseDBTime(updatedAt)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetRequest(requestID string) (Request, error) {
	var r Request
	var createdAt, updatedAt string
	err := s.db.QueryRow(`SELECT request_id,from_peer_id,to_peer_id,state,intro_text,nickname,remote_chat_kex_pub,conversation_id,last_transport_mode,created_at,updated_at FROM requests WHERE request_id=?`, requestID).
		Scan(&r.RequestID, &r.FromPeerID, &r.ToPeerID, &r.State, &r.IntroText, &r.Nickname, &r.RemoteChatKexPub, &r.ConversationID, &r.LastTransportMode, &createdAt, &updatedAt)
	if err != nil {
		return Request{}, err
	}
	r.CreatedAt = parseDBTime(createdAt)
	r.UpdatedAt = parseDBTime(updatedAt)
	return r, nil
}

func (s *Store) UpdateRequestState(requestID, state, conversationID string) error {
	_, err := s.db.Exec(`UPDATE requests SET state=?, conversation_id=?, updated_at=? WHERE request_id=?`,
		state, conversationID, time.Now().UTC().Format(time.RFC3339Nano), requestID)
	return err
}

func (s *Store) CreateConversation(conv Conversation, sess sessionState) (Conversation, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO conversations(conversation_id,peer_id,state,last_message_at,last_transport_mode,unread_count,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?)
	`, conv.ConversationID, conv.PeerID, conv.State, now, conv.LastTransportMode, conv.UnreadCount, now, now)
	if err != nil {
		return Conversation{}, err
	}
	_, err = s.db.Exec(`
		INSERT OR REPLACE INTO session_states(conversation_id,peer_id,send_key,recv_key,send_counter,recv_counter)
		VALUES(?,?,?,?,?,?)
	`, sess.ConversationID, sess.PeerID, base64.StdEncoding.EncodeToString(sess.SendKey), base64.StdEncoding.EncodeToString(sess.RecvKey), sess.SendCounter, sess.RecvCounter)
	if err != nil {
		return Conversation{}, err
	}
	return s.GetConversation(conv.ConversationID)
}

func (s *Store) GetConversation(id string) (Conversation, error) {
	var c Conversation
	var lastMessageAt, createdAt, updatedAt string
	err := s.db.QueryRow(`SELECT conversation_id,peer_id,state,last_message_at,last_transport_mode,unread_count,created_at,updated_at FROM conversations WHERE conversation_id=?`, id).
		Scan(&c.ConversationID, &c.PeerID, &c.State, &lastMessageAt, &c.LastTransportMode, &c.UnreadCount, &createdAt, &updatedAt)
	if err != nil {
		return Conversation{}, err
	}
	c.LastMessageAt = parseDBTime(lastMessageAt)
	c.CreatedAt = parseDBTime(createdAt)
	c.UpdatedAt = parseDBTime(updatedAt)
	return c, nil
}

func (s *Store) GetConversationByPeer(peerID string) (Conversation, error) {
	var c Conversation
	var lastMessageAt, createdAt, updatedAt string
	err := s.db.QueryRow(`SELECT conversation_id,peer_id,state,last_message_at,last_transport_mode,unread_count,created_at,updated_at FROM conversations WHERE peer_id=?`, peerID).
		Scan(&c.ConversationID, &c.PeerID, &c.State, &lastMessageAt, &c.LastTransportMode, &c.UnreadCount, &createdAt, &updatedAt)
	if err != nil {
		return Conversation{}, err
	}
	c.LastMessageAt = parseDBTime(lastMessageAt)
	c.CreatedAt = parseDBTime(createdAt)
	c.UpdatedAt = parseDBTime(updatedAt)
	return c, nil
}

func (s *Store) ListConversations() ([]Conversation, error) {
	rows, err := s.db.Query(`SELECT conversation_id,peer_id,state,last_message_at,last_transport_mode,unread_count,created_at,updated_at FROM conversations ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		var lastMessageAt, createdAt, updatedAt string
		if err := rows.Scan(&c.ConversationID, &c.PeerID, &c.State, &lastMessageAt, &c.LastTransportMode, &c.UnreadCount, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		c.LastMessageAt = parseDBTime(lastMessageAt)
		c.CreatedAt = parseDBTime(createdAt)
		c.UpdatedAt = parseDBTime(updatedAt)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) AddMessage(msg Message, ciphertext []byte) (Message, error) {
	deliveredAt := ""
	if !msg.DeliveredAt.IsZero() {
		deliveredAt = msg.DeliveredAt.UTC().Format(time.RFC3339Nano)
	}
	createdAt := msg.CreatedAt.UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO messages(msg_id,conversation_id,sender_peer_id,receiver_peer_id,direction,msg_type,plaintext,ciphertext_blob,transport_mode,state,counter,created_at,delivered_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, msg.MsgID, msg.ConversationID, msg.SenderPeerID, msg.ReceiverPeerID, msg.Direction, msg.MsgType, msg.Plaintext, ciphertext, msg.TransportMode, msg.State, msg.Counter, createdAt, deliveredAt)
	if err != nil {
		return Message{}, err
	}
	if _, err := s.db.Exec(`UPDATE conversations SET last_message_at=?, updated_at=?, last_transport_mode=? WHERE conversation_id=?`,
		createdAt, createdAt, msg.TransportMode, msg.ConversationID); err != nil {
		return Message{}, err
	}
	return msg, nil
}

func (s *Store) ListMessages(conversationID string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT msg_id,conversation_id,sender_peer_id,receiver_peer_id,direction,msg_type,plaintext,transport_mode,state,counter,created_at,delivered_at FROM messages WHERE conversation_id=? ORDER BY created_at ASC`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var createdAt, deliveredAt string
		if err := rows.Scan(&m.MsgID, &m.ConversationID, &m.SenderPeerID, &m.ReceiverPeerID, &m.Direction, &m.MsgType, &m.Plaintext, &m.TransportMode, &m.State, &m.Counter, &createdAt, &deliveredAt); err != nil {
			return nil, err
		}
		m.CreatedAt = parseDBTime(createdAt)
		m.DeliveredAt = parseDBTime(deliveredAt)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) MarkMessageDelivered(msgID string, deliveredAt time.Time) error {
	ts := deliveredAt.UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`UPDATE messages SET state=?, delivered_at=? WHERE msg_id=?`, MessageStateDeliveredRemote, ts, msgID)
	return err
}

func (s *Store) GetSessionState(conversationID string) (sessionState, error) {
	var sess sessionState
	var sendKeyB64, recvKeyB64 string
	err := s.db.QueryRow(`SELECT conversation_id,peer_id,send_key,recv_key,send_counter,recv_counter FROM session_states WHERE conversation_id=?`, conversationID).
		Scan(&sess.ConversationID, &sess.PeerID, &sendKeyB64, &recvKeyB64, &sess.SendCounter, &sess.RecvCounter)
	if err != nil {
		return sessionState{}, err
	}
	sess.SendKey, err = base64.StdEncoding.DecodeString(sendKeyB64)
	if err != nil {
		return sessionState{}, err
	}
	sess.RecvKey, err = base64.StdEncoding.DecodeString(recvKeyB64)
	if err != nil {
		return sessionState{}, err
	}
	return sess, nil
}

func (s *Store) UpdateSendCounter(conversationID string, counter uint64) error {
	_, err := s.db.Exec(`UPDATE session_states SET send_counter=? WHERE conversation_id=?`, counter, conversationID)
	return err
}

func (s *Store) UpdateRecvCounter(conversationID string, counter uint64) error {
	_, err := s.db.Exec(`UPDATE session_states SET recv_counter=? WHERE conversation_id=?`, counter, conversationID)
	return err
}

func parseDBTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

func shortPeerID(v string) string {
	if len(v) <= 8 {
		return v
	}
	return v[:8]
}

func deriveConversationID(a, b, requestID string) string {
	parts := []string{a, b}
	sort.Strings(parts)
	return strings.Join(parts, ":") + ":" + requestID
}

func deriveSessionState(conversationID, localPeerID, remotePeerID string, localPriv []byte, remotePubB64 string) (sessionState, error) {
	remotePub, err := base64.StdEncoding.DecodeString(remotePubB64)
	if err != nil {
		return sessionState{}, err
	}
	shared, err := protocol.X25519SharedSecret(localPriv, remotePub)
	if err != nil {
		return sessionState{}, err
	}
	k1, k2, err := protocol.DeriveHopKeys(shared)
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
