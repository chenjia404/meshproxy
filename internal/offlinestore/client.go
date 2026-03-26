package offlinestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	host "github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// DefaultRPCTimeout 文檔 10.2：所有請求必須帶 timeout
const DefaultRPCTimeout = 10 * time.Second

// ErrResponseNotOK 響應業務層 ok 為 false（文檔 16：必須校驗 ok）
var ErrResponseNotOK = errors.New("offlinestore: response ok is false")

func withDefaultTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

// Libp2pStoreClient 基於 libp2p stream 的離線 store RPC 客戶端（無 HTTP）
type Libp2pStoreClient struct {
	Host  host.Host
	Codec StreamCodec
}

// NewLibp2pStoreClient 建立客戶端；Host 用於 NewStream
func NewLibp2pStoreClient(h host.Host) *Libp2pStoreClient {
	if h == nil {
		return nil
	}
	return &Libp2pStoreClient{Host: h, Codec: StreamCodec{}}
}

// rpcBodyHasOKKey 判斷 body 是否為業務層 Store/Fetch/Ack 響應（JSON 含 "ok" 字段）。
// RPC 層錯誤形狀 RPCErrorBody 不含 "ok"，避免誤解為業務結構。
func rpcBodyHasOKKey(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return false
	}
	_, ok := m["ok"]
	return ok
}

func validateRPCRequestID(want string, resp RPCResponse) error {
	if strings.TrimSpace(resp.RequestID) == "" {
		return errors.New("offlinestore: response missing request_id")
	}
	if resp.RequestID != want {
		return fmt.Errorf("%w: want %q got %q", ErrRPCRequestIDMismatch, want, resp.RequestID)
	}
	return nil
}

// rpcLayerErrorFromBody 解析 RPC 層 body（error_code / error_message）。
func rpcLayerErrorFromBody(body []byte) error {
	var eb RPCErrorBody
	if err := json.Unmarshal(body, &eb); err != nil {
		return fmt.Errorf("%w: parse rpc error body: %w", ErrRPCLayer, err)
	}
	if eb.ErrorCode == "" {
		return fmt.Errorf("%w: missing error_code", ErrRPCLayer)
	}
	if eb.ErrorMessage != "" {
		return fmt.Errorf("%w: %s: %s", ErrRPCLayer, eb.ErrorCode, eb.ErrorMessage)
	}
	return fmt.Errorf("%w: %s", ErrRPCLayer, eb.ErrorCode)
}

func (c *Libp2pStoreClient) rpcCall(ctx context.Context, storePeer peer.ID, method string, body any) (reqID string, resp RPCResponse, err error) {
	if c == nil || c.Host == nil {
		return "", RPCResponse{}, errors.New("offlinestore: nil client or host")
	}
	ctx, cancel := withDefaultTimeout(ctx, DefaultRPCTimeout)
	defer cancel()

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return "", RPCResponse{}, fmt.Errorf("offlinestore: marshal rpc body: %w", err)
	}
	reqID = uuid.New().String()
	req := RPCRequest{
		RequestID: reqID,
		Method:    method,
		Body:      bodyJSON,
	}
	stream, err := c.Host.NewStream(ctx, storePeer, ProtocolRPC)
	if err != nil {
		return "", RPCResponse{}, fmt.Errorf("offlinestore: new stream rpc: %w", err)
	}
	defer stream.Close()

	if err := c.Codec.WriteFrame(stream, &req); err != nil {
		return "", RPCResponse{}, err
	}
	var rpcResp RPCResponse
	if err := c.Codec.ReadFrameJSON(stream, &rpcResp); err != nil {
		return "", RPCResponse{}, err
	}
	if err := validateRPCRequestID(reqID, rpcResp); err != nil {
		return "", RPCResponse{}, err
	}
	return reqID, rpcResp, nil
}

// handleRPCEnvelope 在已讀取 RPCResponse 後：區分 RPC 層錯誤與業務層失敗；業務成功時 rpcOK 為 true。
func handleRPCEnvelope(rpcResp RPCResponse) (rpcOK bool, businessBody []byte, err error) {
	if rpcResp.OK {
		if !rpcBodyHasOKKey(rpcResp.Body) {
			return false, nil, fmt.Errorf("offlinestore: rpc ok=true but body missing business \"ok\" field")
		}
		return true, rpcResp.Body, nil
	}
	// !rpcResp.OK
	if rpcBodyHasOKKey(rpcResp.Body) {
		return false, rpcResp.Body, nil
	}
	if len(rpcResp.Body) == 0 {
		msg := rpcResp.Error
		if msg != "" {
			return false, nil, fmt.Errorf("%w: empty body: %s", ErrRPCLayer, msg)
		}
		return false, nil, fmt.Errorf("%w: empty body", ErrRPCLayer)
	}
	return false, nil, rpcLayerErrorFromBody(rpcResp.Body)
}

// StoreMessage 協議 /meshchat/offline-store/rpc/1.0.0 method offline.store
// message 為完整請求體中的 message（envelope JSON）；冪等：重複調用由 store 返回 duplicate，客戶端無本地副作用
func (c *Libp2pStoreClient) StoreMessage(ctx context.Context, storePeer peer.ID, req *StoreMessageRequest) (*StoreMessageResponse, error) {
	if c == nil || c.Host == nil {
		return nil, errors.New("offlinestore: nil client or host")
	}
	if req == nil {
		return nil, errors.New("offlinestore: nil request")
	}
	_, rpcResp, err := c.rpcCall(ctx, storePeer, MethodOfflineStore, req)
	if err != nil {
		return nil, err
	}
	ok, body, err := handleRPCEnvelope(rpcResp)
	if err != nil {
		return nil, err
	}
	var inner StoreMessageResponse
	if err := json.Unmarshal(body, &inner); err != nil {
		return nil, fmt.Errorf("offlinestore: unmarshal store response: %w", err)
	}
	if ok {
		if !inner.OK {
			return nil, fmt.Errorf("offlinestore: rpc ok=true but store ok=false")
		}
		return &inner, nil
	}
	if !inner.OK {
		return &inner, fmt.Errorf("%w: %s", ErrResponseNotOK, inner.ErrorCode)
	}
	return &inner, nil
}

// FetchMessages 協議 /meshchat/offline-store/rpc/1.0.0 method offline.fetch
// recipientID / afterSeq / limit 對應 8.3；limit<=0 時使用 100（文檔示例）
func (c *Libp2pStoreClient) FetchMessages(ctx context.Context, storePeer peer.ID, recipientID string, afterSeq int64, limit int) (*FetchMessagesResponse, error) {
	if c == nil || c.Host == nil {
		return nil, errors.New("offlinestore: nil client or host")
	}
	if recipientID == "" {
		return nil, errors.New("offlinestore: empty recipient_id")
	}
	if limit <= 0 {
		limit = 100
	}
	req := &FetchMessagesRequest{
		Version:     1,
		RecipientID: recipientID,
		AfterSeq:    afterSeq,
		Limit:       limit,
	}
	_, rpcResp, err := c.rpcCall(ctx, storePeer, MethodOfflineFetch, req)
	if err != nil {
		return nil, err
	}
	ok, body, err := handleRPCEnvelope(rpcResp)
	if err != nil {
		return nil, err
	}
	var inner FetchMessagesResponse
	if err := json.Unmarshal(body, &inner); err != nil {
		return nil, fmt.Errorf("offlinestore: unmarshal fetch response: %w", err)
	}
	if ok {
		if !inner.OK {
			return nil, fmt.Errorf("offlinestore: rpc ok=true but fetch ok=false")
		}
		return &inner, nil
	}
	if !inner.OK {
		return &inner, fmt.Errorf("%w: %s", ErrResponseNotOK, inner.ErrorCode)
	}
	return &inner, nil
}

// AckMessages 協議 /meshchat/offline-store/rpc/1.0.0 method offline.ack
// 冪等：重複發送相同 ack_seq 由 store 保證冪等；客戶端可安全重試
func (c *Libp2pStoreClient) AckMessages(ctx context.Context, storePeer peer.ID, req *AckMessagesRequest) (*AckMessagesResponse, error) {
	if c == nil || c.Host == nil {
		return nil, errors.New("offlinestore: nil client or host")
	}
	if req == nil {
		return nil, errors.New("offlinestore: nil request")
	}
	_, rpcResp, err := c.rpcCall(ctx, storePeer, MethodOfflineAck, req)
	if err != nil {
		return nil, err
	}
	ok, body, err := handleRPCEnvelope(rpcResp)
	if err != nil {
		return nil, err
	}
	var inner AckMessagesResponse
	if err := json.Unmarshal(body, &inner); err != nil {
		return nil, fmt.Errorf("offlinestore: unmarshal ack response: %w", err)
	}
	if ok {
		if !inner.OK {
			return nil, fmt.Errorf("offlinestore: rpc ok=true but ack ok=false")
		}
		return &inner, nil
	}
	if !inner.OK {
		code := inner.ErrorCode
		if code == "" {
			code = "unknown"
		}
		return &inner, fmt.Errorf("%w: %s", ErrResponseNotOK, code)
	}
	return &inner, nil
}
