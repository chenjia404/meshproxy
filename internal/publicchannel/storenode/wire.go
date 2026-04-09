package storenode

// 与 meshchat-store-node internal/publicchannel 类型 JSON 字段对齐。

// ChannelImage 头像或消息内图片。
type ChannelImage struct {
	CID     string `json:"cid"`
	MediaID string `json:"media_id"`
	BlobID  string `json:"blob_id"`
	SHA256  string `json:"sha256"`
	URL     string `json:"url"`
	Mime    string `json:"mime"`
	Size    int64  `json:"size"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Name    string `json:"name"`
}

// ChannelFile 附件。
type ChannelFile struct {
	CID     string `json:"cid"`
	MediaID string `json:"media_id"`
	BlobID  string `json:"blob_id"`
	SHA256  string `json:"sha256"`
	URL     string `json:"url"`
	Mime    string `json:"mime"`
	Size    int64  `json:"size"`
	Name    string `json:"name"`
}

// ChannelContent 消息正文。
type ChannelContent struct {
	Text   string         `json:"text"`
	Images []ChannelImage `json:"images"`
	Files  []ChannelFile  `json:"files"`
}

// ChannelProfile 频道资料（store 侧）。
type ChannelProfile struct {
	ChannelID               string         `json:"channel_id"`
	OwnerPeerID             string         `json:"owner_peer_id"`
	OwnerVersion            int            `json:"owner_version"`
	Name                    string         `json:"name"`
	Avatar                  *ChannelImage  `json:"avatar,omitempty"`
	Bio                     string         `json:"bio"`
	MessageRetentionMinutes int            `json:"message_retention_minutes"`
	ProfileVersion          int            `json:"profile_version"`
	CreatedAt               int64          `json:"created_at"`
	UpdatedAt               int64          `json:"updated_at"`
	Signature               string         `json:"signature"`
}

// ChannelHead 频道头。
type ChannelHead struct {
	ChannelID      string `json:"channel_id"`
	OwnerPeerID    string `json:"owner_peer_id"`
	OwnerVersion   int    `json:"owner_version"`
	LastMessageID  int    `json:"last_message_id"`
	ProfileVersion int    `json:"profile_version"`
	LastSeq        int    `json:"last_seq"`
	UpdatedAt      int64  `json:"updated_at"`
	Signature      string `json:"signature"`
}

// ChannelMessage 单条消息。
type ChannelMessage struct {
	ChannelID     string         `json:"channel_id"`
	MessageID     int            `json:"message_id"`
	Version       int            `json:"version"`
	Seq           int            `json:"seq"`
	OwnerVersion  int            `json:"owner_version"`
	CreatorPeerID string         `json:"creator_peer_id"`
	AuthorPeerID  string         `json:"author_peer_id"`
	CreatedAt     int64          `json:"created_at"`
	UpdatedAt     int64          `json:"updated_at"`
	IsDeleted     bool           `json:"is_deleted"`
	MessageType   string         `json:"message_type"`
	Content       ChannelContent `json:"content"`
	Signature     string         `json:"signature"`
}

// PushRequest owner 向 store-node 提交。
type PushRequest struct {
	Profile  *ChannelProfile   `json:"profile"`
	Head     *ChannelHead      `json:"head"`
	Messages []*ChannelMessage `json:"messages,omitempty"`
}

// PushResponse 推送结果。
type PushResponse struct {
	OK           bool   `json:"ok"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}
