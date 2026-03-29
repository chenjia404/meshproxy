package publicchannel

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	TopicPrefix = "/meshchat/public-channel/"
	TopicSuffix = "/v1"
)

const (
	ChangeTypeMessage = "message"
	ChangeTypeProfile = "profile"
)

const (
	MessageTypeText    = "text"
	MessageTypeImage   = "image"
	MessageTypeVideo   = "video"
	MessageTypeAudio   = "audio"
	MessageTypeFile    = "file"
	MessageTypeSystem  = "system"
	MessageTypeDeleted = "deleted"
)

const (
	DefaultPageLimit    = 20
	DefaultChangesLimit = 100
	DefaultProviderFind = 16
	MinRetentionMinutes = 1
	MaxRetentionMinutes = 60 * 24 * 365
)

var ErrNotImplemented = errors.New("not_implemented")

type Avatar struct {
	FileName string `json:"file_name,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	BlobID   string `json:"blob_id,omitempty"`
	URL      string `json:"url,omitempty"`
}

type File struct {
	FileID   string `json:"file_id,omitempty"`
	FileName string `json:"file_name"`
	MIMEType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	BlobID   string `json:"blob_id,omitempty"`
	URL      string `json:"url,omitempty"`
}

type MessageContent struct {
	Text  string `json:"text,omitempty"`
	Files []File `json:"files,omitempty"`
}

type ChannelProfile struct {
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
	Signature               string `json:"signature"`
}

type ChannelHead struct {
	ChannelID      string `json:"channel_id"`
	OwnerPeerID    string `json:"owner_peer_id"`
	OwnerVersion   int64  `json:"owner_version"`
	LastMessageID  int64  `json:"last_message_id"`
	ProfileVersion int64  `json:"profile_version"`
	LastSeq        int64  `json:"last_seq"`
	UpdatedAt      int64  `json:"updated_at"`
	Signature      string `json:"signature"`
}

type ChannelMessage struct {
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
	Signature     string         `json:"signature"`
}

type ChannelChange struct {
	ChannelID      string `json:"channel_id"`
	Seq            int64  `json:"seq"`
	ChangeType     string `json:"change_type"`
	MessageID      *int64 `json:"message_id,omitempty"`
	Version        *int64 `json:"version,omitempty"`
	IsDeleted      *bool  `json:"is_deleted,omitempty"`
	ProfileVersion *int64 `json:"profile_version,omitempty"`
	CreatedAt      int64  `json:"created_at"`
	ProviderPeerID string `json:"provider_peer_id,omitempty"`
}

type ChannelSyncState struct {
	ChannelID             string `json:"channel_id"`
	LastSeenSeq           int64  `json:"last_seen_seq"`
	LastSyncedSeq         int64  `json:"last_synced_seq"`
	LatestLoadedMessageID int64  `json:"latest_loaded_message_id"`
	OldestLoadedMessageID int64  `json:"oldest_loaded_message_id"`
	UnreadCount           int    `json:"unread_count"`
	Subscribed            bool   `json:"subscribed"`
	UpdatedAt             int64  `json:"updated_at"`
}

type ChannelProvider struct {
	ChannelID     string `json:"channel_id"`
	PeerID        string `json:"peer_id"`
	Source        string `json:"source"`
	UpdatedAt     int64  `json:"updated_at"`
	LastSuccessAt int64  `json:"last_success_at"`
	LastFailureAt int64  `json:"last_failure_at"`
	SuccessCount  int64  `json:"success_count"`
	FailureCount  int64  `json:"failure_count"`
}

type ChannelSummary struct {
	Profile ChannelProfile   `json:"profile"`
	Head    ChannelHead      `json:"head"`
	Sync    ChannelSyncState `json:"sync"`
}

type GetMessagesResponse struct {
	ChannelID string           `json:"channel_id"`
	Items     []ChannelMessage `json:"items"`
}

type GetChangesResponse struct {
	ChannelID      string          `json:"channel_id"`
	CurrentLastSeq int64           `json:"current_last_seq"`
	HasMore        bool            `json:"has_more"`
	NextAfterSeq   int64           `json:"next_after_seq"`
	Items          []ChannelChange `json:"items"`
}

type SubscribeResult struct {
	Profile   ChannelProfile    `json:"profile"`
	Head      ChannelHead       `json:"head"`
	Messages  []ChannelMessage  `json:"messages"`
	Providers []ChannelProvider `json:"providers,omitempty"`
}

type ChannelSubscriptionDigest struct {
	ChannelID string `json:"channel_id"`
	LastSeq   int64  `json:"last_seq"`
}

type ExchangeSubscriptionsRequest struct {
	Items []ChannelSubscriptionDigest `json:"items"`
}

type ExchangeSubscriptionsResponse struct {
	Items []ChannelSubscriptionDigest `json:"items"`
}

type CreateChannelInput struct {
	Name                    string `json:"name"`
	Bio                     string `json:"bio"`
	Avatar                  Avatar `json:"avatar"`
	MessageRetentionMinutes int    `json:"message_retention_minutes"`
}

type UpdateChannelProfileInput struct {
	Name                    string `json:"name"`
	Bio                     string `json:"bio"`
	Avatar                  Avatar `json:"avatar"`
	MessageRetentionMinutes *int   `json:"message_retention_minutes,omitempty"`
}

type UpsertMessageInput struct {
	MessageType string `json:"message_type,omitempty"`
	Text        string `json:"text"`
	Files       []File `json:"files"`
}

type UploadFileInput struct {
	FileName string
	MIMEType string
	Data     []byte
}

// RPCRequest is the single-stream multi-method public-channel RPC request.
type RPCRequest struct {
	RequestID string          `json:"request_id"`
	Method    string          `json:"method"`
	Body      json.RawMessage `json:"body"`
}

// RPCResponse is the single-stream multi-method public-channel RPC response.
type RPCResponse struct {
	RequestID string `json:"request_id"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Body      any    `json:"body,omitempty"`
}

func TopicForChannel(channelID string) string {
	return TopicPrefix + strings.TrimSpace(channelID) + TopicSuffix
}

// BuildChannelID binds the owner peer ID to the random UUID, so the owner is part of
// the canonical channel identifier rather than a separate mutable field.
func BuildChannelID(ownerPeerID string, channelUUID uuid.UUID) string {
	return strings.TrimSpace(ownerPeerID) + ":" + channelUUID.String()
}

// ParseChannelID accepts both canonical "ownerPeerID:uuidv7" IDs and legacy plain uuidv7 IDs.
// Legacy IDs return an empty ownerPeerID so callers can decide whether a migration is needed.
func ParseChannelID(channelID string) (ownerPeerID string, channelUUID uuid.UUID, err error) {
	trimmed := strings.TrimSpace(channelID)
	if trimmed == "" {
		return "", uuid.Nil, errors.New("channel_id is required")
	}
	parts := strings.SplitN(trimmed, ":", 2)
	if len(parts) == 1 {
		parsed, parseErr := uuid.Parse(parts[0])
		if parseErr != nil {
			return "", uuid.Nil, fmt.Errorf("invalid channel_id: %w", parseErr)
		}
		if parsed.Version() != 7 {
			return "", uuid.Nil, fmt.Errorf("channel_id must be uuidv7")
		}
		return "", parsed, nil
	}
	ownerPeerID = strings.TrimSpace(parts[0])
	if ownerPeerID == "" {
		return "", uuid.Nil, errors.New("channel_id owner_peer_id is required")
	}
	parsed, parseErr := uuid.Parse(strings.TrimSpace(parts[1]))
	if parseErr != nil {
		return "", uuid.Nil, fmt.Errorf("invalid channel_id uuid: %w", parseErr)
	}
	if parsed.Version() != 7 {
		return "", uuid.Nil, fmt.Errorf("channel_id uuid must be uuidv7")
	}
	return ownerPeerID, parsed, nil
}

func ValidateChannelID(channelID string) error {
	_, _, err := ParseChannelID(channelID)
	return err
}

func NormalizeMessageContent(text string, files []File) MessageContent {
	out := MessageContent{Text: strings.TrimSpace(text)}
	if len(files) == 0 {
		return out
	}
	out.Files = make([]File, 0, len(files))
	for _, item := range files {
		name := strings.TrimSpace(item.FileName)
		if name == "" {
			continue
		}
		item.FileName = name
		item.MIMEType = strings.TrimSpace(item.MIMEType)
		item.SHA256 = strings.TrimSpace(item.SHA256)
		item.BlobID = strings.TrimSpace(item.BlobID)
		item.URL = strings.TrimSpace(item.URL)
		out.Files = append(out.Files, item)
	}
	return out
}

func NormalizeMessageType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case MessageTypeText:
		return MessageTypeText
	case MessageTypeImage:
		return MessageTypeImage
	case MessageTypeVideo:
		return MessageTypeVideo
	case MessageTypeAudio:
		return MessageTypeAudio
	case MessageTypeFile:
		return MessageTypeFile
	case MessageTypeSystem:
		return MessageTypeSystem
	case MessageTypeDeleted:
		return MessageTypeDeleted
	default:
		return ""
	}
}

func DetermineMessageType(content MessageContent, requestedType string, deleted bool) string {
	if deleted {
		return MessageTypeDeleted
	}
	requestedType = NormalizeMessageType(requestedType)
	if len(content.Files) == 0 {
		if requestedType == MessageTypeSystem {
			return MessageTypeSystem
		}
		return MessageTypeText
	}
	switch requestedType {
	case MessageTypeImage, MessageTypeVideo, MessageTypeAudio, MessageTypeFile:
		return requestedType
	}
	mime := ""
	for _, item := range content.Files {
		mime = strings.ToLower(strings.TrimSpace(item.MIMEType))
		if mime != "" {
			break
		}
	}
	switch {
	case strings.HasPrefix(mime, "image/"):
		return MessageTypeImage
	case strings.HasPrefix(mime, "video/"):
		return MessageTypeVideo
	case strings.HasPrefix(mime, "audio/"):
		return MessageTypeAudio
	default:
		return MessageTypeFile
	}
}

func NormalizeRetentionMinutes(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func ValidateRetentionMinutes(v int) error {
	v = NormalizeRetentionMinutes(v)
	if v != 0 && (v < MinRetentionMinutes || v > MaxRetentionMinutes) {
		return fmt.Errorf("message_retention_minutes must be 0 or between %d and %d", MinRetentionMinutes, MaxRetentionMinutes)
	}
	return nil
}
