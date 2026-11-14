package macos

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	quickShieldLimit = 50 * time.Millisecond
	quickShieldPoll  = 5 * time.Millisecond
)

func cursorAt(p Point) func() Point {
	return func() Point { return p }
}

func TestAShieldCoveredForAMomentStandsOnceItTakesTheMouse(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{false, false, true}}
	shield := newShield(window)

	if err := shield.raise(context.Background(), cursorAt(finderUnderTheCursor), quickShieldLimit, quickShieldPoll); err != nil {
		t.Fatalf("raise: %v", err)
	}

	if _, movedTo, closed := window.seen(); len(movedTo) != 3 || closed != 0 {
		t.Errorf("the window moved %d times and was closed %d times, want 3 and 0", len(movedTo), closed)
	}
	if err := shield.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := shield.Close(); err != nil {
		t.Errorf("Close again: %v", err)
	}
	if _, _, closed := window.seen(); closed != 1 {
		t.Errorf("the window was closed %d times, want once", closed)
	}
}

func TestAShieldThatNeverTakesTheMouseLeavesNothingOnTheScreen(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{false}}
	shield := newShield(window)

	err := shield.raise(context.Background(), cursorAt(finderUnderTheCursor), quickShieldLimit, quickShieldPoll)

	if !errors.Is(err, ErrShieldMissed) {
		t.Errorf("raise = %v, want ErrShieldMissed", err)
	}
	if _, movedTo, closed := window.seen(); len(movedTo) < 2 || closed != 1 {
		t.Errorf("the window moved %d times and was closed %d times, want tries until the limit and one close", len(movedTo), closed)
	}
}

func TestAShieldClosedWhileItWaitsStopsWaitingAndClosesOnce(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{false}}
	shield := newShield(window)
	window.onTake = func(call int) {
		if call == 2 {
			shield.Close()
		}
	}

	err := shield.raise(context.Background(), cursorAt(finderUnderTheCursor), time.Second, quickShieldPoll)

	if !errors.Is(err, ErrShieldClosed) {
		t.Errorf("raise = %v, want ErrShieldClosed", err)
	}
	if _, movedTo, closed := window.seen(); len(movedTo) != 2 || closed != 1 {
		t.Errorf("the window moved %d times and was closed %d times, want 2 and once", len(movedTo), closed)
	}
}

func TestAShieldCancelledWhileItWaitsLeavesNothingOnTheScreen(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{false}}
	shield := newShield(window)
	ctx, cancel := context.WithCancel(context.Background())
	window.onTake = func(int) { cancel() }

	err := shield.raise(ctx, cursorAt(finderUnderTheCursor), time.Second, quickShieldPoll)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("raise = %v, want context.Canceled", err)
	}
	if _, _, closed := window.seen(); closed != 1 {
		t.Errorf("the window was closed %d times, want once", closed)
	}
}

func TestAShieldWhoseMainThreadAnswersLateIsMissedAndSaysWhy(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{true}, takeLate: true}
	shield := newShield(window)

	err := shield.raise(context.Background(), cursorAt(finderUnderTheCursor), quickShieldLimit, quickShieldPoll)

	if !errors.Is(err, ErrShieldMissed) || !strings.Contains(err.Error(), ErrMainThreadLate.Error()) {
		t.Errorf("raise = %v, want ErrShieldMissed with the late main thread", err)
	}
}

func TestAShieldFollowsTheCursorUntilItTakesTheMouse(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{false, true}}
	shield := newShield(window)
	where := []Point{finderUnderTheCursor, mailUnderTheCursor}
	cursor := func() Point {
		next := where[0]
		if len(where) > 1 {
			where = where[1:]
		}
		return next
	}

	if err := shield.raise(context.Background(), cursor, quickShieldLimit, quickShieldPoll); err != nil {
		t.Fatalf("raise: %v", err)
	}
	if _, movedTo, _ := window.seen(); !slices.Equal(movedTo, []Point{finderUnderTheCursor, mailUnderTheCursor}) {
		t.Errorf("the window moved to %+v, want under the cursor each time", movedTo)
	}
}

func TestClosingAShieldIsBoundedAndSaysWhenTheMainThreadWasLate(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{true}, closeLate: true}
	shield := newShield(window)
	if err := shield.raise(context.Background(), cursorAt(finderUnderTheCursor), quickShieldLimit, quickShieldPoll); err != nil {
		t.Fatalf("raise: %v", err)
	}

	if err := shield.Close(); !errors.Is(err, ErrMainThreadLate) {
		t.Errorf("Close = %v, want ErrMainThreadLate", err)
	}
	if err := shield.Close(); !errors.Is(err, ErrMainThreadLate) {
		t.Errorf("Close again = %v, want the same answer", err)
	}
	if _, _, closed := window.seen(); closed != 1 {
		t.Errorf("the window was closed %d times, want once", closed)
	}
}

func TestClosingNoShieldDoesNothing(t *testing.T) {
	var shield *Shield
	if err := shield.Close(); err != nil {
		t.Errorf("Close of no shield = %v", err)
	}
}
