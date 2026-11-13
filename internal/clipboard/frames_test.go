package clipboard

import (
	"encoding/binary"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

func framesOf(t *testing.T, text string) []event.Event {
	t.Helper()
	parts, err := Slices(text)
	if err != nil {
		t.Fatalf("Slices(%d bytes): %v", len(text), err)
	}
	return slices.Concat(parts...)
}

const withdrawn = "(withdrawn)"

func assemble(t *testing.T, frames []event.Event) []string {
	t.Helper()
	var a Assembler
	var texts []string
	for i, frame := range frames {
		text, progress, err := a.Take(frame)
		if err != nil {
			t.Fatalf("Take frame %d (%+v): %v", i, frame, err)
		}
		switch progress {
		case Arrived:
			texts = append(texts, text)
		case Withdrawn:
			texts = append(texts, withdrawn)
		}
	}
	return texts
}

func TestTextsComeBackTheSameAfterTheirFrames(t *testing.T) {
	texts := map[string]string{
		"1 byte":                    "a",
		"2 bytes":                   "ab",
		"3 bytes":                   "abc",
		"4 bytes":                   "abcd",
		"5 bytes":                   "abcde",
		"6 bytes":                   "abcdef",
		"7 bytes":                   "abcdefg",
		"8 bytes":                   "abcdefgh",
		"9 bytes":                   "abcdefghi",
		"accents":                   "olá, ação",
		"japanese":                  "日本語のテキスト",
		"emoji":                     "🙂👍🏽",
		"tabs and several lines":    "line one\r\nline two\n\tindented\n",
		"zero bytes inside":         "a\x00b",
		"the limit":                 strings.Repeat("x", Limit),
		"the limit ending in emoji": strings.Repeat("x", Limit-4) + "🙂",
	}
	for name, text := range texts {
		t.Run(name, func(t *testing.T) {
			got := assemble(t, framesOf(t, text))
			if len(got) != 1 {
				t.Fatalf("got %d complete copies, want 1", len(got))
			}
			if got[0] != text {
				t.Errorf("got %d bytes back, want the same %d bytes", len(got[0]), len(text))
			}
		})
	}
}

func TestACopyIsItsLengthThenFourBytesPerFramePaddedWithZeros(t *testing.T) {
	got := framesOf(t, "abcde")
	want := []event.Event{
		event.Clipboard(5),
		event.ClipboardData(0x61626364),
		event.ClipboardData(0x65000000),
	}
	if !slices.Equal(got, want) {
		t.Errorf("frames = %+v, want %+v", got, want)
	}
}

func TestACopyIsCompleteOnlyAtItsLastFrame(t *testing.T) {
	frames := framesOf(t, "abcdefghi")
	var a Assembler
	for i, frame := range frames {
		_, progress, err := a.Take(frame)
		if err != nil {
			t.Fatalf("Take frame %d: %v", i, err)
		}
		if last := i == len(frames)-1; (progress == Arrived) != last {
			t.Errorf("frame %d of %d: progress = %v", i+1, len(frames), progress)
		}
	}
}

func TestCopiesThatCannotBeSentAreRefused(t *testing.T) {
	tests := []struct {
		name string
		text string
		want error
	}{
		{"empty", "", ErrEmpty},
		{"one byte over the limit", strings.Repeat("x", Limit+1), ErrTooLarge},
		{"not UTF-8", "abc\xff", ErrNotText},
		{"half of a character", "🙂"[:2], ErrNotText},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parts, err := Slices(test.text)
			if !errors.Is(err, test.want) {
				t.Errorf("Slices error = %v, want %v", err, test.want)
			}
			if parts != nil {
				t.Errorf("Slices returned %d slices with the error", len(parts))
			}
		})
	}
}

func TestACopyIsSplitIntoSlicesOfAtMost512Frames(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		wantSlices int
	}{
		{"one frame of data", "a", 1},
		{"exactly one slice", strings.Repeat("x", (SliceFrames-1)*4), 1},
		{"one frame more than a slice", strings.Repeat("x", (SliceFrames-1)*4+1), 2},
		{"the limit", strings.Repeat("x", Limit), (1 + Limit/4 + SliceFrames - 1) / SliceFrames},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parts, err := Slices(test.text)
			if err != nil {
				t.Fatalf("Slices: %v", err)
			}
			if len(parts) != test.wantSlices {
				t.Errorf("got %d slices, want %d", len(parts), test.wantSlices)
			}
			for i, part := range parts {
				if len(part) == 0 || len(part) > SliceFrames {
					t.Errorf("slice %d has %d frames, want 1 to %d", i, len(part), SliceFrames)
				}
			}
			for i := 0; i+1 < len(parts); i++ {
				next := parts[i+1][0]
				_ = append(parts[i], event.Ping())
				if parts[i+1][0] != next {
					t.Errorf("appending to slice %d changed slice %d", i, i+1)
				}
			}
		})
	}
}

func TestFramesMixedWithInputAndPingsStillGiveTheText(t *testing.T) {
	copyFrames := framesOf(t, "hello, world")
	var mixed []event.Event
	for _, frame := range copyFrames {
		mixed = append(mixed, frame, event.Relative(event.RelX, 3), event.Sync(), event.Ping(), event.Enter())
	}

	got := assemble(t, mixed)

	if !slices.Equal(got, []string{"hello, world"}) {
		t.Errorf("copies = %q, want one copy %q", got, "hello, world")
	}
}

func TestANewCopyDropsOneThatHasNotFinished(t *testing.T) {
	first := framesOf(t, "first copy, never finished")
	frames := append(slices.Clone(first[:3]), framesOf(t, "second")...)

	got := assemble(t, frames)

	if !slices.Equal(got, []string{"second"}) {
		t.Errorf("copies = %q, want only %q", got, "second")
	}
}

func TestDataOutsideACopyIsRefusedAndTheNextCopyStillArrives(t *testing.T) {
	var a Assembler
	if _, _, err := a.Take(event.ClipboardData(0x61626364)); !errors.Is(err, ErrUnannounced) {
		t.Errorf("data before any copy: error = %v, want %v", err, ErrUnannounced)
	}

	var got []string
	for _, frame := range framesOf(t, "ok") {
		if text, progress, err := a.Take(frame); err != nil {
			t.Fatalf("Take: %v", err)
		} else if progress == Arrived {
			got = append(got, text)
		}
	}
	if !slices.Equal(got, []string{"ok"}) {
		t.Fatalf("copies = %q, want %q", got, "ok")
	}

	if _, _, err := a.Take(event.ClipboardData(0)); !errors.Is(err, ErrUnannounced) {
		t.Errorf("data after a complete copy: error = %v, want %v", err, ErrUnannounced)
	}
}

func TestAnnouncedLengthsOutsideTheLimitAreRefused(t *testing.T) {
	for _, length := range []int32{-1, Limit + 1, -Limit, math.MinInt32, math.MaxInt32} {
		var a Assembler
		if _, _, err := a.Take(event.Clipboard(length)); !errors.Is(err, ErrLength) {
			t.Errorf("length %d: error = %v, want %v", length, err, ErrLength)
		}
		if _, _, err := a.Take(event.ClipboardData(0x61626364)); !errors.Is(err, ErrUnannounced) {
			t.Errorf("data after length %d: error = %v, want %v", length, err, ErrUnannounced)
		}
	}
}

func TestACopyThatIsNotUTF8IsRefusedWhenItArrives(t *testing.T) {
	var a Assembler
	a.Take(event.Clipboard(1))

	text, progress, err := a.Take(event.ClipboardData(int32(binary.BigEndian.Uint32([]byte{0xff, 0, 0, 0}))))

	if !errors.Is(err, ErrNotText) || progress != Collecting || text != "" {
		t.Errorf("Take = %q, %v, %v, want no copy and %v", text, progress, err, ErrNotText)
	}
}

func TestOnlyTheTwoCopyFramesAreCopyFrames(t *testing.T) {
	tests := []struct {
		frame event.Event
		want  bool
	}{
		{event.Clipboard(1), true},
		{event.ClipboardData(0), true},
		{Withdrawal(), true},
		{event.ClipboardAck(1), false},
		{event.ClipboardUnchanged(), false},
		{event.Features(event.FeatureClipboard), false},
		{event.Hello(), false},
		{event.Ping(), false},
		{event.Enter(), false},
		{event.Leave(), false},
		{event.Key(event.KeyA, event.KeyPressed), false},
		{event.Event{Type: event.TypeKey, Code: event.ControlClipboard, Value: 1}, false},
	}
	for _, test := range tests {
		if got := IsCopyFrame(test.frame); got != test.want {
			t.Errorf("IsCopyFrame(%+v) = %v, want %v", test.frame, got, test.want)
		}
	}
}

func TestAWithdrawalIsNotACopy(t *testing.T) {
	var a Assembler

	text, progress, err := a.Take(Withdrawal())

	if err != nil || progress != Withdrawn || text != "" {
		t.Errorf("Take(Withdrawal()) = %q, %v, %v, want a withdrawal with no text", text, progress, err)
	}
}

func TestAWithdrawalDropsACopyThatHasNotFinished(t *testing.T) {
	frames := append(slices.Clone(framesOf(t, "unfinished copy")[:2]), Withdrawal())

	if got := assemble(t, frames); !slices.Equal(got, []string{withdrawn}) {
		t.Errorf("copies = %q, want only the withdrawal", got)
	}
}

func TestALengthOutsideTheLimitDropsTheCopyThatWasOpen(t *testing.T) {
	var a Assembler
	open := framesOf(t, "abcdefgh")
	a.Take(open[0])
	a.Take(open[1])

	if _, _, err := a.Take(event.Clipboard(Limit + 1)); !errors.Is(err, ErrLength) {
		t.Fatalf("error = %v, want %v", err, ErrLength)
	}
	if _, _, err := a.Take(open[2]); !errors.Is(err, ErrUnannounced) {
		t.Errorf("the rest of the open copy: error = %v, want %v", err, ErrUnannounced)
	}
}

func TestTheNextCopyArrivesAfterOneThatWasNotUTF8(t *testing.T) {
	var a Assembler
	a.Take(event.Clipboard(1))
	if _, _, err := a.Take(event.ClipboardData(int32(binary.BigEndian.Uint32([]byte{0xff, 0, 0, 0})))); !errors.Is(err, ErrNotText) {
		t.Fatalf("error = %v, want %v", err, ErrNotText)
	}

	var got []string
	for _, frame := range framesOf(t, "fine") {
		if text, progress, err := a.Take(frame); err != nil {
			t.Fatalf("Take: %v", err)
		} else if progress == Arrived {
			got = append(got, text)
		}
	}
	if !slices.Equal(got, []string{"fine"}) {
		t.Errorf("copies = %q, want %q", got, "fine")
	}
}

func TestPaddingAfterTheAnnouncedLengthIsIgnored(t *testing.T) {
	frames := []event.Event{event.Clipboard(1), event.ClipboardData(0x61626364)}

	if got := assemble(t, frames); !slices.Equal(got, []string{"a"}) {
		t.Errorf("copies = %q, want %q", got, "a")
	}
}

func TestAnAnnouncementAloneDoesNotReserveTheWholeLimit(t *testing.T) {
	var a Assembler
	a.Take(event.Clipboard(Limit))

	if got := cap(a.text); got > SliceFrames*bytesPerFrame {
		t.Errorf("an announcement of 1 MiB reserved %d bytes, want at most %d", got, SliceFrames*bytesPerFrame)
	}
}

func acksWhileTaking(t *testing.T, frames []event.Event) []event.Event {
	t.Helper()
	var a Assembler
	var acks []event.Event
	for _, frame := range frames {
		a.Take(frame)
		if ack, due := a.Ack(); due {
			acks = append(acks, ack)
		}
	}
	return acks
}

func TestTheReadingSideAcknowledgesEach512FramesAndTheEndOfACopy(t *testing.T) {
	frames := framesOf(t, strings.Repeat("x", 3000*4))

	acks := acksWhileTaking(t, frames)

	want := []event.Event{
		event.ClipboardAck(512),
		event.ClipboardAck(1024),
		event.ClipboardAck(1536),
		event.ClipboardAck(2048),
		event.ClipboardAck(2560),
		event.ClipboardAck(3001),
	}
	if !slices.Equal(acks, want) {
		t.Errorf("acks = %+v, want %+v", acks, want)
	}
}

func TestEveryCopyFrameIsAcknowledgedEvenWhenItIsRefused(t *testing.T) {
	frames := []event.Event{
		event.ClipboardData(1),
		event.Ping(),
		event.Clipboard(-1),
		Withdrawal(),
		event.Key(event.KeyA, event.KeyPressed),
	}
	frames = append(frames, framesOf(t, "abc")...)

	acks := acksWhileTaking(t, frames)

	want := []event.Event{event.ClipboardAck(1), event.ClipboardAck(2), event.ClipboardAck(3), event.ClipboardAck(5)}
	if !slices.Equal(acks, want) {
		t.Errorf("acks = %+v, want %+v", acks, want)
	}
}

func TestAWindowHoldsAtMostEightSlicesThatWereNotAcknowledged(t *testing.T) {
	var w Window
	if got := w.Room(); got != 8*SliceFrames {
		t.Fatalf("room of a new window = %d, want %d", got, 8*SliceFrames)
	}

	w.Sent(WindowFrames)
	if got := w.Room(); got != 0 {
		t.Errorf("room after sending a whole window = %d, want 0", got)
	}

	if err := w.Acked(SliceFrames); err != nil {
		t.Fatalf("Acked: %v", err)
	}
	if got := w.Room(); got != SliceFrames {
		t.Errorf("room after one slice was acknowledged = %d, want %d", got, SliceFrames)
	}
}

func TestAnAcknowledgementOfFramesNeverSentIsRefused(t *testing.T) {
	var w Window
	w.Sent(10)

	if err := w.Acked(11); !errors.Is(err, ErrAck) {
		t.Errorf("Acked(11) after 10 sent: error = %v, want %v", err, ErrAck)
	}
	if got := w.Room(); got != WindowFrames-10 {
		t.Errorf("room = %d, want %d", got, WindowFrames-10)
	}
}

func TestWindowCountsKeepWorkingWhenTheyWrapAround(t *testing.T) {
	start := uint32(math.MaxUint32 - 10)
	w := Window{sent: start, acked: start}

	w.Sent(100)
	if got := w.Room(); got != WindowFrames-100 {
		t.Fatalf("room = %d, want %d", got, WindowFrames-100)
	}
	if err := w.Acked(int32(start + 100)); err != nil {
		t.Fatalf("Acked across the wrap: %v", err)
	}
	if got := w.Room(); got != WindowFrames {
		t.Errorf("room = %d, want %d", got, WindowFrames)
	}
}

func TestASenderThatWaitsForAcknowledgementsDeliversTheLimitWithinTheWindow(t *testing.T) {
	text := strings.Repeat("y", Limit)
	parts, err := Slices(text)
	if err != nil {
		t.Fatalf("Slices: %v", err)
	}
	var (
		w        Window
		a        Assembler
		onTheWay []event.Event
		got      []string
	)
	deliverOneSlice := func() {
		for _, frame := range onTheWay[:min(SliceFrames, len(onTheWay))] {
			if text, progress, err := a.Take(frame); err != nil {
				t.Fatalf("Take: %v", err)
			} else if progress == Arrived {
				got = append(got, text)
			}
			if ack, due := a.Ack(); due {
				if err := w.Acked(ack.Value); err != nil {
					t.Fatalf("Acked: %v", err)
				}
			}
		}
		onTheWay = onTheWay[min(SliceFrames, len(onTheWay)):]
	}

	for _, part := range parts {
		for stalls := 0; w.Room() < len(part); stalls++ {
			if len(onTheWay) == 0 || stalls > WindowFrames/SliceFrames {
				t.Fatalf("the sender stalls with room %d for a slice of %d frames", w.Room(), len(part))
			}
			deliverOneSlice()
		}
		w.Sent(len(part))
		onTheWay = append(onTheWay, part...)
		if len(onTheWay) > WindowFrames {
			t.Fatalf("%d frames on the way, more than the window of %d", len(onTheWay), WindowFrames)
		}
	}
	for len(onTheWay) > 0 {
		deliverOneSlice()
	}

	if len(got) != 1 || got[0] != text {
		t.Errorf("got %d copies, want the 1 MiB copy once", len(got))
	}
	if w.Room() != WindowFrames {
		t.Errorf("room at the end = %d, want the whole window", w.Room())
	}
}

func TestTheWindowStillHasRoomForASliceWhenACopyWasAbandonedJustBeforeAnAcknowledgement(t *testing.T) {
	leftUnacknowledged := SliceFrames - 1
	if WindowFrames-leftUnacknowledged < SliceFrames {
		t.Errorf("a window of %d frames with %d left unacknowledged has no room for a slice of %d", WindowFrames, leftUnacknowledged, SliceFrames)
	}
}

func TestAnAbandonedCopyAndAWithdrawalLeaveTheWindowWorking(t *testing.T) {
	var (
		w Window
		a Assembler
	)
	deliver := func(frames []event.Event) []string {
		var got []string
		w.Sent(len(frames))
		for _, frame := range frames {
			text, progress, _ := a.Take(frame)
			switch progress {
			case Arrived:
				got = append(got, text)
			case Withdrawn:
				got = append(got, withdrawn)
			}
			if ack, due := a.Ack(); due {
				if err := w.Acked(ack.Value); err != nil {
					t.Fatalf("Acked: %v", err)
				}
			}
		}
		return got
	}

	abandoned := framesOf(t, strings.Repeat("a", 4*WindowFrames))[:3*SliceFrames-1]
	if got := deliver(abandoned); got != nil {
		t.Fatalf("an abandoned copy gave %q", got)
	}
	if got := deliver([]event.Event{Withdrawal()}); !slices.Equal(got, []string{withdrawn}) {
		t.Fatalf("the withdrawal gave %q", got)
	}
	if got := w.Room(); got != WindowFrames {
		t.Errorf("room after the withdrawal = %d, want the whole window", got)
	}
	if got := deliver(framesOf(t, "next")); !slices.Equal(got, []string{"next"}) {
		t.Errorf("the next copy gave %q", got)
	}
}

func TestAWithdrawalTheSenderDidNotCountIsAnAcknowledgementOfFramesNeverSent(t *testing.T) {
	var (
		w Window
		a Assembler
	)
	a.Take(Withdrawal())
	ack, _ := a.Ack()

	if err := w.Acked(ack.Value); !errors.Is(err, ErrAck) {
		t.Errorf("Acked = %v, want %v", err, ErrAck)
	}
}
