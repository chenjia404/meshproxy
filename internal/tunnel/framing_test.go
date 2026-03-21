package tunnel

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteJSONFrameAllowsPayloadAboveOneMiB(t *testing.T) {
	payload := map[string]string{
		"blob": strings.Repeat("a", (1<<20)+1024),
	}

	var buf bytes.Buffer
	if err := WriteJSONFrame(&buf, payload); err != nil {
		t.Fatalf("WriteJSONFrame() error = %v", err)
	}

	var decoded map[string]string
	if err := ReadJSONFrame(&buf, &decoded); err != nil {
		t.Fatalf("ReadJSONFrame() error = %v", err)
	}
	if decoded["blob"] != payload["blob"] {
		t.Fatalf("decoded payload mismatch: got %d bytes want %d bytes", len(decoded["blob"]), len(payload["blob"]))
	}
}

func TestWriteJSONFrameRejectsPayloadAboveLimit(t *testing.T) {
	payload := map[string]string{
		"blob": strings.Repeat("a", MaxJSONFrameSize),
	}

	var buf bytes.Buffer
	if err := WriteJSONFrame(&buf, payload); err == nil {
		t.Fatal("WriteJSONFrame() expected error for oversized payload")
	}
}
