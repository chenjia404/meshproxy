package publicchannel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	network "github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/chenjia404/meshproxy/internal/p2p"
	"github.com/chenjia404/meshproxy/internal/tunnel"
)

func (s *Service) handleRPCStream(str network.Stream) {
	defer str.Close()
	_ = str.SetDeadline(time.Now().Add(30 * time.Second))
	var req RPCRequest
	if err := tunnel.ReadJSONFrame(str, &req); err != nil {
		_ = tunnel.WriteJSONFrame(str, RPCResponse{OK: false, Error: err.Error()})
		return
	}
	resp := RPCResponse{RequestID: req.RequestID, OK: true}
	body, err := s.dispatchRPC(str.Conn().RemotePeer().String(), req.Method, req.Body)
	if err != nil {
		resp.OK = false
		resp.Error = err.Error()
	} else {
		resp.Body = body
	}
	_ = tunnel.WriteJSONFrame(str, resp)
}

func (s *Service) dispatchRPC(remotePeerID, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "get_channel_profile":
		var body struct {
			ChannelID string `json:"channel_id"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		return s.GetChannelProfile(body.ChannelID)
	case "get_channel_head":
		var body struct {
			ChannelID string `json:"channel_id"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		return s.GetChannelHead(body.ChannelID)
	case "get_channel_messages":
		var body struct {
			ChannelID       string `json:"channel_id"`
			BeforeMessageID int64  `json:"before_message_id,omitempty"`
			Limit           int    `json:"limit"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		items, err := s.GetChannelMessages(body.ChannelID, body.BeforeMessageID, body.Limit)
		if err != nil {
			return nil, err
		}
		return GetMessagesResponse{ChannelID: body.ChannelID, Items: items}, nil
	case "get_channel_message":
		var body struct {
			ChannelID string `json:"channel_id"`
			MessageID int64  `json:"message_id"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		return s.GetChannelMessage(body.ChannelID, body.MessageID)
	case "get_channel_changes":
		var body struct {
			ChannelID string `json:"channel_id"`
			AfterSeq  int64  `json:"after_seq"`
			Limit     int    `json:"limit"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		return s.GetChannelChanges(body.ChannelID, body.AfterSeq, body.Limit)
	case "list_channels_by_owner":
		var body struct {
			OwnerPeerID string `json:"owner_peer_id"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		return s.ListChannelsByOwner(body.OwnerPeerID)
	case "create_channel":
		if remotePeerID != s.localPeer {
			return nil, errors.New("write rpc is local-only")
		}
		var body CreateChannelInput
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		return s.CreateChannel(body)
	case "update_channel_profile":
		if remotePeerID != s.localPeer {
			return nil, errors.New("write rpc is local-only")
		}
		var body struct {
			ChannelID string `json:"channel_id"`
			UpdateChannelProfileInput
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		return s.UpdateChannelProfile(body.ChannelID, body.UpdateChannelProfileInput)
	case "create_channel_message":
		if remotePeerID != s.localPeer {
			return nil, errors.New("write rpc is local-only")
		}
		var body struct {
			ChannelID string `json:"channel_id"`
			UpsertMessageInput
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		return s.CreateChannelMessage(body.ChannelID, body.UpsertMessageInput)
	case "update_channel_message":
		if remotePeerID != s.localPeer {
			return nil, errors.New("write rpc is local-only")
		}
		var body struct {
			ChannelID string `json:"channel_id"`
			MessageID int64  `json:"message_id"`
			UpsertMessageInput
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		return s.UpdateChannelMessage(context.Background(), body.ChannelID, body.MessageID, body.UpsertMessageInput)
	case "delete_channel_message":
		if remotePeerID != s.localPeer {
			return nil, errors.New("write rpc is local-only")
		}
		var body struct {
			ChannelID string `json:"channel_id"`
			MessageID int64  `json:"message_id"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		return s.DeleteChannelMessage(context.Background(), body.ChannelID, body.MessageID)
	case "owner_transfer_init", "owner_transfer_accept", "owner_transfer_get":
		return nil, ErrNotImplemented
	default:
		return nil, fmt.Errorf("unsupported method: %s", method)
	}
}

func (s *Service) rpcCall(ctx context.Context, peerID, method string, body any, out any) error {
	pid, err := peer.Decode(strings.TrimSpace(peerID))
	if err != nil {
		return err
	}
	if s.host.Network().Connectedness(pid) != network.Connected {
		if s.routing != nil {
			findCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			info, ferr := s.routing.FindPeer(findCtx, pid)
			cancel()
			if ferr == nil {
				s.host.Peerstore().AddAddrs(pid, info.Addrs, time.Hour)
			}
		}
		connectCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		if err := s.host.Connect(connectCtx, peer.AddrInfo{ID: pid, Addrs: s.host.Peerstore().Addrs(pid)}); err != nil {
			cancel()
			return err
		}
		cancel()
	}
	stream, err := s.host.NewStream(ctx, pid, p2p.ProtocolPublicChannelRPC)
	if err != nil {
		return err
	}
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(20 * time.Second))
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req := RPCRequest{
		RequestID: uuid.NewString(),
		Method:    method,
		Body:      raw,
	}
	if err := tunnel.WriteJSONFrame(stream, req); err != nil {
		return err
	}
	var resp RPCResponse
	if err := tunnel.ReadJSONFrame(stream, &resp); err != nil {
		return err
	}
	if !resp.OK {
		if resp.Error == ErrNotImplemented.Error() {
			return ErrNotImplemented
		}
		return errors.New(resp.Error)
	}
	if out == nil || resp.Body == nil {
		return nil
	}
	rawResp, err := json.Marshal(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(rawResp, out)
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func jsonUnmarshal(data []byte, out any) error {
	return json.Unmarshal(data, out)
}
