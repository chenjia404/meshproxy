package storenode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/chenjia404/meshproxy/internal/offlinestore"
	host "github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// ProtocolRPC 与 meshchat-store-node 及文档一致。
const ProtocolRPC protocol.ID = "/meshchat/public-channel/rpc/1.0.0"

// MethodPublicChannelPush store-node 方法名。
const MethodPublicChannelPush = "public_channel.push"

const defaultRPCTimeout = 30 * time.Second

// Client 向 store-node 推送公共频道数据（单流多方法 RPC，帧格式同 offline-store）。
type Client struct {
	Host  host.Host
	Codec offlinestore.StreamCodec
}

// NewClient 创建客户端。
func NewClient(h host.Host) *Client {
	if h == nil {
		return nil
	}
	return &Client{Host: h, Codec: offlinestore.StreamCodec{}}
}

func (c *Client) rpcCall(ctx context.Context, storePeer peer.ID, method string, body any) (offlinestore.RPCResponse, error) {
	if c == nil || c.Host == nil {
		return offlinestore.RPCResponse{}, errors.New("storenode: nil client or host")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultRPCTimeout)
		defer cancel()
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return offlinestore.RPCResponse{}, fmt.Errorf("storenode: marshal body: %w", err)
	}
	reqID := uuid.New().String()
	req := offlinestore.RPCRequest{
		RequestID: reqID,
		Method:    method,
		Body:      bodyJSON,
	}
	stream, err := c.Host.NewStream(ctx, storePeer, ProtocolRPC)
	if err != nil {
		return offlinestore.RPCResponse{}, fmt.Errorf("storenode: new stream: %w", err)
	}
	defer stream.Close()

	if err := c.Codec.WriteFrame(stream, &req); err != nil {
		return offlinestore.RPCResponse{}, err
	}
	var rpcResp offlinestore.RPCResponse
	if err := c.Codec.ReadFrameJSON(stream, &rpcResp); err != nil {
		return offlinestore.RPCResponse{}, err
	}
	if strings.TrimSpace(rpcResp.RequestID) == "" || rpcResp.RequestID != reqID {
		return offlinestore.RPCResponse{}, offlinestore.ErrRPCRequestIDMismatch
	}
	return rpcResp, nil
}

// Push 调用 public_channel.push。
func (c *Client) Push(ctx context.Context, storePeer peer.ID, req *PushRequest) (*PushResponse, error) {
	if req == nil || req.Profile == nil || req.Head == nil {
		return nil, errors.New("storenode: profile and head are required")
	}
	rpcResp, err := c.rpcCall(ctx, storePeer, MethodPublicChannelPush, req)
	if err != nil {
		return nil, err
	}
	if !rpcResp.OK {
		if len(rpcResp.Body) > 0 {
			var eb offlinestore.RPCErrorBody
			if json.Unmarshal(rpcResp.Body, &eb) == nil && eb.ErrorCode != "" {
				return nil, fmt.Errorf("storenode: rpc layer: %s: %s", eb.ErrorCode, eb.ErrorMessage)
			}
		}
		if rpcResp.Error != "" {
			return nil, fmt.Errorf("storenode: rpc: %s", rpcResp.Error)
		}
		return nil, errors.New("storenode: rpc ok=false")
	}
	var inner PushResponse
	if err := json.Unmarshal(rpcResp.Body, &inner); err != nil {
		return nil, fmt.Errorf("storenode: unmarshal push response: %w", err)
	}
	if !inner.OK {
		return &inner, fmt.Errorf("storenode: push ok=false: %s %s", inner.ErrorCode, inner.ErrorMessage)
	}
	return &inner, nil
}
