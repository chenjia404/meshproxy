package meshserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	crypto "github.com/libp2p/go-libp2p/core/crypto"
)

// HTTPAccessTokenResult 對應 meshserver POST /v1/auth/verify 成功回應（見 meshserver docs/api.md）。
type HTTPAccessTokenResult struct {
	AccessToken string          `json:"access_token"`
	TokenType   string          `json:"token_type"`
	ExpiresIn   int             `json:"expires_in"`
	ExpiresAt   string          `json:"expires_at"`
	User        json.RawMessage `json:"user"`
}

type challengeHTTPResp struct {
	ProtocolID  string `json:"protocol_id"`
	NodePeerID  string `json:"node_peer_id"`
	Nonce       string `json:"nonce"`
	IssuedAtMs  uint64 `json:"issued_at_ms"`
	ExpiresAtMs uint64 `json:"expires_at_ms"`
}

type verifyHTTPReq struct {
	ClientPeerID string `json:"client_peer_id"`
	NodePeerID   string `json:"node_peer_id"`
	Nonce        string `json:"nonce"`
	IssuedAtMs   uint64 `json:"issued_at_ms"`
	ExpiresAtMs  uint64 `json:"expires_at_ms"`
	Signature    string `json:"signature"`
	PublicKey    string `json:"public_key"`
}

// FetchHTTPAccessToken 對指定 meshserver 的 HTTP 基底 URL 執行 challenge → 簽名 → verify，取得 JWT access_token。
// baseURL 例如 https://mesh.example.com（無尾隨斜線亦可）；路徑為 /v1/auth/challenge 與 /v1/auth/verify。
// protocolIDOverride 非空時覆寫 challenge 回傳的 protocol_id（一般用於與伺服器設定一致）。
func FetchHTTPAccessToken(ctx context.Context, baseURL string, clientPeerID string, priv crypto.PrivKey, protocolIDOverride string) (*HTTPAccessTokenResult, error) {
	if priv == nil {
		return nil, fmt.Errorf("private key is nil")
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("base_url is empty")
	}
	clientPeerID = strings.TrimSpace(clientPeerID)
	if clientPeerID == "" {
		return nil, fmt.Errorf("client_peer_id is empty")
	}

	client := &http.Client{Timeout: 60 * time.Second}

	challengeURL := baseURL + "/v1/auth/challenge"
	chBody := []byte(fmt.Sprintf(`{"client_peer_id":%q}`, clientPeerID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, challengeURL, bytes.NewReader(chBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("challenge request: %w", err)
	}
	defer resp.Body.Close()
	chRaw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("challenge HTTP %d: %s", resp.StatusCode, trimErrBody(chRaw))
	}
	var ch challengeHTTPResp
	if err := json.Unmarshal(chRaw, &ch); err != nil {
		return nil, fmt.Errorf("decode challenge: %w", err)
	}
	nonceBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ch.Nonce))
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}
	protocolID := strings.TrimSpace(protocolIDOverride)
	if protocolID == "" {
		protocolID = strings.TrimSpace(ch.ProtocolID)
	}
	if protocolID == "" {
		return nil, fmt.Errorf("protocol_id is empty")
	}
	payload := BuildChallengePayload(protocolID, clientPeerID, strings.TrimSpace(ch.NodePeerID), nonceBytes, ch.IssuedAtMs, ch.ExpiresAtMs)
	sig, err := priv.Sign(payload)
	if err != nil {
		return nil, fmt.Errorf("sign challenge: %w", err)
	}
	pub, err := crypto.MarshalPublicKey(priv.GetPublic())
	if err != nil {
		return nil, fmt.Errorf("marshal public key: %w", err)
	}
	vreq := verifyHTTPReq{
		ClientPeerID: clientPeerID,
		NodePeerID:   strings.TrimSpace(ch.NodePeerID),
		Nonce:        strings.TrimSpace(ch.Nonce),
		IssuedAtMs:   ch.IssuedAtMs,
		ExpiresAtMs:  ch.ExpiresAtMs,
		Signature:    base64.StdEncoding.EncodeToString(sig),
		PublicKey:    base64.StdEncoding.EncodeToString(pub),
	}
	verifyBody, err := json.Marshal(vreq)
	if err != nil {
		return nil, err
	}
	verifyURL := baseURL + "/v1/auth/verify"
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, verifyURL, bytes.NewReader(verifyBody))
	if err != nil {
		return nil, err
	}
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(req2)
	if err != nil {
		return nil, fmt.Errorf("verify request: %w", err)
	}
	defer resp2.Body.Close()
	verifyRaw, err := io.ReadAll(io.LimitReader(resp2.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp2.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("verify HTTP %d: %s", resp2.StatusCode, trimErrBody(verifyRaw))
	}
	var out HTTPAccessTokenResult
	if err := json.Unmarshal(verifyRaw, &out); err != nil {
		return nil, fmt.Errorf("decode verify: %w", err)
	}
	if strings.TrimSpace(out.AccessToken) == "" {
		return nil, fmt.Errorf("verify response missing access_token")
	}
	return &out, nil
}

func trimErrBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 512 {
		return s[:512] + "..."
	}
	return s
}
