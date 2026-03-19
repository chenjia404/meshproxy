package meshserver

import "github.com/chenjia404/meshproxy/internal/meshserver/sessionv1"

// MyServer is the "joined server" view for the currently authenticated user.
type MyServer struct {
	// json key uses Space terminology for the "my spaces list" endpoint.
	Server *sessionv1.ServerSummary `json:"space"`
	Role   sessionv1.MemberRole     `json:"role"`
}

type MyServersResp struct {
	Servers []*MyServer `json:"servers"`
}

