package tunnel

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// MaxJSONFrameSize limits a single JSON frame payload. Chat/file payloads are
// base64-encoded inside JSON, so keep enough headroom for common multi-MB
// attachments while still bounding per-frame memory usage.
const MaxJSONFrameSize = 16 << 20

func WriteJSONFrame(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data) > MaxJSONFrameSize {
		return fmt.Errorf("frame too large: %d", len(data))
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(data)))
	if _, err := w.Write(size[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func ReadJSONFrame(r io.Reader, v any) error {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > MaxJSONFrameSize {
		return fmt.Errorf("invalid frame size %d", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
