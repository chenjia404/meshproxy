package chat

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chenjia404/meshproxy/internal/binding"
	"github.com/chenjia404/meshproxy/internal/profile"
)

func (s *Store) ensureProfileBindingColumns() error {
	cols := []struct {
		name string
		stmt string
	}{
		{"binding_seq", `ALTER TABLE profile ADD COLUMN binding_seq INTEGER NOT NULL DEFAULT 0`},
		{"binding_record_json", `ALTER TABLE profile ADD COLUMN binding_record_json TEXT NOT NULL DEFAULT ''`},
		{"binding_eth_address", `ALTER TABLE profile ADD COLUMN binding_eth_address TEXT NOT NULL DEFAULT ''`},
		{"binding_chain_id", `ALTER TABLE profile ADD COLUMN binding_chain_id INTEGER NOT NULL DEFAULT 0`},
		{"binding_expire_at", `ALTER TABLE profile ADD COLUMN binding_expire_at INTEGER NOT NULL DEFAULT 0`},
		{"binding_updated_at", `ALTER TABLE profile ADD COLUMN binding_updated_at TEXT NOT NULL DEFAULT ''`},
	}
	return s.ensureColumns("profile", cols)
}

func (s *Store) ensurePeerBindingColumns() error {
	cols := []struct {
		name string
		stmt string
	}{
		{"binding_record_json", `ALTER TABLE peers ADD COLUMN binding_record_json TEXT NOT NULL DEFAULT ''`},
		{"binding_eth_address", `ALTER TABLE peers ADD COLUMN binding_eth_address TEXT NOT NULL DEFAULT ''`},
		{"binding_chain_id", `ALTER TABLE peers ADD COLUMN binding_chain_id INTEGER NOT NULL DEFAULT 0`},
		{"binding_seq", `ALTER TABLE peers ADD COLUMN binding_seq INTEGER NOT NULL DEFAULT 0`},
		{"binding_expire_at", `ALTER TABLE peers ADD COLUMN binding_expire_at INTEGER NOT NULL DEFAULT 0`},
		{"binding_status", `ALTER TABLE peers ADD COLUMN binding_status TEXT NOT NULL DEFAULT ''`},
		{"binding_validated_at", `ALTER TABLE peers ADD COLUMN binding_validated_at TEXT NOT NULL DEFAULT ''`},
		{"binding_updated_at", `ALTER TABLE peers ADD COLUMN binding_updated_at TEXT NOT NULL DEFAULT ''`},
		{"binding_error", `ALTER TABLE peers ADD COLUMN binding_error TEXT NOT NULL DEFAULT ''`},
	}
	return s.ensureColumns("peers", cols)
}

func (s *Store) ensureColumns(table string, cols []struct {
	name string
	stmt string
}) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		have[name] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range cols {
		if have[c.name] {
			continue
		}
		if _, err := s.db.Exec(c.stmt); err != nil {
			return fmt.Errorf("alter %s add %s: %w", table, c.name, err)
		}
	}
	return nil
}

// CurrentBindingSeq 回傳本機 profile.binding_seq（無列則 0）。
func (s *Store) CurrentBindingSeq(localPeerID string) (uint64, error) {
	var seq uint64
	err := s.db.QueryRow(`SELECT IFNULL(binding_seq, 0) FROM profile WHERE peer_id=?`, localPeerID).Scan(&seq)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return seq, nil
}

// SaveLocalBinding 寫入完整 BindingRecord 與冗餘欄位；會將 binding_seq 設為 record.Payload.Seq。
func (s *Store) SaveLocalBinding(localPeerID string, record binding.BindingRecord) (Profile, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return Profile{}, err
	}
	now := time.Now().UTC()
	bindingTS := strconv.FormatInt(now.UnixMilli(), 10)
	addr := binding.NormalizeEthAddress(record.Payload.EthAddress)
	_, err = s.db.Exec(`
		UPDATE profile SET
			binding_seq=?,
			binding_record_json=?,
			binding_eth_address=?,
			binding_chain_id=?,
			binding_expire_at=?,
			binding_updated_at=?,
			updated_at=?
		WHERE peer_id=?`,
		record.Payload.Seq,
		string(raw),
		addr,
		record.Payload.ChainID,
		record.Payload.ExpireAt,
		bindingTS,
		now.Format(time.RFC3339Nano),
		localPeerID,
	)
	if err != nil {
		return Profile{}, err
	}
	p, _, err := s.GetProfile(localPeerID)
	return p, err
}

// ClearLocalBinding 清空綁定展示欄位，保留 binding_seq。
func (s *Store) ClearLocalBinding(localPeerID string) (Profile, error) {
	now := time.Now().UTC()
	bindingTS := strconv.FormatInt(now.UnixMilli(), 10)
	_, err := s.db.Exec(`
		UPDATE profile SET
			binding_record_json='',
			binding_eth_address='',
			binding_chain_id=0,
			binding_expire_at=0,
			binding_updated_at=?,
			updated_at=?
		WHERE peer_id=?`,
		bindingTS,
		now.Format(time.RFC3339Nano),
		localPeerID,
	)
	if err != nil {
		return Profile{}, err
	}
	p, _, err := s.GetProfile(localPeerID)
	return p, err
}

// SavePeerBinding 依 seq 規則寫入對端緩存。
func (s *Store) SavePeerBinding(peerID string, record binding.BindingRecord, status, errMsg string, validatedAt time.Time) error {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return errors.New("peer_id empty")
	}
	incomingSeq := record.Payload.Seq
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	var curSeq uint64
	var curJSON string
	err = s.db.QueryRow(`
		SELECT IFNULL(binding_seq, 0), IFNULL(binding_record_json, '')
		FROM peers WHERE peer_id=?`, peerID).Scan(&curSeq, &curJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return err
	}

	switch {
	case incomingSeq < curSeq:
		_, e := s.db.Exec(`
			UPDATE peers SET binding_status=?, binding_error=?, binding_validated_at=?, updated_at=?
			WHERE peer_id=?`,
			profile.BindingStatusStale, "incoming seq older than cached", formatUnixMillisDB(validatedAt), time.Now().UTC().Format(time.RFC3339Nano), peerID)
		return e

	case incomingSeq == curSeq:
		if curJSON != "" && bytes.Equal(raw, []byte(curJSON)) {
			_, e := s.db.Exec(`
				UPDATE peers SET binding_validated_at=?, updated_at=?
				WHERE peer_id=?`,
				formatUnixMillisDB(validatedAt), time.Now().UTC().Format(time.RFC3339Nano), peerID)
			return e
		}
		_, e := s.db.Exec(`
			UPDATE peers SET binding_status=?, binding_error=?, binding_validated_at=?, updated_at=?
			WHERE peer_id=?`,
			profile.BindingStatusParseError, "same seq different payload", formatUnixMillisDB(validatedAt), time.Now().UTC().Format(time.RFC3339Nano), peerID)
		return e

	default: // incomingSeq > curSeq
		addr := binding.NormalizeEthAddress(record.Payload.EthAddress)
		now := time.Now().UTC()
		nowRFC := now.Format(time.RFC3339Nano)
		bindingPeerUpdated := strconv.FormatInt(now.UnixMilli(), 10)
		_, err := s.db.Exec(`
			UPDATE peers SET
				binding_record_json=?,
				binding_eth_address=?,
				binding_chain_id=?,
				binding_seq=?,
				binding_expire_at=?,
				binding_status=?,
				binding_validated_at=?,
				binding_updated_at=?,
				binding_error=?,
				updated_at=?
			WHERE peer_id=?`,
			string(raw),
			addr,
			record.Payload.ChainID,
			incomingSeq,
			record.Payload.ExpireAt,
			status,
			formatUnixMillisDB(validatedAt),
			bindingPeerUpdated,
			errMsg,
			nowRFC,
			peerID,
		)
		return err
	}
}

// ClearPeerBinding 清空對端綁定欄位（可選維護用）。
func (s *Store) ClearPeerBinding(peerID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		UPDATE peers SET
			binding_record_json='',
			binding_eth_address='',
			binding_chain_id=0,
			binding_seq=0,
			binding_expire_at=0,
			binding_status='',
			binding_validated_at='',
			binding_updated_at='',
			binding_error='',
			updated_at=?
		WHERE peer_id=?`, now, peerID)
	return err
}

func decodeBindingRecordJSON(s string) (*binding.BindingRecord, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var r binding.BindingRecord
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func scanProfileBinding(p *Profile, bindingJSON, bindingUpdatedAt string, bindingSeq uint64) {
	p.bindingRecord, _ = decodeBindingRecordJSON(bindingJSON)
	p.BindingUpdatedAt = parseBindingTimeToUnixMilli(bindingUpdatedAt)
	_ = bindingSeq
}

func scanContactBinding(c *Contact, bindingJSON, bindingEth, status, validatedAt, errMsg string) {
	c.bindingRecord, _ = decodeBindingRecordJSON(bindingJSON)
	c.BindingEthAddress = strings.TrimSpace(bindingEth)
	c.BindingStatus = status
	c.BindingValidatedAt = parseBindingTimeToUnixMilli(validatedAt)
	c.BindingError = errMsg
}

// formatUnixMillisDB 將時間存為毫秒數字字串（binding_*_at 欄位）。
func formatUnixMillisDB(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return strconv.FormatInt(t.UTC().UnixMilli(), 10)
}

// parseBindingTimeToUnixMilli 解析 DB 中的值：優先十進位毫秒；若失敗則嘗試 RFC3339Nano（舊資料相容）。
func parseBindingTimeToUnixMilli(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		return ms
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().UnixMilli()
	}
	return 0
}
