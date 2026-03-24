package offlinestore

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestStreamCodecRoundTrip(t *testing.T) {
	var c StreamCodec
	type sample struct {
		Version int    `json:"version"`
		Hello   string `json:"hello"`
	}
	in := sample{Version: 1, Hello: "test"}
	var buf bytes.Buffer
	if err := c.WriteFrame(&buf, in); err != nil {
		t.Fatal(err)
	}
	b, err := c.ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	var out sample
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("got %+v want %+v", out, in)
	}
}

func TestReadFrameTooLarge(t *testing.T) {
	var c StreamCodec
	var buf bytes.Buffer
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(MaxFrameSize)+1)
	buf.Write(hdr[:])
	_, err := c.ReadFrame(&buf)
	if err != ErrFrameTooLarge {
		t.Fatalf("want ErrFrameTooLarge, got %v", err)
	}
}
