package wire

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const FrameSize = 8

var ErrFrameSize = errors.New("wire: frame must be exactly 8 bytes")

func Append(dst []byte, e event.Event) []byte {
	dst = binary.BigEndian.AppendUint16(dst, e.Type)
	dst = binary.BigEndian.AppendUint16(dst, e.Code)
	return binary.BigEndian.AppendUint32(dst, uint32(e.Value))
}

func Decode(frame []byte) (event.Event, error) {
	if len(frame) != FrameSize {
		return event.Event{}, ErrFrameSize
	}
	return event.Event{
		Type:  binary.BigEndian.Uint16(frame[0:2]),
		Code:  binary.BigEndian.Uint16(frame[2:4]),
		Value: int32(binary.BigEndian.Uint32(frame[4:8])),
	}, nil
}

func Write(w io.Writer, events ...event.Event) error {
	if len(events) == 0 {
		return nil
	}
	buffer := make([]byte, 0, len(events)*FrameSize)
	for _, e := range events {
		buffer = Append(buffer, e)
	}
	_, err := w.Write(buffer)
	return err
}

func Read(r io.Reader) (event.Event, error) {
	var frame [FrameSize]byte
	if _, err := io.ReadFull(r, frame[:]); err != nil {
		return event.Event{}, err
	}
	return Decode(frame[:])
}
