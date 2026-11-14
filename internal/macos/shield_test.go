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
	shortShieldLimit = 50 * time.Millisecond
	quickShieldPoll  = 5 * time.Millisecond
)

func cursorAt(p Point) func() Point {
	return func() Point { return p }
}

func shieldOver(t *testing.T, window *fakeDropWindow) *Shield {
	t.Helper()
	shield := newShield(window, time.Minute)
	t.Cleanup(func() { shield.Close() })
	return shield
}

func TestAShieldCoveredForAMomentStandsOnceItTakesTheMouse(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{false, false, true}}
	shield := shieldOver(t, window)

	if err := shield.raise(context.Background(), cursorAt(finderUnderTheCursor), time.Second, quickShieldPoll); err != nil {
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
	shield := shieldOver(t, window)

	err := shield.raise(context.Background(), cursorAt(finderUnderTheCursor), shortShieldLimit, quickShieldPoll)

	if !errors.Is(err, ErrShieldMissed) {
		t.Errorf("raise = %v, want ErrShieldMissed", err)
	}
	if _, movedTo, closed := window.seen(); len(movedTo) < 2 || closed != 1 {
		t.Errorf("the window moved %d times and was closed %d times, want tries until the limit and one close", len(movedTo), closed)
	}
}

func TestAShieldClosedWhileItWaitsStopsWaitingAndClosesOnce(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{false}}
	shield := shieldOver(t, window)
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
	shield := shieldOver(t, window)
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
	shield := shieldOver(t, window)

	err := shield.raise(context.Background(), cursorAt(finderUnderTheCursor), shortShieldLimit, quickShieldPoll)

	if !errors.Is(err, ErrShieldMissed) || !strings.Contains(err.Error(), ErrMainThreadLate.Error()) {
		t.Errorf("raise = %v, want ErrShieldMissed with the late main thread", err)
	}
}

func TestAShieldFollowsTheCursorUntilItTakesTheMouse(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{false, true}}
	shield := shieldOver(t, window)
	where := []Point{finderUnderTheCursor, mailUnderTheCursor}
	cursor := func() Point {
		next := where[0]
		if len(where) > 1 {
			where = where[1:]
		}
		return next
	}

	if err := shield.raise(context.Background(), cursor, time.Second, quickShieldPoll); err != nil {
		t.Fatalf("raise: %v", err)
	}
	if _, movedTo, _ := window.seen(); !slices.Equal(movedTo, []Point{finderUnderTheCursor, mailUnderTheCursor}) {
		t.Errorf("the window moved to %+v, want under the cursor each time", movedTo)
	}
}

func TestClosingAShieldIsBoundedAndSaysWhenTheMainThreadWasLate(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{true}, closeLate: true}
	shield := shieldOver(t, window)
	if err := shield.raise(context.Background(), cursorAt(finderUnderTheCursor), time.Second, quickShieldPoll); err != nil {
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

func TestAShieldLeftOpenClosesItselfAfterItsLifetime(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{true}}
	shield := newShield(window, 30*time.Millisecond)
	if err := shield.raise(context.Background(), cursorAt(finderUnderTheCursor), time.Second, quickShieldPoll); err != nil {
		t.Fatalf("raise: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for _, _, closed := window.seen(); closed == 0 && time.Now().Before(deadline); _, _, closed = window.seen() {
		time.Sleep(5 * time.Millisecond)
	}

	if _, _, closed := window.seen(); closed != 1 {
		t.Fatalf("the shield left open was closed %d times after its lifetime, want once", closed)
	}
	shield.Close()
	if _, _, closed := window.seen(); closed != 1 {
		t.Errorf("Close after the lifetime closed the window again: %d closes", closed)
	}
}

func TestAShieldWaitingOutALongPollStopsAtOnceWhenClosedOrCancelled(t *testing.T) {
	const longPoll = time.Second
	stops := map[string]func(*Shield, context.CancelFunc){
		"Close":  func(shield *Shield, _ context.CancelFunc) { shield.Close() },
		"cancel": func(_ *Shield, cancel context.CancelFunc) { cancel() },
	}
	for name, stop := range stops {
		window := &fakeDropWindow{answers: []bool{false}}
		shield := shieldOver(t, window)
		ctx, cancel := context.WithCancel(context.Background())
		window.onTake = func(int) {
			go func() {
				time.Sleep(20 * time.Millisecond)
				stop(shield, cancel)
			}()
		}

		started := time.Now()
		err := shield.raise(ctx, cursorAt(finderUnderTheCursor), 10*time.Second, longPoll)
		took := time.Since(started)
		cancel()

		if err == nil {
			t.Errorf("%s: raise stood without the window taking the mouse", name)
		}
		if took >= longPoll/2 {
			t.Errorf("%s: raise returned after %v, want well before the poll of %v", name, took, longPoll)
		}
	}
}
