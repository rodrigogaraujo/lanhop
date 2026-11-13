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
	WindowFrames  = 8 * SliceFrames
	bytesPerFrame = 4
)

var (
	ErrEmpty       = errors.New("clipboard: the copy is empty")
	ErrTooLarge    = errors.New("clipboard: the copy is larger than 1 MiB")
	ErrNotText     = errors.New("clipboard: the copy is not UTF-8 text")
	ErrPrivate     = errors.New("clipboard: the program that made the copy marked it private")
	ErrLength      = errors.New("clipboard: a copy was announced with a negative length or one over 1 MiB")
	ErrUnannounced = errors.New("clipboard: copy data arrived outside a copy")
	ErrAck         = errors.New("clipboard: the other machine acknowledged frames that were never sent")
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

func Withdrawal() event.Event {
	return event.Clipboard(0)
}

func IsCopyFrame(frame event.Event) bool {
	return frame.IsControl() && (frame.Code == event.ControlClipboard || frame.Code == event.ControlClipboardData)
}

type Progress int

const (
	Collecting Progress = iota
	Arrived
	Withdrawn
)

type Assembler struct {
	text   []byte
	length int
	open   bool
	taken  uint32
	acked  uint32
}

func (a *Assembler) Take(frame event.Event) (text string, progress Progress, err error) {
	if !IsCopyFrame(frame) {
		return "", Collecting, nil
	}
	a.taken++
	if frame.Code == event.ControlClipboard {
		return a.start(frame.Value)
	}
	return a.add(frame.Value)
}

func (a *Assembler) Ack() (event.Event, bool) {
	unacknowledged := a.taken - a.acked
	if unacknowledged == 0 || (a.open && unacknowledged < SliceFrames) {
		return event.Event{}, false
	}
	a.acked = a.taken
	return event.ClipboardAck(int32(a.taken)), true
}

func (a *Assembler) start(length int32) (string, Progress, error) {
	a.drop()
	switch {
	case length == 0:
		return "", Withdrawn, nil
	case length < 0 || length > Limit:
		return "", Collecting, fmt.Errorf("%w: %d bytes", ErrLength, length)
	}
	a.length = int(length)
	a.text = make([]byte, 0, min(dataFramesFor(a.length), SliceFrames)*bytesPerFrame)
	a.open = true
	return "", Collecting, nil
}

func (a *Assembler) add(chunk int32) (string, Progress, error) {
	if !a.open {
		return "", Collecting, ErrUnannounced
	}
	a.text = binary.BigEndian.AppendUint32(a.text, uint32(chunk))
	if len(a.text) < a.length {
		return "", Collecting, nil
	}
	text := a.text[:a.length]
	a.drop()
	if !utf8.Valid(text) {
		return "", Collecting, ErrNotText
	}
	return string(text), Arrived, nil
}

func (a *Assembler) drop() {
	a.text, a.length, a.open = nil, 0, false
}

type Window struct {
	sent  uint32
	acked uint32
}

func (w *Window) Room() int {
	return WindowFrames - int(w.sent-w.acked)
}

func (w *Window) Sent(frames int) {
	w.sent += uint32(frames)
}

func (w *Window) Acked(framesRead int32) error {
	read := uint32(framesRead)
	if read-w.acked > w.sent-w.acked {
		return fmt.Errorf("%w: %d read, %d sent", ErrAck, read, w.sent)
	}
	w.acked = read
	return nil
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
