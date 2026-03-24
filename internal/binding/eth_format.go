package binding

import (
	"encoding/hex"
	"strings"
)

// ValidateEthSignaturePresent 僅檢查非空與常見 hex 格式（不做恢復地址）。
func ValidateEthSignaturePresent(ethSigHex string) error {
	s := strings.TrimSpace(ethSigHex)
	if s == "" {
		return ErrEthSigEmpty
	}
	if !strings.HasPrefix(s, "0x") {
		return ErrEthSigFormat
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		return ErrEthSigFormat
	}
	// EIP-191 簽名通常 65 bytes；允許未來格式略放寬
	if len(raw) < 64 || len(raw) > 256 {
		return ErrEthSigFormat
	}
	return nil
}

// ErrEthSigEmpty 表示未提供 ETH 簽名。
var ErrEthSigEmpty = errEthSig("eth signature empty")

// ErrEthSigFormat 表示 ETH 簽名格式不合法。
var ErrEthSigFormat = errEthSig("eth signature format invalid")

type errEthSig string

func (e errEthSig) Error() string { return string(e) }
