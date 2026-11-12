//go:build darwin && cgo

package macos

import (
	"slices"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/keymap"
)

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
	tapF19 := []event.Event{event.Key(linuxF19, event.KeyPressed), event.Sync(), event.Key(linuxF19, event.KeyReleased), event.Sync()}

	if err := capture.Take(); err != nil {
		t.Fatalf("Take: %v", err)
	}
	replay(t, replayed, tapF19...)
	whileTaken := captured(t, capture, len(tapF19))
	leaked := replayedWithin(afterTheCapture, 300*time.Millisecond)

	if err := capture.GiveBack(); err != nil {
		t.Fatalf("GiveBack: %v", err)
	}
	replay(t, replayed, tapF19...)
	afterGivingBack := captured(t, capture, len(tapF19))
	passedOn := replayedWithin(afterTheCapture, 300*time.Millisecond)

	if !slices.Equal(whileTaken, tapF19) || !slices.Equal(afterGivingBack, tapF19) {
		t.Errorf("captured %+v while taken and %+v after, want %+v both times", whileTaken, afterGivingBack, tapF19)
	}
	for _, seen := range leaked {
		if seen.KeyCode == macOSF19 {
			t.Errorf("while the input was taken the rest of the system still saw F19, type %d", seen.Type)
		} else {
			t.Logf("an event this test did not post arrived: type %d, key %d, flags %#x", seen.Type, seen.KeyCode, uint64(seen.Flags))
		}
	}
	passedF19 := 0
	for _, seen := range passedOn {
		if seen.KeyCode == macOSF19 {
			passedF19++
		}
	}
	if passedF19 != 2 {
		t.Errorf("after giving the input back the rest of the system saw F19 %d times, want the key down and up", passedF19)
	}
}

func TestClosedCaptureEndsItsEvents(t *testing.T) {
	if !CanListen() || !CanPost() {
		t.Skip("this program has no Accessibility permission, see docs/setup-macos.md")
	}
	capture, err := startCapture(keymap.Translator{}, onlyWhatLanhopPosts)
	if err != nil {
		t.Fatalf("startCapture: %v", err)
	}
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
	if capture.taken.Load() {
		t.Error("a closed capture still keeps the input taken")
	}
}
