package protocol

import "encoding/json"

const (
	TCPDataChunkBytes = 32 * 1024
)

// MarshalPaddedOnionPayload preserves the existing call sites but now just
// marshals the payload directly without any size bucketing or padding.
func MarshalPaddedOnionPayload(payload OnionPayload) ([]byte, error) {
	payload.Pad = ""
	return json.Marshal(payload)
}
