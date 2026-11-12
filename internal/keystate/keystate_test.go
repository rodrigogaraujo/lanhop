package keystate

import (
	"slices"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const (
	keyB       uint16 = 48
	leftShift  uint16 = 42
	keyPressed        = event.KeyPressed
)

func observe(tracker *Tracker, events ...event.Event) {
	for _, e := range events {
		tracker.Observe(e)
	}
}

func TestReleaseAllReleasesEveryHeldKeyAndButtonThenSyncs(t *testing.T) {
	var tracker Tracker
	observe(&tracker,
		event.Key(leftShift, keyPressed),
		event.Key(event.KeyA, keyPressed),
		event.Key(event.ButtonLeft, keyPressed),
	)

	got := tracker.ReleaseAll()
	want := []event.Event{
		event.Key(event.KeyA, event.KeyReleased),
		event.Key(leftShift, event.KeyReleased),
		event.Key(event.ButtonLeft, event.KeyReleased),
		event.Sync(),
	}
	if !slices.Equal(got, want) {
		t.Errorf("ReleaseAll = %+v, want %+v", got, want)
	}
	if held := tracker.Held(); len(held) != 0 {
		t.Errorf("Held after ReleaseAll = %v, want none", held)
	}
}

func TestReleasedKeysAreNotReleasedAgain(t *testing.T) {
	var tracker Tracker
	observe(&tracker,
		event.Key(event.KeyA, keyPressed),
		event.Key(keyB, keyPressed),
		event.Key(event.KeyA, event.KeyReleased),
	)

	if got, want := tracker.Held(), []uint16{keyB}; !slices.Equal(got, want) {
		t.Errorf("Held = %v, want %v", got, want)
	}
}

func TestReleaseAllWithNothingHeldReturnsNothing(t *testing.T) {
	var tracker Tracker
	if got := tracker.ReleaseAll(); got != nil {
		t.Errorf("ReleaseAll = %+v, want nil", got)
	}
}

func TestRepeatsAndOtherEventTypesDoNotChangeWhatIsHeld(t *testing.T) {
	var tracker Tracker
	observe(&tracker,
		event.Key(event.KeyA, keyPressed),
		event.Key(event.KeyA, event.KeyRepeated),
		event.Key(keyB, event.KeyRepeated),
		event.Relative(event.RelX, 5),
		event.Sync(),
		event.Ping(),
	)

	if got, want := tracker.Held(), []uint16{event.KeyA}; !slices.Equal(got, want) {
		t.Errorf("Held = %v, want %v", got, want)
	}
}

func TestReleaseOfAKeyNeverSeenPressedIsIgnored(t *testing.T) {
	var tracker Tracker
	tracker.Observe(event.Key(event.KeyA, event.KeyReleased))
	if held := tracker.Held(); len(held) != 0 {
		t.Errorf("Held = %v, want none", held)
	}
}

func TestTrackerIsReusableAfterReleaseAll(t *testing.T) {
	var tracker Tracker
	tracker.Observe(event.Key(event.KeyA, keyPressed))
	tracker.ReleaseAll()
	tracker.Observe(event.Key(keyB, keyPressed))

	if got, want := tracker.Held(), []uint16{keyB}; !slices.Equal(got, want) {
		t.Errorf("Held = %v, want %v", got, want)
	}
}
