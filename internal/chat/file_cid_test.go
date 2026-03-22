package chat

import (
	"testing"

	cid "github.com/ipfs/go-cid"
)

func TestComputeChatFileCID(t *testing.T) {
	a, err := ComputeChatFileCID([]byte("hello world"))
	if err != nil {
		t.Fatalf("ComputeChatFileCID returned error: %v", err)
	}
	if _, err := cid.Decode(a); err != nil {
		t.Fatalf("ComputeChatFileCID returned invalid cid %q: %v", a, err)
	}
	b, err := ComputeChatFileCID([]byte("hello world"))
	if err != nil {
		t.Fatalf("ComputeChatFileCID second call returned error: %v", err)
	}
	if a != b {
		t.Fatalf("ComputeChatFileCID not stable: %q != %q", a, b)
	}
	c, err := ComputeChatFileCID([]byte("hello mesh"))
	if err != nil {
		t.Fatalf("ComputeChatFileCID third call returned error: %v", err)
	}
	if a == c {
		t.Fatalf("ComputeChatFileCID should change when content changes")
	}
}
