package chat

import "testing"

func TestPeekOfflineStoreSeq(t *testing.T) {
	t.Parallel()
	if got := peekOfflineStoreSeq([]byte(`{"store_seq":42,"message":{}}`)); got != 42 {
		t.Fatalf("want 42, got %d", got)
	}
	// 截断 JSON 仍应能提取序号
	if got := peekOfflineStoreSeq([]byte(`{"store_seq":99,"message":`)); got != 99 {
		t.Fatalf("want 99, got %d", got)
	}
	if got := peekOfflineStoreSeq([]byte(`not json`)); got != 0 {
		t.Fatalf("want 0, got %d", got)
	}
}
