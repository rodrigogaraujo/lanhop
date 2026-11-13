package clipboard

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const (
	Limit         = 1 << 20
	SliceFrames   = 512
	bytesPerFrame = 4
)

var (
	ErrEmpty       = errors.New("clipboard: the copy is empty")
	ErrTooLarge    = errors.New("clipboard: the copy is larger than 1 MiB")
	ErrNotText     = errors.New("clipboard: the copy is not UTF-8 text")
	ErrLength      = errors.New("clipboard: a copy was announced with a length outside 1 byte to 1 MiB")
	ErrUnannounced = errors.New("clipboard: copy data arrived outside a copy")
)

func Slices(text string) ([][]event.Event, error) {
	if err := sendable(text); err != nil {
		return nil, err
	}
	frames := make([]event.Event, 0, 1+dataFramesFor(len(text)))
	frames = append(frames, event.Clipboard(int32(len(text))))
	for start := 0; start < len(text); start += bytesPerFrame {
		var chunk [bytesPerFrame]byte
		copy(chunk[:], text[start:])
		frames = append(frames, event.ClipboardData(int32(binary.BigEndian.Uint32(chunk[:]))))
	}
	return inSlices(frames), nil
}

func IsCopyFrame(frame event.Event) bool {
	return frame.IsControl() && (frame.Code == event.ControlClipboard || frame.Code == event.ControlClipboardData)
}

type Assembler struct {
	text   []byte
	length int
	open   bool
}

func (a *Assembler) Take(frame event.Event) (text string, complete bool, err error) {
	if !IsCopyFrame(frame) {
		return "", false, nil
	}
	if frame.Code == event.ControlClipboard {
		return "", false, a.start(frame.Value)
	}
	return a.add(frame.Value)
}

func (a *Assembler) start(length int32) error {
	a.drop()
	if length < 1 || length > Limit {
		return fmt.Errorf("%w: %d bytes", ErrLength, length)
	}
	a.length = int(length)
	a.text = make([]byte, 0, dataFramesFor(a.length)*bytesPerFrame)
	a.open = true
	return nil
}

func (a *Assembler) add(chunk int32) (string, bool, error) {
	if !a.open {
		return "", false, ErrUnannounced
	}
	a.text = binary.BigEndian.AppendUint32(a.text, uint32(chunk))
	if len(a.text) < a.length {
		return "", false, nil
	}
	text := a.text[:a.length]
	a.drop()
	if !utf8.Valid(text) {
		return "", false, ErrNotText
	}
	return string(text), true, nil
}

func (a *Assembler) drop() {
	*a = Assembler{}
}

func sendable(text string) error {
	switch {
	case text == "":
		return ErrEmpty
	case len(text) > Limit:
		return fmt.Errorf("%w: %d bytes", ErrTooLarge, len(text))
	case !utf8.ValidString(text):
		return ErrNotText
	}
	return nil
}

func dataFramesFor(length int) int {
	return (length + bytesPerFrame - 1) / bytesPerFrame
}

func inSlices(frames []event.Event) [][]event.Event {
	slices := make([][]event.Event, 0, (len(frames)+SliceFrames-1)/SliceFrames)
	for len(frames) > SliceFrames {
		slices = append(slices, frames[:SliceFrames:SliceFrames])
		frames = frames[SliceFrames:]
	}
	return append(slices, frames)
}
