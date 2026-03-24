package offlinestore

import (
	"context"
	"errors"
	"fmt"
	"time"

	host "github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

// DefaultRPCTimeout 文檔 10.2：所有請求必須帶 timeout
const DefaultRPCTimeout = 10 * time.Second

// ErrResponseNotOK 響應 ok 為 false（文檔 16：必須校驗 ok）
var ErrResponseNotOK = errors.New("offlinestore: response ok is false")

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

func withDefaultTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

// StoreMessage 協議 /chat/offline/store/1.0.0（文檔 7）
// message 為完整請求體中的 message（envelope JSON）；冪等：重複調用由 store 返回 duplicate，客戶端無本地副作用
func (c *Libp2pStoreClient) StoreMessage(ctx context.Context, storePeer peer.ID, req *StoreMessageRequest) (*StoreMessageResponse, error) {
	if c == nil || c.Host == nil {
		return nil, errors.New("offlinestore: nil client or host")
	}
	if req == nil {
		return nil, errors.New("offlinestore: nil request")
	}
	ctx, cancel := withDefaultTimeout(ctx, DefaultRPCTimeout)
	defer cancel()

	stream, err := c.Host.NewStream(ctx, storePeer, ProtocolStore)
	if err != nil {
		return nil, fmt.Errorf("offlinestore: new stream store: %w", err)
	}
	defer stream.Close()

	if err := c.Codec.WriteFrame(stream, req); err != nil {
		return nil, err
	}

	var resp StoreMessageResponse
	if err := c.Codec.ReadFrameJSON(stream, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return &resp, fmt.Errorf("%w: %s", ErrResponseNotOK, resp.ErrorCode)
	}
	return &resp, nil
}

// FetchMessages 協議 /chat/offline/fetch/1.0.0（文檔 8）
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
	ctx, cancel := withDefaultTimeout(ctx, DefaultRPCTimeout)
	defer cancel()

	stream, err := c.Host.NewStream(ctx, storePeer, ProtocolFetch)
	if err != nil {
		return nil, fmt.Errorf("offlinestore: new stream fetch: %w", err)
	}
	defer stream.Close()

	if err := c.Codec.WriteFrame(stream, req); err != nil {
		return nil, err
	}

	var resp FetchMessagesResponse
	if err := c.Codec.ReadFrameJSON(stream, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return &resp, fmt.Errorf("%w: %s", ErrResponseNotOK, resp.ErrorCode)
	}
	return &resp, nil
}

// AckMessages 協議 /chat/offline/ack/1.0.0（文檔 9）
// 冪等：重複發送相同 ack_seq 由 store 保證冪等；客戶端可安全重試
func (c *Libp2pStoreClient) AckMessages(ctx context.Context, storePeer peer.ID, req *AckMessagesRequest) (*AckMessagesResponse, error) {
	if c == nil || c.Host == nil {
		return nil, errors.New("offlinestore: nil client or host")
	}
	if req == nil {
		return nil, errors.New("offlinestore: nil request")
	}
	ctx, cancel := withDefaultTimeout(ctx, DefaultRPCTimeout)
	defer cancel()

	stream, err := c.Host.NewStream(ctx, storePeer, ProtocolAck)
	if err != nil {
		return nil, fmt.Errorf("offlinestore: new stream ack: %w", err)
	}
	defer stream.Close()

	if err := c.Codec.WriteFrame(stream, req); err != nil {
		return nil, err
	}

	var resp AckMessagesResponse
	if err := c.Codec.ReadFrameJSON(stream, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return &resp, fmt.Errorf("%w", ErrResponseNotOK)
	}
	return &resp, nil
}
