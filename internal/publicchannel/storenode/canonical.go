package storenode

import (
	"encoding/json"
	"errors"
)

// 与 meshchat-store-node internal/publicchannel/canonical.go 一致，供 store 节点验签。

var (
	errNilProfile = errors.New("channel profile is nil")
	errNilHead    = errors.New("channel head is nil")
	errNilMessage = errors.New("channel message is nil")
)

func canonicalImage(img *ChannelImage) any {
	if img == nil {
		return nil
	}
	return []any{
		img.CID,
		img.MediaID,
		img.BlobID,
		img.SHA256,
		img.URL,
		img.Mime,
		img.Size,
		img.Width,
		img.Height,
		img.Name,
	}
}

func canonicalFile(f *ChannelFile) any {
	if f == nil {
		return nil
	}
	return []any{
		f.CID,
		f.MediaID,
		f.BlobID,
		f.SHA256,
		f.URL,
		f.Mime,
		f.Size,
		f.Name,
	}
}

func canonicalContent(c ChannelContent) ([]byte, error) {
	images := make([]any, len(c.Images))
	for i := range c.Images {
		images[i] = canonicalImage(&c.Images[i])
	}
	files := make([]any, len(c.Files))
	for i := range c.Files {
		files[i] = canonicalFile(&c.Files[i])
	}
	return json.Marshal([]any{c.Text, images, files})
}

// CanonicalProfile 资料验签字节。
func CanonicalProfile(p *ChannelProfile) ([]byte, error) {
	if p == nil {
		return nil, errNilProfile
	}
	return json.Marshal([]any{
		p.ChannelID,
		p.OwnerPeerID,
		p.OwnerVersion,
		p.Name,
		canonicalImage(p.Avatar),
		p.Bio,
		p.ProfileVersion,
		p.CreatedAt,
		p.UpdatedAt,
	})
}

// CanonicalHead 频道头验签字节。
func CanonicalHead(h *ChannelHead) ([]byte, error) {
	if h == nil {
		return nil, errNilHead
	}
	return json.Marshal([]any{
		h.ChannelID,
		h.OwnerPeerID,
		h.OwnerVersion,
		h.LastMessageID,
		h.ProfileVersion,
		h.LastSeq,
		h.UpdatedAt,
	})
}

// CanonicalMessage 消息验签字节。
func CanonicalMessage(m *ChannelMessage) ([]byte, error) {
	if m == nil {
		return nil, errNilMessage
	}
	content, err := canonicalContent(m.Content)
	if err != nil {
		return nil, err
	}
	var contentVal any
	if err := json.Unmarshal(content, &contentVal); err != nil {
		return nil, err
	}
	return json.Marshal([]any{
		m.ChannelID,
		m.MessageID,
		m.Version,
		m.Seq,
		m.OwnerVersion,
		m.CreatorPeerID,
		m.AuthorPeerID,
		m.CreatedAt,
		m.UpdatedAt,
		m.IsDeleted,
		m.MessageType,
		contentVal,
	})
}
