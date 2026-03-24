package offlinestore

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxFrameSize 單幀 JSON 上限，防止惡意長度（需容納 store-node 大 fetch 響應）。
const MaxFrameSize = 128 << 20

var (
	// ErrFrameTooLarge 長度欄位超過 MaxFrameSize
	ErrFrameTooLarge = errors.New("offlinestore: frame exceeds MaxFrameSize")
)

// StreamCodec 實現文檔第 4 節：4 字節 big-endian 長度 + JSON bytes
type StreamCodec struct{}

// WriteFrame 將 v 序列化為 JSON 後寫入 [uint32 BE len][payload]
func (StreamCodec) WriteFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("offlinestore: marshal frame: %w", err)
	}
	if len(b) > MaxFrameSize {
		return ErrFrameTooLarge
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("offlinestore: write length: %w", err)
	}
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("offlinestore: write payload: %w", err)
	}
	return nil
}

// ReadFrame 讀取一幀，返回原始 JSON bytes（不反序列化）
func (StreamCodec) ReadFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("offlinestore: read length: %w", err)
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if uint64(n) > uint64(MaxFrameSize) {
		return nil, ErrFrameTooLarge
	}
	if n == 0 {
		return []byte{}, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("offlinestore: read payload: %w", err)
	}
	return buf, nil
}

// ReadFrameJSON 讀取一幀並反序列化到 v
func (c StreamCodec) ReadFrameJSON(r io.Reader, v any) error {
	b, err := c.ReadFrame(r)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("offlinestore: unmarshal frame: %w", err)
	}
	return nil
}
