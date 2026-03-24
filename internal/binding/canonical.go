package binding

import (
	"fmt"
	"strconv"
	"strings"
)

// CanonicalMessage 依設計文檔固定欄位順序與換行符產生待簽字串。
func CanonicalMessage(p BindingPayload) string {
	var b strings.Builder
	b.WriteString("MeshChat Binding v1\n")
	b.WriteString("action: ")
	b.WriteString(p.Action)
	b.WriteString("\npeer_id: ")
	b.WriteString(strings.TrimSpace(p.PeerID))
	b.WriteString("\neth_address: ")
	b.WriteString(NormalizeEthAddress(p.EthAddress))
	b.WriteString("\nchain_id: ")
	b.WriteString(strconv.FormatUint(p.ChainID, 10))
	b.WriteString("\ndomain: ")
	b.WriteString(p.Domain)
	b.WriteString("\nnonce: ")
	b.WriteString(strings.TrimSpace(p.Nonce))
	b.WriteString("\nseq: ")
	b.WriteString(strconv.FormatUint(p.Seq, 10))
	b.WriteString("\nissued_at: ")
	b.WriteString(strconv.FormatInt(p.IssuedAt, 10))
	b.WriteString("\nexpire_at: ")
	b.WriteString(strconv.FormatInt(p.ExpireAt, 10))
	b.WriteString("\n")
	return b.String()
}

// PayloadMatchesCanonical 確認 eth_address 已規範化（canonical 內一律小寫）。
func PayloadMatchesCanonical(p BindingPayload) error {
	if NormalizeEthAddress(p.EthAddress) != p.EthAddress {
		return fmt.Errorf("binding: eth_address must be normalized lowercase")
	}
	return nil
}
