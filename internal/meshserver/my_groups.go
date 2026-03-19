package meshserver

import "github.com/chenjia404/meshproxy/internal/meshserver/sessionv1"

// MyGroup is a HTTP-facing view of a joined GROUP channel for the current user.
// It uses Space terminology: this is a group inside a given space (server_id).
type MyGroup struct {
	ID               int                   `json:"id"`
	ChannelID        string                `json:"channel_id"`
	Type             sessionv1.ChannelType `json:"type"`
	Name             string                `json:"name"`
	Description      string                `json:"description"`
	Visibility       sessionv1.Visibility `json:"visibility"`
	SlowModeSeconds  uint32               `json:"slow_mode_seconds"`
	LastSeq          uint64               `json:"last_seq"`
	CanView          bool                  `json:"can_view"`
	CanSendMessage  bool                  `json:"can_send_message"`
	CanSendImage    bool                  `json:"can_send_image"`
	CanSendFile     bool                  `json:"can_send_file"`
}

type MyGroupsResp struct {
	SpaceID string      `json:"space_id"`
	Groups  []*MyGroup  `json:"groups"`
}

