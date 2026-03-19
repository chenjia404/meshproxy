package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"

	proto "github.com/golang/protobuf/proto"

	"github.com/chenjia404/meshproxy/internal/meshserver/sessionv1"
)

const currentEnvelopeVersion = 1

func ReadEnvelope(r io.Reader) (*sessionv1.Envelope, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, err
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	var env sessionv1.Envelope
	if err := proto.Unmarshal(payload, &env); err != nil {
		return nil, fmt.Errorf("unmarshal envelope: %w", err)
	}
	return &env, nil
}

func WriteEnvelope(w io.Writer, env *sessionv1.Envelope) error {
	payload, err := proto.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(payload))); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func MarshalBody(msg proto.Message) ([]byte, error) {
	return proto.Marshal(msg)
}

func UnmarshalBody(data []byte, msg proto.Message) error {
	return proto.Unmarshal(data, msg)
}

func NewEnvelope(msgType sessionv1.MsgType, requestID string, body proto.Message) (*sessionv1.Envelope, error) {
	payload, err := MarshalBody(body)
	if err != nil {
		return nil, err
	}
	return &sessionv1.Envelope{
		Version:     currentEnvelopeVersion,
		MsgType:     msgType,
		RequestId:   requestID,
		TimestampMs: uint64(time.Now().UTC().UnixMilli()),
		Body:        payload,
	}, nil
}
