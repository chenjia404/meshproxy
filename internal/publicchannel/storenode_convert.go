package publicchannel

import (
	"errors"
	"strings"

	"github.com/chenjia404/meshproxy/internal/publicchannel/storenode"
	"github.com/google/uuid"
)

func meshToStoreChannelID(meshChannelID string) (string, error) {
	_, u, err := ParseChannelID(meshChannelID)
	if err != nil {
		return "", err
	}
	s := u.String()
	if err := validateStoreUUIDv7(s); err != nil {
		return "", err
	}
	return s, nil
}

func validateStoreUUIDv7(id string) error {
	u, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if u.Version() != 7 {
		return errors.New("channel_id must be UUIDv7")
	}
	return nil
}

func avatarToStoreNode(a Avatar) *storenode.ChannelImage {
	if strings.TrimSpace(a.FileName) == "" && strings.TrimSpace(a.BlobID) == "" {
		return nil
	}
	return &storenode.ChannelImage{
		CID:     strings.TrimSpace(a.BlobID),
		MediaID: "",
		BlobID:  strings.TrimSpace(a.BlobID),
		SHA256:  strings.TrimSpace(a.SHA256),
		URL:     strings.TrimSpace(a.URL),
		Mime:    strings.TrimSpace(a.MIMEType),
		Size:    a.Size,
		Name:    strings.TrimSpace(a.FileName),
	}
}

func fileToStoreNode(f File) storenode.ChannelFile {
	return storenode.ChannelFile{
		CID:     strings.TrimSpace(f.BlobID),
		MediaID: "",
		BlobID:  strings.TrimSpace(f.BlobID),
		SHA256:  strings.TrimSpace(f.SHA256),
		URL:     strings.TrimSpace(f.URL),
		Mime:    strings.TrimSpace(f.MIMEType),
		Size:    f.Size,
		Name:    strings.TrimSpace(f.FileName),
	}
}

func messageContentToStoreNode(c MessageContent) storenode.ChannelContent {
	out := storenode.ChannelContent{Text: strings.TrimSpace(c.Text)}
	if len(c.Files) == 0 {
		return out
	}
	for _, f := range c.Files {
		cf := fileToStoreNode(f)
		mime := strings.ToLower(strings.TrimSpace(f.MIMEType))
		if strings.HasPrefix(mime, "image/") {
			out.Images = append(out.Images, storenode.ChannelImage{
				CID:     cf.CID,
				MediaID: cf.MediaID,
				BlobID:  cf.BlobID,
				SHA256:  cf.SHA256,
				URL:     cf.URL,
				Mime:    cf.Mime,
				Size:    cf.Size,
				Name:    cf.Name,
			})
		} else {
			out.Files = append(out.Files, cf)
		}
	}
	return out
}

func profileToStoreNode(storeCID string, p ChannelProfile) storenode.ChannelProfile {
	av := avatarToStoreNode(p.Avatar)
	return storenode.ChannelProfile{
		ChannelID:               storeCID,
		OwnerPeerID:             strings.TrimSpace(p.OwnerPeerID),
		OwnerVersion:            int(p.OwnerVersion),
		Name:                    p.Name,
		Avatar:                  av,
		Bio:                     p.Bio,
		MessageRetentionMinutes: p.MessageRetentionMinutes,
		ProfileVersion:          int(p.ProfileVersion),
		CreatedAt:               p.CreatedAt,
		UpdatedAt:               p.UpdatedAt,
	}
}

func headToStoreNode(storeCID string, h ChannelHead) storenode.ChannelHead {
	return storenode.ChannelHead{
		ChannelID:      storeCID,
		OwnerPeerID:    strings.TrimSpace(h.OwnerPeerID),
		OwnerVersion:   int(h.OwnerVersion),
		LastMessageID:  int(h.LastMessageID),
		ProfileVersion: int(h.ProfileVersion),
		LastSeq:        int(h.LastSeq),
		UpdatedAt:      h.UpdatedAt,
	}
}

func messageToStoreNode(storeCID string, m ChannelMessage) storenode.ChannelMessage {
	return storenode.ChannelMessage{
		ChannelID:     storeCID,
		MessageID:     int(m.MessageID),
		Version:       int(m.Version),
		Seq:           int(m.Seq),
		OwnerVersion:  int(m.OwnerVersion),
		CreatorPeerID: strings.TrimSpace(m.CreatorPeerID),
		AuthorPeerID:  strings.TrimSpace(m.AuthorPeerID),
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
		IsDeleted:     m.IsDeleted,
		MessageType:   m.MessageType,
		Content:       messageContentToStoreNode(m.Content),
	}
}
