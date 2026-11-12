package wire

import (
	"bytes"
	"errors"
	"io"
	"math"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

func TestWriteThenReadReturnsTheSameEvents(t *testing.T) {
	sent := []event.Event{
		event.Key(event.KeyA, event.KeyPressed),
		event.Sync(),
		event.Relative(event.RelX, 7),
		event.Relative(event.RelY, -3),
		event.Relative(event.RelWheel, math.MinInt32),
		{Type: math.MaxUint16, Code: math.MaxUint16, Value: math.MaxInt32},
	}

	var stream bytes.Buffer
	if err := Write(&stream, sent...); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, want := stream.Len(), len(sent)*FrameSize; got != want {
		t.Fatalf("stream has %d bytes, want %d", got, want)
	}

	for i, want := range sent {
		got, err := Read(&stream)
		if err != nil {
			t.Fatalf("Read event %d: %v", i, err)
		}
		if got != want {
			t.Errorf("event %d = %+v, want %+v", i, got, want)
		}
	}
	if _, err := Read(&stream); !errors.Is(err, io.EOF) {
		t.Errorf("Read after last event = %v, want io.EOF", err)
	}
}

func TestFrameLayoutIsBigEndianTypeCodeValue(t *testing.T) {
	got := Append(nil, event.Event{Type: 0x0102, Code: 0x0304, Value: 0x05060708})
	want := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	if !bytes.Equal(got, want) {
		t.Errorf("frame = % x, want % x", got, want)
	}
}

func TestDecodeRejectsFramesOfTheWrongSize(t *testing.T) {
	for _, size := range []int{0, FrameSize - 1, FrameSize + 1} {
		if _, err := Decode(make([]byte, size)); !errors.Is(err, ErrFrameSize) {
			t.Errorf("Decode of %d bytes = %v, want ErrFrameSize", size, err)
		}
	}
}

func TestReadReportsATruncatedFrame(t *testing.T) {
	truncated := bytes.NewReader(make([]byte, FrameSize-1))
	if _, err := Read(truncated); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("Read = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestWriteWithoutEventsWritesNothing(t *testing.T) {
	var stream bytes.Buffer
	if err := Write(&stream); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if stream.Len() != 0 {
		t.Errorf("stream has %d bytes, want 0", stream.Len())
	}
}
