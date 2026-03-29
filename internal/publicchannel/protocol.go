package publicchannel

import (
	"encoding/json"
	"fmt"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/chenjia404/meshproxy/internal/binding"
)

const (
	// ProtocolRPC is the single public-channel RPC protocol.
	// Multiple logical methods are multiplexed over this one stream.
	ProtocolRPC protocol.ID = "/meshchat/public-channel/0.1.1"

	MethodGetChannelProfile     = "channel.get_profile"
	MethodGetChannelHead        = "channel.get_head"
	MethodGetChannelMessages    = "channel.get_messages"
	MethodGetChannelMessage     = "channel.get_message"
	MethodGetChannelChanges     = "channel.get_changes"
	MethodListChannelsByOwner   = "channel.list_by_owner"
	MethodExchangeSubscriptions = "channel.exchange_subscriptions"
	MethodCreateChannel         = "channel.create"
	MethodUpdateChannel         = "channel.update_profile"
	MethodCreateMessage         = "channel.create_message"
	MethodUpdateMessage         = "channel.update_message"
	MethodDeleteMessage         = "channel.delete_message"

	signPrefixProfile = "meshchat/public-channel/profile/v1:"
	signPrefixHead    = "meshchat/public-channel/head/v1:"
	signPrefixMessage = "meshchat/public-channel/message/v1:"
)

type canonicalProfile struct {
	ChannelID               string `json:"channel_id"`
	OwnerPeerID             string `json:"owner_peer_id"`
	OwnerVersion            int64  `json:"owner_version"`
	Name                    string `json:"name"`
	Avatar                  Avatar `json:"avatar"`
	Bio                     string `json:"bio"`
	MessageRetentionMinutes int    `json:"message_retention_minutes"`
	ProfileVersion          int64  `json:"profile_version"`
	CreatedAt               int64  `json:"created_at"`
	UpdatedAt               int64  `json:"updated_at"`
}

type canonicalHead struct {
	ChannelID      string `json:"channel_id"`
	OwnerPeerID    string `json:"owner_peer_id"`
	OwnerVersion   int64  `json:"owner_version"`
	LastMessageID  int64  `json:"last_message_id"`
	ProfileVersion int64  `json:"profile_version"`
	LastSeq        int64  `json:"last_seq"`
	UpdatedAt      int64  `json:"updated_at"`
}

type canonicalMessage struct {
	ChannelID     string         `json:"channel_id"`
	MessageID     int64          `json:"message_id"`
	Version       int64          `json:"version"`
	Seq           int64          `json:"seq"`
	OwnerVersion  int64          `json:"owner_version"`
	CreatorPeerID string         `json:"creator_peer_id"`
	AuthorPeerID  string         `json:"author_peer_id"`
	CreatedAt     int64          `json:"created_at"`
	UpdatedAt     int64          `json:"updated_at"`
	IsDeleted     bool           `json:"is_deleted"`
	MessageType   string         `json:"message_type"`
	Content       MessageContent `json:"content"`
}

func canonicalizeProfile(profile ChannelProfile) ([]byte, error) {
	if err := ValidateChannelID(profile.ChannelID); err != nil {
		return nil, err
	}
	if boundOwner, _, err := ParseChannelID(profile.ChannelID); err != nil {
		return nil, err
	} else if boundOwner != "" && boundOwner != profile.OwnerPeerID {
		return nil, fmt.Errorf("channel_id owner mismatch: channel owner=%s profile owner=%s", boundOwner, profile.OwnerPeerID)
	}
	payload, err := json.Marshal(canonicalProfile{
		ChannelID:               profile.ChannelID,
		OwnerPeerID:             profile.OwnerPeerID,
		OwnerVersion:            profile.OwnerVersion,
		Name:                    profile.Name,
		Avatar:                  profile.Avatar,
		Bio:                     profile.Bio,
		MessageRetentionMinutes: profile.MessageRetentionMinutes,
		ProfileVersion:          profile.ProfileVersion,
		CreatedAt:               profile.CreatedAt,
		UpdatedAt:               profile.UpdatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal channel profile canonical: %w", err)
	}
	return append([]byte(signPrefixProfile), payload...), nil
}

func canonicalizeHead(head ChannelHead) ([]byte, error) {
	if err := ValidateChannelID(head.ChannelID); err != nil {
		return nil, err
	}
	if boundOwner, _, err := ParseChannelID(head.ChannelID); err != nil {
		return nil, err
	} else if boundOwner != "" && boundOwner != head.OwnerPeerID {
		return nil, fmt.Errorf("channel_id owner mismatch: channel owner=%s head owner=%s", boundOwner, head.OwnerPeerID)
	}
	payload, err := json.Marshal(canonicalHead{
		ChannelID:      head.ChannelID,
		OwnerPeerID:    head.OwnerPeerID,
		OwnerVersion:   head.OwnerVersion,
		LastMessageID:  head.LastMessageID,
		ProfileVersion: head.ProfileVersion,
		LastSeq:        head.LastSeq,
		UpdatedAt:      head.UpdatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal channel head canonical: %w", err)
	}
	return append([]byte(signPrefixHead), payload...), nil
}

func canonicalizeMessage(message ChannelMessage) ([]byte, error) {
	if err := ValidateChannelID(message.ChannelID); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(canonicalMessage{
		ChannelID:     message.ChannelID,
		MessageID:     message.MessageID,
		Version:       message.Version,
		Seq:           message.Seq,
		OwnerVersion:  message.OwnerVersion,
		CreatorPeerID: message.CreatorPeerID,
		AuthorPeerID:  message.AuthorPeerID,
		CreatedAt:     message.CreatedAt,
		UpdatedAt:     message.UpdatedAt,
		IsDeleted:     message.IsDeleted,
		MessageType:   message.MessageType,
		Content:       message.Content,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal channel message canonical: %w", err)
	}
	return append([]byte(signPrefixMessage), payload...), nil
}

func signProfile(priv crypto.PrivKey, profile *ChannelProfile) error {
	canon, err := canonicalizeProfile(*profile)
	if err != nil {
		return err
	}
	sig, err := binding.SignWithPrivKey(priv, canon)
	if err != nil {
		return err
	}
	profile.Signature = sig
	return nil
}

func signHead(priv crypto.PrivKey, head *ChannelHead) error {
	canon, err := canonicalizeHead(*head)
	if err != nil {
		return err
	}
	sig, err := binding.SignWithPrivKey(priv, canon)
	if err != nil {
		return err
	}
	head.Signature = sig
	return nil
}

func signMessage(priv crypto.PrivKey, message *ChannelMessage) error {
	canon, err := canonicalizeMessage(*message)
	if err != nil {
		return err
	}
	sig, err := binding.SignWithPrivKey(priv, canon)
	if err != nil {
		return err
	}
	message.Signature = sig
	return nil
}

func verifyProfile(profile ChannelProfile) error {
	canon, err := canonicalizeProfile(profile)
	if err != nil {
		return err
	}
	return binding.VerifyPeerSignature(profile.OwnerPeerID, canon, profile.Signature)
}

func verifyHead(head ChannelHead) error {
	canon, err := canonicalizeHead(head)
	if err != nil {
		return err
	}
	return binding.VerifyPeerSignature(head.OwnerPeerID, canon, head.Signature)
}

func verifyMessage(ownerPeerID string, message ChannelMessage) error {
	canon, err := canonicalizeMessage(message)
	if err != nil {
		return err
	}
	return binding.VerifyPeerSignature(ownerPeerID, canon, message.Signature)
}
