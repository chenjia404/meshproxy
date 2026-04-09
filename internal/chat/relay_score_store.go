package chat

import (
	"database/sql"
	"fmt"
	"time"
)

// 单次连接因「同 IP」可累加分数的上限（间隔分钟数与加分一致，最多 60）。
const relayNodeMaxScorePerConnect = 60

// ApplyRelayNodeConnectScore 记录中继连接：若本次观测公网 IP 与库中一致，
// 则按距上次计分的整分钟数加分（单次最多 relayNodeMaxScorePerConnect）；若 IP 变更则清零分数并更新 IP 与时间戳。
func (s *Store) ApplyRelayNodeConnectScore(peerID, realIP string, now time.Time) (added int, err error) {
	if s == nil || s.db == nil || peerID == "" || realIP == "" {
		return 0, nil
	}
	now = now.UTC()

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}

	var storedIP string
	var score int
	var lastAtStr string
	qErr := tx.QueryRow(`SELECT real_ip, score, last_score_at FROM relay_node_scores WHERE peer_id = ?`, peerID).Scan(&storedIP, &score, &lastAtStr)

	if qErr == sql.ErrNoRows {
		_, err = tx.Exec(`INSERT INTO relay_node_scores(peer_id, real_ip, score, last_score_at) VALUES(?,?,?,?)`,
			peerID, realIP, 0, now.Format(time.RFC3339Nano))
		if err != nil {
			_ = tx.Rollback()
			return 0, err
		}
		if err = tx.Commit(); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if qErr != nil {
		_ = tx.Rollback()
		return 0, qErr
	}

	if storedIP != realIP {
		_, err = tx.Exec(`UPDATE relay_node_scores SET real_ip=?, score=0, last_score_at=? WHERE peer_id=?`,
			realIP, now.Format(time.RFC3339Nano), peerID)
		if err != nil {
			_ = tx.Rollback()
			return 0, err
		}
		if err = tx.Commit(); err != nil {
			return 0, err
		}
		return 0, nil
	}

	lastAt, perr := time.Parse(time.RFC3339Nano, lastAtStr)
	if perr != nil {
		lastAt, perr = time.Parse(time.RFC3339, lastAtStr)
	}
	if perr != nil {
		lastAt = now
	}
	minutes := int(now.Sub(lastAt) / time.Minute)
	if minutes < 0 {
		minutes = 0
	}
	add := minutes
	if add > relayNodeMaxScorePerConnect {
		add = relayNodeMaxScorePerConnect
	}
	newScore := score + add
	_, err = tx.Exec(`UPDATE relay_node_scores SET score=?, last_score_at=? WHERE peer_id=?`,
		newScore, now.Format(time.RFC3339Nano), peerID)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return add, nil
}

// RelayNodeScoresMap 返回已知中继节点分数（用于候选排序）。
func (s *Store) RelayNodeScoresMap() (map[string]int, error) {
	if s == nil || s.db == nil {
		return map[string]int{}, nil
	}
	rows, err := s.db.Query(`SELECT peer_id, score FROM relay_node_scores`)
	if err != nil {
		return nil, fmt.Errorf("relay_node_scores: %w", err)
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var pid string
		var sc int
		if err := rows.Scan(&pid, &sc); err != nil {
			return nil, err
		}
		out[pid] = sc
	}
	return out, rows.Err()
}
