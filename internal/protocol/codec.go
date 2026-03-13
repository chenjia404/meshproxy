package protocol

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	network "github.com/libp2p/go-libp2p/core/network"
)

// WriteFrame encodes a frame and writes it to the given writer using
// a simple length-prefixed binary format.
//
// Frame layout:
//   uint32 length (of the rest of the frame)
//   uint16 message type
//   uint16 reserved (currently unused)
//   uint32 circuit id length
//   bytes circuit id
//   uint32 stream id length
//   bytes stream id
//   uint32 payload length
//   bytes payload json
func WriteFrame(w io.Writer, frame Frame) error {
	cid := []byte(frame.CircuitID)
	sid := []byte(frame.StreamID)
	payload := frame.PayloadJSON

	// compute total length (excluding the length field itself)
	length := 2 + 2 + 4 + len(cid) + 4 + len(sid) + 4 + len(payload)

	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(length))
	if _, err := w.Write(buf); err != nil {
		return err
	}

	// type
	buf = make([]byte, 2)
	binary.BigEndian.PutUint16(buf, uint16(frame.Type))
	if _, err := w.Write(buf); err != nil {
		return err
	}

	// reserved
	if _, err := w.Write(buf[:2]); err != nil {
		return err
	}

	// circuit id
	buf4 := make([]byte, 4)
	binary.BigEndian.PutUint32(buf4, uint32(len(cid)))
	if _, err := w.Write(buf4); err != nil {
		return err
	}
	if _, err := w.Write(cid); err != nil {
		return err
	}

	// stream id
	binary.BigEndian.PutUint32(buf4, uint32(len(sid)))
	if _, err := w.Write(buf4); err != nil {
		return err
	}
	if _, err := w.Write(sid); err != nil {
		return err
	}

	// payload
	binary.BigEndian.PutUint32(buf4, uint32(len(payload)))
	if _, err := w.Write(buf4); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}

	return nil
}

// ReadFrame reads and decodes a single frame from the reader.
func ReadFrame(r io.Reader) (Frame, error) {
	var f Frame

	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return f, err
	}
	length := binary.BigEndian.Uint32(header)
	if length == 0 {
		return f, fmt.Errorf("invalid frame length 0")
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return f, err
	}

	offset := 0
	if len(body) < offset+2 {
		return f, fmt.Errorf("truncated frame")
	}
	f.Type = MessageType(binary.BigEndian.Uint16(body[offset : offset+2]))
	offset += 4 // skip type + reserved

	if len(body) < offset+4 {
		return f, fmt.Errorf("truncated frame (cid len)")
	}
	cidLen := int(binary.BigEndian.Uint32(body[offset : offset+4]))
	offset += 4
	if len(body) < offset+cidLen {
		return f, fmt.Errorf("truncated frame (cid)")
	}
	f.CircuitID = string(body[offset : offset+cidLen])
	offset += cidLen

	if len(body) < offset+4 {
		return f, fmt.Errorf("truncated frame (sid len)")
	}
	sidLen := int(binary.BigEndian.Uint32(body[offset : offset+4]))
	offset += 4
	if len(body) < offset+sidLen {
		return f, fmt.Errorf("truncated frame (sid)")
	}
	f.StreamID = string(body[offset : offset+sidLen])
	offset += sidLen

	if len(body) < offset+4 {
		return f, fmt.Errorf("truncated frame (payload len)")
	}
	payloadLen := int(binary.BigEndian.Uint32(body[offset : offset+4]))
	offset += 4
	if len(body) < offset+payloadLen {
		return f, fmt.Errorf("truncated frame (payload)")
	}
	f.PayloadJSON = body[offset : offset+payloadLen]

	return f, nil
}

// Helper functions to encode specific message types into frames.

func NewCreateFrame(circuitID string, msg CreateCircuit) (Frame, error) {
	p, err := json.Marshal(msg)
	if err != nil {
		return Frame{}, err
	}
	return Frame{
		Type:        MsgTypeCreate,
		CircuitID:   circuitID,
		StreamID:    "",
		PayloadJSON: p,
	}, nil
}

func NewExtendFrame(circuitID string, msg Extend) (Frame, error) {
	p, err := json.Marshal(msg)
	if err != nil {
		return Frame{}, err
	}
	return Frame{
		Type:        MsgTypeExtend,
		CircuitID:   circuitID,
		StreamID:    "",
		PayloadJSON: p,
	}, nil
}

func NewBeginTCPFrame(circuitID, streamID string, msg BeginTCP) (Frame, error) {
	p, err := json.Marshal(msg)
	if err != nil {
		return Frame{}, err
	}
	return Frame{
		Type:        MsgTypeBeginTCP,
		CircuitID:   circuitID,
		StreamID:    streamID,
		PayloadJSON: p,
	}, nil
}

// NewConnectedFrame creates a frame carrying a Connected message.
func NewConnectedFrame(circuitID, streamID string, msg Connected) (Frame, error) {
	p, err := json.Marshal(msg)
	if err != nil {
		return Frame{}, err
	}
	return Frame{
		Type:        MsgTypeConnected,
		CircuitID:   circuitID,
		StreamID:    streamID,
		PayloadJSON: p,
	}, nil
}

func NewDataFrame(circuitID, streamID string, payload []byte) (Frame, error) {
	p, err := json.Marshal(DataCell{Payload: payload})
	if err != nil {
		return Frame{}, err
	}
	return Frame{
		Type:        MsgTypeData,
		CircuitID:   circuitID,
		StreamID:    streamID,
		PayloadJSON: p,
	}, nil
}

func NewEndFrame(circuitID, streamID, reason string) (Frame, error) {
	p, err := json.Marshal(EndCell{Reason: reason})
	if err != nil {
		return Frame{}, err
	}
	return Frame{
		Type:        MsgTypeEnd,
		CircuitID:   circuitID,
		StreamID:    streamID,
		PayloadJSON: p,
	}, nil
}

// NewKeyExchangeInitFrame creates a frame carrying a key exchange init payload.
func NewKeyExchangeInitFrame(circuitID string, payload []byte) (Frame, error) {
	p, err := json.Marshal(KeyExchangeInit{Payload: payload})
	if err != nil {
		return Frame{}, err
	}
	return Frame{
		Type:        MsgTypeKeyExchangeInit,
		CircuitID:   circuitID,
		StreamID:    "",
		PayloadJSON: p,
	}, nil
}

// NewKeyExchangeRespFrame creates a frame carrying a key exchange response payload.
func NewKeyExchangeRespFrame(circuitID string, payload []byte) (Frame, error) {
	p, err := json.Marshal(KeyExchangeResp{Payload: payload})
	if err != nil {
		return Frame{}, err
	}
	return Frame{
		Type:        MsgTypeKeyExchangeResp,
		CircuitID:   circuitID,
		StreamID:    "",
		PayloadJSON: p,
	}, nil
}

// NewOnionDataFrame wraps an OnionCell into a DATA-like frame. The actual
// encryption is handled at a higher level; here we only marshal the structure.
func NewOnionDataFrame(circuitID, streamID string, cell OnionCell) (Frame, error) {
	p, err := json.Marshal(cell)
	if err != nil {
		return Frame{}, err
	}
	return Frame{
		Type:        MsgTypeOnionData,
		CircuitID:   circuitID,
		StreamID:    streamID,
		PayloadJSON: p,
	}, nil
}

// StartStreamReader continuously reads frames from the given libp2p stream
// and passes them to the handler until context is cancelled or the stream closes.
func StartStreamReader(ctx context.Context, s network.Stream, handler func(Frame)) {
	go func() {
		defer s.Close()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			frame, err := ReadFrame(s)
			if err != nil {
				if err != io.EOF {
					// In real system, we might hook logger here.
				}
				return
			}
			handler(frame)
		}
	}()
}

