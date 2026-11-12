//go:build darwin && cgo

package macos

import (
	"slices"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/keymap"
)

const (
	linuxF18 uint16 = 188
	macOSF18 uint16 = 0x4F
)

func waitUntilNothingArrives(tap *Tap) {
	for len(replayedWithin(tap, 300*time.Millisecond)) > 0 {
	}
}

func onlyWhatLanhopPosts(seen Observation) bool {
	return seen.Marker == Marker
}

func captureOfReplayedInput(t *testing.T) (*Replay, *Capture) {
	t.Helper()
	replayed, tap := observeRealInput(t)
	tap.Close()
	capture, err := startCapture(keymap.Translator{}, onlyWhatLanhopPosts)
	if err != nil {
		t.Fatalf("startCapture: %v", err)
	}
	t.Cleanup(func() { capture.Close() })
	return replayed, capture
}

func captured(t *testing.T, capture *Capture, count int) []event.Event {
	t.Helper()
	got := make([]event.Event, 0, count)
	deadline := time.After(observePatience)
	for len(got) < count {
		select {
		case next, open := <-capture.Events():
			if !open {
				t.Fatalf("the capture ended after %+v", got)
			}
			got = append(got, next)
		case <-deadline:
			t.Fatalf("captured only %+v, want %d events", got, count)
		}
	}
	return got
}

func TestCaptureTurnsWhatTheSystemDeliversBackIntoTheSameInput(t *testing.T) {
	replayed, capture := captureOfReplayedInput(t)
	typed := []event.Event{
		event.Key(linuxLeftShift, event.KeyPressed), event.Sync(),
		event.Key(linuxF19, event.KeyPressed), event.Sync(),
		event.Key(linuxF19, event.KeyReleased), event.Sync(),
		event.Key(linuxLeftShift, event.KeyReleased), event.Sync(),
		event.Relative(event.RelX, 3), event.Relative(event.RelY, 2), event.Sync(),
		event.Relative(event.RelX, -3), event.Relative(event.RelY, -2), event.Sync(),
		event.Relative(event.RelWheel, 1), event.Sync(),
		event.Relative(event.RelWheel, -1), event.Sync(),
	}

	replay(t, replayed, typed...)

	if got := captured(t, capture, len(typed)); !slices.Equal(got, typed) {
		t.Errorf("captured %+v, want %+v", got, typed)
	}
}

func TestTakenCaptureKeepsItsInputFromTheRestOfTheSystem(t *testing.T) {
	replayed, capture := captureOfReplayedInput(t)
	afterTheCapture, err := startTap(seesInputLast, nil)
	if err != nil {
		t.Fatalf("startTap: %v", err)
	}
	defer afterTheCapture.Close()
	waitUntilNothingArrives(afterTheCapture)
	tapF18 := []event.Event{event.Key(linuxF18, event.KeyPressed), event.Sync(), event.Key(linuxF18, event.KeyReleased), event.Sync()}

	if err := capture.Take(); err != nil {
		t.Fatalf("Take: %v", err)
	}
	replay(t, replayed, tapF18...)
	whileTaken := captured(t, capture, len(tapF18))
	leaked := replayedWithin(afterTheCapture, 300*time.Millisecond)

	if err := capture.GiveBack(); err != nil {
		t.Fatalf("GiveBack: %v", err)
	}
	replay(t, replayed, tapF18...)
	afterGivingBack := captured(t, capture, len(tapF18))
	passedOn := replayedWithin(afterTheCapture, 300*time.Millisecond)

	if !slices.Equal(whileTaken, tapF18) || !slices.Equal(afterGivingBack, tapF18) {
		t.Errorf("captured %+v while taken and %+v after, want %+v both times", whileTaken, afterGivingBack, tapF18)
	}
	for _, seen := range leaked {
		if seen.KeyCode == macOSF18 {
			t.Errorf("while the input was taken the rest of the system still saw F18, type %d", seen.Type)
		} else {
			t.Logf("an event this test did not post arrived: type %d, key %d, flags %#x", seen.Type, seen.KeyCode, uint64(seen.Flags))
		}
	}
	passedF18 := 0
	for _, seen := range passedOn {
		if seen.KeyCode == macOSF18 {
			passedF18++
		}
	}
	if passedF18 != 2 {
		t.Errorf("after giving the input back the rest of the system saw F18 %d times, want the key down and up", passedF18)
	}
}

func TestClosedCaptureEndsItsEvents(t *testing.T) {
	_, capture := captureOfReplayedInput(t)
	if err := capture.Take(); err != nil {
		t.Fatalf("Take: %v", err)
	}
	if err := capture.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := capture.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}

	select {
	case _, open := <-capture.Events():
		if open {
			t.Error("a closed capture delivered an event")
		}
	case <-time.After(observePatience):
		t.Fatal("the events of a closed capture never ended")
	}
	if capture.hold.keeps(Observation{Type: TypeKeyDown, KeyCode: macOSF18, Marker: Marker}) {
		t.Error("a closed capture still keeps the input taken")
	}
}

func TestCursorStaysWhereItWasWhileTheInputIsTaken(t *testing.T) {
	replayed, capture := captureOfReplayedInput(t)
	start := Quartz{}.Cursor()
	defer Quartz{}.PlaceCursor(start)

	if err := capture.Take(); err != nil {
		t.Fatalf("Take: %v", err)
	}
	replay(t, replayed, event.Relative(event.RelX, 40), event.Relative(event.RelY, 25), event.Sync())
	moved := captured(t, capture, 3)
	time.Sleep(100 * time.Millisecond)
	whileTaken := Quartz{}.Cursor()

	if err := capture.GiveBack(); err != nil {
		t.Fatalf("GiveBack: %v", err)
	}
	replay(t, replayed, event.Relative(event.RelX, 40), event.Relative(event.RelY, 25), event.Sync())
	captured(t, capture, 3)
	time.Sleep(100 * time.Millisecond)
	afterGivingBack := Quartz{}.Cursor()

	if want := []event.Event{event.Relative(event.RelX, 40), event.Relative(event.RelY, 25), event.Sync()}; !slices.Equal(moved, want) {
		t.Errorf("captured %+v while taken, want the movement %+v", moved, want)
	}
	if !withinOnePoint(whileTaken, start) {
		t.Errorf("the cursor went to %+v while the input was taken, want it to stay at %+v", whileTaken, start)
	}
	if withinOnePoint(afterGivingBack, start) {
		t.Errorf("the cursor stayed at %+v after the input was given back, want it to follow the movement", start)
	}
}

func withinOnePoint(a, b Point) bool {
	return abs(a.X-b.X) <= 1 && abs(a.Y-b.Y) <= 1
}
