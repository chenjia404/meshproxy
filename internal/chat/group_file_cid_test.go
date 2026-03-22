package chat

import (
	"strings"
	"testing"
)

func TestMarshalGroupChatFileForSigningOmitsEmptyFileCID(t *testing.T) {
	msg := GroupChatFile{
		Type:         MessageTypeGroupChatFile,
		GroupID:      "group-1",
		Epoch:        1,
		MsgID:        "msg-1",
		SenderPeerID: "peer-1",
		SenderSeq:    1,
		FileName:     "demo.mp4",
		MIMEType:     "video/mp4",
		FileSize:     1024,
		SentAtUnix:   12345,
	}
	payload, err := marshalGroupChatFileForSigning(msg)
	if err != nil {
		t.Fatalf("marshal without file cid: %v", err)
	}
	if strings.Contains(string(payload), "file_cid") {
		t.Fatalf("expected empty file cid to be omitted, got %s", payload)
	}

	msg.FileCID = "bafybeigdyrzt3jz2"
	payload, err = marshalGroupChatFileForSigning(msg)
	if err != nil {
		t.Fatalf("marshal with file cid: %v", err)
	}
	if !strings.Contains(string(payload), "\"file_cid\":\"bafybeigdyrzt3jz2\"") {
		t.Fatalf("expected file cid in payload, got %s", payload)
	}
}
