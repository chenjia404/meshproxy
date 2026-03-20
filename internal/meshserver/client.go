package meshserver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	proto "github.com/golang/protobuf/proto"
	"github.com/google/uuid"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"
	coreprotocol "github.com/libp2p/go-libp2p/core/protocol"

	"github.com/chenjia404/meshproxy/internal/meshserver/protocol"
	"github.com/chenjia404/meshproxy/internal/meshserver/sessionv1"
)

const (
	DefaultProtocolID  = "/meshserver/session/1.0.0"
	DefaultClientAgent = "meshproxy-client"
)

var ErrNotConnected = errors.New("meshserver client not connected")

type Options struct {
	ClientAgent string
	ProtocolID  string
}

type Client struct {
	host          host.Host
	privKey       crypto.PrivKey
	serverPeerID  peer.ID
	clientAgent   string
	protocolID    coreprotocol.ID
	writeMu       sync.Mutex
	pendingMu     sync.Mutex
	pending       map[string]chan *sessionv1.Envelope
	events        chan *sessionv1.MessageEvent
	done          chan struct{}
	closeOnce     sync.Once
	streamMu      sync.RWMutex
	stream        network.Stream
	readErrMu     sync.RWMutex
	readErr       error
	authMu        sync.RWMutex
	authenticated bool
	authResult    *sessionv1.AuthResult
}

func NewClient(ctx context.Context, h host.Host, privKey crypto.PrivKey, serverPeerID string, opts Options) (*Client, error) {
	if h == nil {
		return nil, errors.New("host is nil")
	}
	if privKey == nil {
		return nil, errors.New("private key is nil")
	}
	pid, err := peer.Decode(serverPeerID)
	if err != nil {
		return nil, fmt.Errorf("decode server peer id: %w", err)
	}
	protoID := opts.ProtocolID
	if protoID == "" {
		protoID = DefaultProtocolID
	}
	c := &Client{
		host:         h,
		privKey:      privKey,
		serverPeerID: pid,
		clientAgent:  opts.ClientAgent,
		protocolID:   coreprotocol.ID(protoID),
		pending:      make(map[string]chan *sessionv1.Envelope),
		events:       make(chan *sessionv1.MessageEvent, 64),
		done:         make(chan struct{}),
	}
	if c.clientAgent == "" {
		c.clientAgent = DefaultClientAgent
	}
	if err := c.connect(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		close(c.done)
		c.streamMu.Lock()
		if c.stream != nil {
			_ = c.stream.Close()
			c.stream = nil
		}
		c.streamMu.Unlock()
		c.pendingMu.Lock()
		for reqID, ch := range c.pending {
			delete(c.pending, reqID)
			close(ch)
		}
		c.pendingMu.Unlock()
		close(c.events)
	})
	return nil
}

func (c *Client) Events() <-chan *sessionv1.MessageEvent {
	if c == nil {
		return nil
	}
	return c.events
}

func (c *Client) Authenticated() bool {
	c.authMu.RLock()
	defer c.authMu.RUnlock()
	return c.authenticated
}

func (c *Client) AuthResult() *sessionv1.AuthResult {
	c.authMu.RLock()
	defer c.authMu.RUnlock()
	if c.authResult == nil {
		return nil
	}
	out := *c.authResult
	return &out
}

func (c *Client) connect(ctx context.Context) error {
	str, err := c.host.NewStream(ctx, c.serverPeerID, c.protocolID)
	if err != nil {
		return fmt.Errorf("connect meshserver stream: %w", err)
	}
	c.streamMu.Lock()
	c.stream = str
	c.streamMu.Unlock()
	safeGo := func(fn func()) {
		go fn()
	}
	safeGo(c.readLoop)
	if err := c.authenticate(ctx); err != nil {
		_ = c.Close()
		return err
	}
	return nil
}

func (c *Client) authenticate(ctx context.Context) error {
	// Hello 與 connect-client 一致：ProtocolVersion "1.0.0"
	helloEnv, err := c.request(ctx, sessionv1.MsgType_HELLO, &sessionv1.Hello{
		ClientPeerId:    c.host.ID().String(),
		ClientAgent:     c.clientAgent,
		ProtocolVersion: "1.0.0",
	})
	if err != nil {
		return err
	}
	var chal sessionv1.AuthChallenge
	if err := protocol.UnmarshalBody(helloEnv.Body, &chal); err != nil {
		return fmt.Errorf("decode auth challenge: %w", err)
	}
	// 與 meshserver internal/auth.BuildChallengePayload 一致：node_peer_id、尾部 \n；ServerPeerId 對應 proto node_peer_id 欄位
	payload := buildChallengePayload(string(c.protocolID), c.host.ID().String(), chal.NodePeerId, chal.Nonce, chal.IssuedAtMs, chal.ExpiresAtMs)
	sig, err := c.privKey.Sign(payload)
	if err != nil {
		return fmt.Errorf("sign auth challenge: %w", err)
	}
	// The server expects the public key in marshaled wire-format.
	// Using Raw() (fixed-size key bytes) may cause proto unmarshal failures server-side.
	pub, err := crypto.MarshalPublicKey(c.privKey.GetPublic())
	if err != nil {
		return fmt.Errorf("get public key: %w", err)
	}
	proveEnv, err := c.request(ctx, sessionv1.MsgType_AUTH_PROVE, &sessionv1.AuthProve{
		ClientPeerId: c.host.ID().String(),
		Nonce:        chal.Nonce,
		IssuedAtMs:   chal.IssuedAtMs,
		ExpiresAtMs:  chal.ExpiresAtMs,
		Signature:    sig,
		PublicKey:    pub,
	})
	if err != nil {
		return err
	}
	var result sessionv1.AuthResult
	if err := protocol.UnmarshalBody(proveEnv.Body, &result); err != nil {
		return fmt.Errorf("decode auth result: %w", err)
	}
	if !result.Ok {
		if result.Message == "" {
			result.Message = "authentication failed"
		}
		return errors.New(result.Message)
	}
	c.authMu.Lock()
	c.authenticated = true
	c.authResult = &result
	c.authMu.Unlock()
	return nil
}

func (c *Client) readLoop() {
	for {
		select {
		case <-c.done:
			return
		default:
		}
		str := c.currentStream()
		if str == nil {
			c.fail(errors.New("stream closed"))
			return
		}
		env, err := protocol.ReadEnvelope(str)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.fail(err)
			} else {
				c.fail(io.EOF)
			}
			return
		}
		if env.MsgType == sessionv1.MsgType_MESSAGE_EVENT {
			var event sessionv1.MessageEvent
			if err := protocol.UnmarshalBody(env.Body, &event); err == nil {
				select {
				case c.events <- &event:
				case <-c.done:
				}
			}
			continue
		}
		if env.RequestId == "" {
			continue
		}
		c.pendingMu.Lock()
		ch, ok := c.pending[env.RequestId]
		if ok {
			delete(c.pending, env.RequestId)
		}
		c.pendingMu.Unlock()
		if ok {
			select {
			case ch <- env:
			case <-c.done:
			}
		}
	}
}

func (c *Client) fail(err error) {
	c.readErrMu.Lock()
	if c.readErr == nil {
		c.readErr = err
	}
	c.readErrMu.Unlock()
	_ = c.Close()
}

func (c *Client) currentStream() network.Stream {
	c.streamMu.RLock()
	defer c.streamMu.RUnlock()
	return c.stream
}

func (c *Client) request(ctx context.Context, msgType sessionv1.MsgType, body proto.Message) (*sessionv1.Envelope, error) {
	if c == nil {
		return nil, ErrNotConnected
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if c.currentStream() == nil {
		return nil, ErrNotConnected
	}
	reqID := uuid.NewString()
	respCh := make(chan *sessionv1.Envelope, 1)
	c.pendingMu.Lock()
	c.pending[reqID] = respCh
	c.pendingMu.Unlock()
	env, err := protocol.NewEnvelope(msgType, reqID, body)
	if err != nil {
		c.pendingMu.Lock()
		delete(c.pending, reqID)
		c.pendingMu.Unlock()
		return nil, err
	}
	if err := protocol.WriteEnvelope(c.currentStream(), env); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, reqID)
		c.pendingMu.Unlock()
		return nil, err
	}
	select {
	case resp, ok := <-respCh:
		if !ok || resp == nil {
			return nil, c.readError()
		}
		if resp.MsgType == sessionv1.MsgType_ERROR {
			var msg sessionv1.ErrorMsg
			if err := protocol.UnmarshalBody(resp.Body, &msg); err != nil {
				return nil, fmt.Errorf("decode error response: %w", err)
			}
			if msg.Message == "" {
				msg.Message = "meshserver request failed"
			}
			return nil, errors.New(msg.Message)
		}
		return resp, nil
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, reqID)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	case <-c.done:
		return nil, c.readError()
	}
}

func (c *Client) readError() error {
	c.readErrMu.RLock()
	defer c.readErrMu.RUnlock()
	if c.readErr != nil {
		return c.readErr
	}
	return ErrNotConnected
}

// buildChallengePayload 與 meshserver internal/auth.BuildChallengePayload 一致：
// 使用 node_peer_id（對應 AuthChallenge 的 node_peer_id/ServerPeerId）且含尾部換行。
func buildChallengePayload(protocolID, clientPeerID, nodePeerID string, nonce []byte, issuedAtMs, expiresAtMs uint64) []byte {
	return []byte(fmt.Sprintf(
		"protocol_id=%s\nclient_peer_id=%s\nnode_peer_id=%s\nnonce=%s\nissued_at_ms=%d\nexpires_at_ms=%d\n",
		protocolID,
		clientPeerID,
		nodePeerID,
		base64.StdEncoding.EncodeToString(nonce),
		issuedAtMs,
		expiresAtMs,
	))
}

func (c *Client) ListServers(ctx context.Context) (*sessionv1.ListSpacesResp, error) {
	env, err := c.request(ctx, sessionv1.MsgType_LIST_SPACES_REQ, &sessionv1.ListSpacesReq{})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.ListSpacesResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetCreateSpacePermissions(ctx context.Context) (*sessionv1.GetCreateSpacePermissionsResp, error) {
	env, err := c.request(ctx, sessionv1.MsgType_GET_CREATE_SPACE_PERMISSIONS_REQ, &sessionv1.GetCreateSpacePermissionsReq{})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.GetCreateSpacePermissionsResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateSpace sends CREATE_SPACE_REQ to the connected meshserver.
func (c *Client) CreateSpace(ctx context.Context, name, description string, visibility sessionv1.Visibility, allowChannelCreation bool) (*sessionv1.CreateSpaceResp, error) {
	env, err := c.request(ctx, sessionv1.MsgType_CREATE_SPACE_REQ, &sessionv1.CreateSpaceReq{
		Name:                 name,
		Description:          description,
		Visibility:           visibility,
		AllowChannelCreation: allowChannelCreation,
	})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.CreateSpaceResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ListChannels(ctx context.Context, spaceID uint32) (*sessionv1.ListChannelsResp, error) {
	env, err := c.request(ctx, sessionv1.MsgType_LIST_CHANNELS_REQ, &sessionv1.ListChannelsReq{SpaceId: spaceID})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.ListChannelsResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) CreateGroup(ctx context.Context, serverID, name, description string, visibility sessionv1.Visibility, slowModeSeconds uint32) (*sessionv1.CreateGroupResp, error) {
	spaceID64, _ := strconv.ParseUint(strings.TrimSpace(serverID), 10, 32)
	env, err := c.request(ctx, sessionv1.MsgType_CREATE_GROUP_REQ, &sessionv1.CreateGroupReq{
		SpaceId:         uint32(spaceID64),
		Name:            name,
		Description:     description,
		Visibility:      visibility,
		SlowModeSeconds: slowModeSeconds,
	})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.CreateGroupResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) CreateChannel(ctx context.Context, serverID, name, description string, visibility sessionv1.Visibility, slowModeSeconds uint32) (*sessionv1.CreateChannelResp, error) {
	spaceID64, _ := strconv.ParseUint(strings.TrimSpace(serverID), 10, 32)
	env, err := c.request(ctx, sessionv1.MsgType_CREATE_CHANNEL_REQ, &sessionv1.CreateChannelReq{
		SpaceId:         uint32(spaceID64),
		Name:            name,
		Description:     description,
		Visibility:      visibility,
		SlowModeSeconds: slowModeSeconds,
	})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.CreateChannelResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) SubscribeChannel(ctx context.Context, channelID uint32, lastSeenSeq uint64) (*sessionv1.SubscribeChannelResp, error) {
	env, err := c.request(ctx, sessionv1.MsgType_SUBSCRIBE_CHANNEL_REQ, &sessionv1.SubscribeChannelReq{
		ChannelId:   channelID,
		LastSeenSeq: lastSeenSeq,
	})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.SubscribeChannelResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UnsubscribeChannel(ctx context.Context, channelID uint32) (*sessionv1.UnsubscribeChannelResp, error) {
	env, err := c.request(ctx, sessionv1.MsgType_UNSUBSCRIBE_CHANNEL_REQ, &sessionv1.UnsubscribeChannelReq{
		ChannelId: channelID,
	})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.UnsubscribeChannelResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) SendMessage(ctx context.Context, channelID, clientMsgID string, messageType sessionv1.MessageType, text string, images []*sessionv1.MediaImage, files []*sessionv1.MediaFile) (*sessionv1.SendMessageAck, error) {
	chID64, _ := strconv.ParseUint(strings.TrimSpace(channelID), 10, 32)
	env, err := c.request(ctx, sessionv1.MsgType_SEND_MESSAGE_REQ, &sessionv1.SendMessageReq{
		ChannelId:   uint32(chID64),
		ClientMsgId: clientMsgID,
		MessageType: messageType,
		Content:     &sessionv1.MessageContent{Text: text, Images: images, Files: files},
	})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.SendMessageAck
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) SyncChannel(ctx context.Context, channelID string, afterSeq uint64, limit uint32) (*sessionv1.SyncChannelResp, error) {
	chID64, _ := strconv.ParseUint(strings.TrimSpace(channelID), 10, 32)
	env, err := c.request(ctx, sessionv1.MsgType_SYNC_CHANNEL_REQ, &sessionv1.SyncChannelReq{
		ChannelId: uint32(chID64),
		AfterSeq:  afterSeq,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.SyncChannelResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetMedia(ctx context.Context, mediaID string) (*sessionv1.GetMediaResp, error) {
	env, err := c.request(ctx, sessionv1.MsgType_GET_MEDIA_REQ, &sessionv1.GetMediaReq{MediaId: mediaID})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.GetMediaResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) SetMemberRole(ctx context.Context, serverID, targetUserID string, role sessionv1.MemberRole) (*sessionv1.AdminSetSpaceMemberRoleResp, error) {
	spaceID64, _ := strconv.ParseUint(strings.TrimSpace(serverID), 10, 32)
	env, err := c.request(ctx, sessionv1.MsgType_ADMIN_SET_SPACE_MEMBER_ROLE_REQ, &sessionv1.AdminSetSpaceMemberRoleReq{
		SpaceId:      uint32(spaceID64),
		TargetUserId: targetUserID,
		Role:         role,
	})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.AdminSetSpaceMemberRoleResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) SetChannelCreation(ctx context.Context, serverID string, enabled bool) (*sessionv1.AdminSetSpaceChannelCreationResp, error) {
	spaceID64, _ := strconv.ParseUint(strings.TrimSpace(serverID), 10, 32)
	env, err := c.request(ctx, sessionv1.MsgType_ADMIN_SET_SPACE_CHANNEL_CREATION_REQ, &sessionv1.AdminSetSpaceChannelCreationReq{
		SpaceId:              uint32(spaceID64),
		AllowChannelCreation: enabled,
	})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.AdminSetSpaceChannelCreationResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) JoinServer(ctx context.Context, serverID string) (*sessionv1.JoinSpaceResp, error) {
	spaceID64, _ := strconv.ParseUint(strings.TrimSpace(serverID), 10, 32)
	env, err := c.request(ctx, sessionv1.MsgType_JOIN_SPACE_REQ, &sessionv1.JoinSpaceReq{
		SpaceId: uint32(spaceID64),
	})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.JoinSpaceResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) InviteServerMember(ctx context.Context, serverID, targetUserID string) (*sessionv1.InviteSpaceMemberResp, error) {
	spaceID64, _ := strconv.ParseUint(strings.TrimSpace(serverID), 10, 32)
	env, err := c.request(ctx, sessionv1.MsgType_INVITE_SPACE_MEMBER_REQ, &sessionv1.InviteSpaceMemberReq{
		SpaceId:      uint32(spaceID64),
		TargetUserId: targetUserID,
	})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.InviteSpaceMemberResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) KickServerMember(ctx context.Context, serverID, targetUserID string) (*sessionv1.KickSpaceMemberResp, error) {
	spaceID64, _ := strconv.ParseUint(strings.TrimSpace(serverID), 10, 32)
	env, err := c.request(ctx, sessionv1.MsgType_KICK_SPACE_MEMBER_REQ, &sessionv1.KickSpaceMemberReq{
		SpaceId:      uint32(spaceID64),
		TargetUserId: targetUserID,
	})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.KickSpaceMemberResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) BanServerMember(ctx context.Context, serverID, targetUserID string) (*sessionv1.BanSpaceMemberResp, error) {
	spaceID64, _ := strconv.ParseUint(strings.TrimSpace(serverID), 10, 32)
	env, err := c.request(ctx, sessionv1.MsgType_BAN_SPACE_MEMBER_REQ, &sessionv1.BanSpaceMemberReq{
		SpaceId:      uint32(spaceID64),
		TargetUserId: targetUserID,
	})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.BanSpaceMemberResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ListServerMembers(ctx context.Context, serverID string, afterMemberID uint64, limit uint32) (*sessionv1.ListSpaceMembersResp, error) {
	spaceID64, _ := strconv.ParseUint(strings.TrimSpace(serverID), 10, 32)
	env, err := c.request(ctx, sessionv1.MsgType_LIST_SPACE_MEMBERS_REQ, &sessionv1.ListSpaceMembersReq{
		SpaceId:       uint32(spaceID64),
		AfterMemberId: afterMemberID,
		Limit:         limit,
	})
	if err != nil {
		return nil, err
	}
	var resp sessionv1.ListSpaceMembersResp
	if err := protocol.UnmarshalBody(env.Body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
