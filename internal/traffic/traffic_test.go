package traffic

import "testing"

func TestRecorderSnapshot(t *testing.T) {
	r := NewRecorder()
	r.Add(128, 256)
	r.Add(10, 20)

	got := r.Snapshot()
	if got.BytesSent != 138 {
		t.Fatalf("BytesSent = %d, want 138", got.BytesSent)
	}
	if got.BytesReceived != 276 {
		t.Fatalf("BytesReceived = %d, want 276", got.BytesReceived)
	}
	if got.BytesTotal != 414 {
		t.Fatalf("BytesTotal = %d, want 414", got.BytesTotal)
	}
}
