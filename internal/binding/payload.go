package binding

import (
	"errors"
	"strings"
)

const (
	Version1           = 1
	ActionBindEthAddr  = "bind_eth_address"
	DomainMeshChat          = "meshchat"
	MessageTypeBindingSync  = "binding_sync"
)

// BindingPayload 為待簽與持久化的綁定內容（JSON 與 canonical 驗簽共用）。
type BindingPayload struct {
	Version    int    `json:"version"`
	Action     string `json:"action"`
	PeerID     string `json:"peer_id"`
	EthAddress string `json:"eth_address"`
	ChainID    uint64 `json:"chain_id"`
	Domain     string `json:"domain"`
	Nonce      string `json:"nonce"`
	Seq        uint64 `json:"seq"`
	IssuedAt   int64  `json:"issued_at"`
	ExpireAt   int64  `json:"expire_at"`
}

// NormalizeEthAddress 入庫前統一為小寫並去除空白。
func NormalizeEthAddress(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ValidatePayloadFields 檢查固定欄位與基本約束（不含簽名）。
func ValidatePayloadFields(p BindingPayload) error {
	if p.Version != Version1 {
		return errors.New("binding: invalid version")
	}
	if p.Action != ActionBindEthAddr {
		return errors.New("binding: invalid action")
	}
	if p.Domain != DomainMeshChat {
		return errors.New("binding: invalid domain")
	}
	addr := NormalizeEthAddress(p.EthAddress)
	if addr == "" || !strings.HasPrefix(addr, "0x") || len(addr) != 42 {
		return errors.New("binding: invalid eth_address")
	}
	if p.ChainID == 0 {
		return errors.New("binding: chain_id must be > 0")
	}
	if strings.TrimSpace(p.Nonce) == "" {
		return errors.New("binding: nonce required")
	}
	if p.Seq == 0 {
		return errors.New("binding: seq must be > 0")
	}
	if p.IssuedAt <= 0 || p.ExpireAt <= 0 {
		return errors.New("binding: invalid issued_at/expire_at")
	}
	if p.ExpireAt <= p.IssuedAt {
		return errors.New("binding: expire_at must be after issued_at")
	}
	return nil
}
