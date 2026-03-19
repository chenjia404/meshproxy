package meshserver

import (
	"github.com/chenjia404/meshproxy/internal/meshserver/sessionv1"
)

// MyPermissions is a HTTP-facing view of "GET_MY_PERMISSIONS".
// The underlying meshserver protocol message may not be wired into meshproxy yet,
// so we derive it from existing data (auth + server summary + member role).
type MyPermissions struct {
	ServerID string `json:"server_id"`

	// Role is the caller's role on the given server (proto enum number).
	Role sessionv1.MemberRole `json:"role"`

	CanCreateChannel       bool `json:"can_create_channel"`
	CanManageMembers       bool `json:"can_manage_members"`
	CanSetMemberRoles      bool `json:"can_set_member_roles"`
	CanSetChannelCreation  bool `json:"can_set_channel_creation"`
}

