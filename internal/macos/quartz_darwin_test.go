//go:build darwin && cgo

package macos

import (
	"os"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/keymap"
)

const (
	realInputVariable = "LANHOP_TEST_REAL_INPUT"
	observePatience   = 2 * time.Second

	typeMouseMoved   uint32 = 5
	typeKeyDown      uint32 = 10
	typeKeyUp        uint32 = 11
	typeFlagsChanged uint32 = 12
	typeScrollWheel  uint32 = 22

	linuxLeftShift uint16 = 42
	linuxF19       uint16 = 189
	macOSLeftShift uint16 = 0x38
	macOSF19       uint16 = 0x50
)

func observeRealInput(t *testing.T) (*Replay, *Tap) {
	t.Helper()
	if os.Getenv(realInputVariable) == "" {
		t.Skipf("set %s=1 to press keys and move the mouse on this Mac", realInputVariable)
	}
	if !CanPost() || !CanListen() {
		t.Skip("this program has no Accessibility permission, see docs/setup-macos.md")
	}
	tap, err := StartTap()
	if err != nil {
		t.Fatalf("StartTap: %v", err)
	}
	t.Cleanup(func() { tap.Close() })
	return NewReplay(Quartz{}, keymap.Translator{}), tap
}

func nextReplayed(t *testing.T, tap *Tap) Observation {
	t.Helper()
	deadline := time.After(observePatience)
	for {
		select {
		case seen, open := <-tap.Observed():
			if !open {
				t.Fatal("the tap was closed before the replayed event arrived")
			}
			if seen.Marker == Marker {
				return seen
			}
		case <-deadline:
			t.Fatal("the system did not deliver the replayed event")
			return Observation{}
		}
	}
}

func replay(t *testing.T, replay *Replay, events ...event.Event) {
	t.Helper()
	if err := replay.Emit(events...); err != nil {
		t.Fatalf("Emit: %v", err)
	}
}

func TestModifierReachesTheSystemAsAFlagChange(t *testing.T) {
	replayed, tap := observeRealInput(t)

	replay(t, replayed, event.Key(linuxLeftShift, event.KeyPressed), event.Sync())
	pressed := nextReplayed(t, tap)
	replay(t, replayed, event.Key(linuxLeftShift, event.KeyReleased), event.Sync())
	released := nextReplayed(t, tap)

	if pressed.Type != typeFlagsChanged || pressed.KeyCode != macOSLeftShift || pressed.Flags&FlagShift == 0 {
		t.Errorf("press = %+v, want a flag change of left Shift with the Shift flag", pressed)
	}
	if released.Type != typeFlagsChanged || released.KeyCode != macOSLeftShift || released.Flags&FlagShift != 0 {
		t.Errorf("release = %+v, want a flag change of left Shift without the Shift flag", released)
	}
}

func TestKeyReachesTheSystemWithTheModifiersHeld(t *testing.T) {
	replayed, tap := observeRealInput(t)

	replay(t, replayed, event.Key(linuxLeftShift, event.KeyPressed), event.Sync())
	nextReplayed(t, tap)
	replay(t, replayed, event.Key(linuxF19, event.KeyPressed), event.Sync())
	down := nextReplayed(t, tap)
	replay(t, replayed, event.Key(linuxF19, event.KeyReleased), event.Sync())
	up := nextReplayed(t, tap)
	replay(t, replayed, event.Key(linuxLeftShift, event.KeyReleased), event.Sync())
	nextReplayed(t, tap)

	if down.Type != typeKeyDown || down.KeyCode != macOSF19 || down.Flags&FlagShift == 0 {
		t.Errorf("key down = %+v, want F19 down with the Shift flag", down)
	}
	if up.Type != typeKeyUp || up.KeyCode != macOSF19 {
		t.Errorf("key up = %+v, want F19 up", up)
	}
}

func TestMovementMovesTheCursorByTheSameAmount(t *testing.T) {
	replayed, tap := observeRealInput(t)
	start := Quartz{}.Cursor()

	replay(t, replayed, event.Relative(event.RelX, 3), event.Relative(event.RelY, 2), event.Sync())
	there := nextReplayed(t, tap)
	replay(t, replayed, event.Relative(event.RelX, -3), event.Relative(event.RelY, -2), event.Sync())
	back := nextReplayed(t, tap)

	if want := (Point{X: start.X + 3, Y: start.Y + 2}); there.Type != typeMouseMoved || there.At != want || there.DX != 3 || there.DY != 2 {
		t.Errorf("first move = %+v, want a move to %+v by 3,2", there, want)
	}
	if back.Type != typeMouseMoved || back.At != start || back.DX != -3 || back.DY != -2 {
		t.Errorf("second move = %+v, want a move back to %+v by -3,-2", back, start)
	}
	if end := (Quartz{}).Cursor(); end != start {
		t.Errorf("the cursor ended at %+v, want %+v", end, start)
	}
}

func TestWheelScrollsByLines(t *testing.T) {
	replayed, tap := observeRealInput(t)

	replay(t, replayed, event.Relative(event.RelWheel, 1), event.Sync())
	up := nextReplayed(t, tap)
	replay(t, replayed, event.Relative(event.RelWheel, -1), event.Sync())
	down := nextReplayed(t, tap)

	if up.Type != typeScrollWheel || up.ScrollVertical != 1 {
		t.Errorf("first scroll = %+v, want one line up", up)
	}
	if down.Type != typeScrollWheel || down.ScrollVertical != -1 {
		t.Errorf("second scroll = %+v, want one line down", down)
	}
}

func TestDisplaysAreReportedAndHoldTheCursor(t *testing.T) {
	displays := Quartz{}.Displays()
	if len(displays) == 0 {
		t.Skip("no display is attached")
	}
	cursor := Quartz{}.Cursor()
	for _, display := range displays {
		if display.Width <= 0 || display.Height <= 0 {
			t.Errorf("display %+v has no size", display)
		}
		if display.contains(cursor) {
			return
		}
	}
	t.Errorf("the cursor at %+v is on none of the displays %+v", cursor, displays)
}

func replayedWithin(tap *Tap, window time.Duration) []Observation {
	var seen []Observation
	deadline := time.After(window)
	for {
		select {
		case observation, open := <-tap.Observed():
			if !open {
				return seen
			}
			if observation.Marker == Marker {
				seen = append(seen, observation)
			}
		case <-deadline:
			return seen
		}
	}
}

func TestClosedTapStopsObserving(t *testing.T) {
	replayed, first := observeRealInput(t)
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	second, err := StartTap()
	if err != nil {
		t.Fatalf("StartTap: %v", err)
	}
	defer second.Close()

	replay(t, replayed, event.Key(linuxLeftShift, event.KeyPressed), event.Sync())
	pressed := replayedWithin(second, 300*time.Millisecond)
	replay(t, replayed, event.Key(linuxLeftShift, event.KeyReleased), event.Sync())
	released := replayedWithin(second, 300*time.Millisecond)

	if len(pressed) != 1 || len(released) != 1 {
		t.Errorf("the second tap saw %d and %d events for one press and one release, want 1 and 1", len(pressed), len(released))
	}
	if stale := replayedWithin(first, 50*time.Millisecond); len(stale) != 0 {
		t.Errorf("the closed tap still observed %d events", len(stale))
	}
}

const (
	realClicks = "clicks"

	typeLeftMouseDown    uint32 = 1
	typeLeftMouseUp      uint32 = 2
	typeLeftMouseDragged uint32 = 6
)

func observeRealClicks(t *testing.T) (*Replay, *Tap) {
	t.Helper()
	if os.Getenv(realInputVariable) != realClicks {
		t.Skipf("set %s=%s to click and drag where the cursor is on this Mac", realInputVariable, realClicks)
	}
	return observeRealInput(t)
}

func TestClickAndDoubleClickCarryTheirClickCount(t *testing.T) {
	replayed, tap := observeRealClicks(t)
	click := []event.Event{
		event.Key(event.ButtonLeft, event.KeyPressed), event.Sync(),
		event.Key(event.ButtonLeft, event.KeyReleased), event.Sync(),
	}

	replay(t, replayed, click...)
	firstDown, firstUp := nextReplayed(t, tap), nextReplayed(t, tap)
	replay(t, replayed, click...)
	secondDown, secondUp := nextReplayed(t, tap), nextReplayed(t, tap)

	for name, check := range map[string]struct {
		seen     Observation
		wantType uint32
		clicks   int64
	}{
		"first press":    {firstDown, typeLeftMouseDown, 1},
		"first release":  {firstUp, typeLeftMouseUp, 1},
		"second press":   {secondDown, typeLeftMouseDown, 2},
		"second release": {secondUp, typeLeftMouseUp, 2},
	} {
		if check.seen.Type != check.wantType || check.seen.Clicks != check.clicks {
			t.Errorf("%s = %+v, want type %d with click count %d", name, check.seen, check.wantType, check.clicks)
		}
	}
}

func TestMovementWithAButtonHeldIsADrag(t *testing.T) {
	replayed, tap := observeRealClicks(t)

	replay(t, replayed, event.Key(event.ButtonLeft, event.KeyPressed), event.Sync())
	nextReplayed(t, tap)
	replay(t, replayed, event.Relative(event.RelX, 3), event.Sync())
	dragged := nextReplayed(t, tap)
	replay(t, replayed, event.Relative(event.RelX, -3), event.Sync())
	nextReplayed(t, tap)
	replay(t, replayed, event.Key(event.ButtonLeft, event.KeyReleased), event.Sync())
	released := nextReplayed(t, tap)

	if dragged.Type != typeLeftMouseDragged || dragged.DX != 3 {
		t.Errorf("movement with the button held = %+v, want a left drag by 3", dragged)
	}
	if released.Type != typeLeftMouseUp {
		t.Errorf("release = %+v, want left button up", released)
	}
}

func TestTapClosedRightAfterItStartsAlwaysStops(t *testing.T) {
	if !CanListen() {
		t.Skip("this program is not allowed to observe input, see docs/setup-macos.md")
	}
	const rounds = 300
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for round := 0; round < rounds; round++ {
			tap, err := StartTap()
			if err != nil {
				t.Errorf("StartTap in round %d: %v", round, err)
				return
			}
			tap.Close()
		}
	}()
	select {
	case <-finished:
	case <-time.After(60 * time.Second):
		t.Fatal("closing a tap right after starting it did not return")
	}
}

func TestClosedTapEndsItsStreamOfObservations(t *testing.T) {
	if !CanListen() {
		t.Skip("this program is not allowed to observe input, see docs/setup-macos.md")
	}
	tap, err := StartTap()
	if err != nil {
		t.Fatalf("StartTap: %v", err)
	}
	tap.Close()

	deadline := time.After(observePatience)
	for {
		select {
		case _, open := <-tap.Observed():
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("the stream of a closed tap never ended")
		}
	}
}

func TestQuickMovementsAddUpWithoutWaitingForTheSystem(t *testing.T) {
	replayed, tap := observeRealInput(t)
	const steps = 20
	start := Quartz{}.Cursor()

	for range steps {
		replay(t, replayed, event.Relative(event.RelX, 1), event.Sync())
	}
	var last Observation
	for range steps {
		last = nextReplayed(t, tap)
	}
	for range steps {
		replay(t, replayed, event.Relative(event.RelX, -1), event.Sync())
	}
	for range steps {
		nextReplayed(t, tap)
	}

	if want := (Point{X: start.X + steps, Y: start.Y}); last.At != want {
		t.Errorf("after %d quick steps the cursor was at %+v, want %+v", steps, last.At, want)
	}
	if end := (Quartz{}).Cursor(); end != start {
		t.Errorf("the cursor ended at %+v, want %+v", end, start)
	}
}
