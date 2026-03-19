package sessionv1

import (
	proto "github.com/golang/protobuf/proto"
)

// NOTE:
// This project vendors a generated session.pb.go.
// Upstream meshserver introduced GET_MEDIA_REQ/RESP recently, but this vendored
// proto file may not yet include it. We extend it here to keep compatibility.

const (
	MsgType_GET_MEDIA_REQ  MsgType = 53
	MsgType_GET_MEDIA_RESP MsgType = 54
)

func init() {
	// Extend MsgType_name for nicer debugging (optional).
	if MsgType_name != nil {
		MsgType_name[int32(MsgType_GET_MEDIA_REQ)] = "GET_MEDIA_REQ"
		MsgType_name[int32(MsgType_GET_MEDIA_RESP)] = "GET_MEDIA_RESP"
	}
}

// GetMediaReq asks server to return media (image/file) by media_id.
type GetMediaReq struct {
	MediaId string `protobuf:"bytes,1,opt,name=media_id,json=mediaId,proto3" json:"media_id,omitempty"`
}

func (m *GetMediaReq) Reset()         { *m = GetMediaReq{} }
func (m *GetMediaReq) String() string { return proto.CompactTextString(m) }
func (*GetMediaReq) ProtoMessage()    {}

// GetMediaResp returns a single media file.
type GetMediaResp struct {
	File *MediaFile `protobuf:"bytes,1,opt,name=file,proto3" json:"file,omitempty"`
}

func (m *GetMediaResp) Reset()         { *m = GetMediaResp{} }
func (m *GetMediaResp) String() string { return proto.CompactTextString(m) }
func (*GetMediaResp) ProtoMessage()    {}

