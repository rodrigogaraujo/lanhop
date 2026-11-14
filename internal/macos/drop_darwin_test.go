//go:build darwin && cgo

package macos

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

type fakeDropWindow struct {
	mu             sync.Mutex
	drop           uint64
	answers        []bool
	sessionPending bool
	late           bool
	refused        bool
	movedTo        []Point
	closed         int
}

func (w *fakeDropWindow) state() windowState {
	return windowReady
}

func (w *fakeDropWindow) refuseDrops() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refused = true
	return nil
}

func (w *fakeDropWindow) takeTheMouseAt(at Point) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.movedTo = append(w.movedTo, at)
	if len(w.answers) == 0 {
		return false, nil
	}
	taken := w.answers[0]
	if len(w.answers) > 1 {
		w.answers = w.answers[1:]
	}
	return taken, nil
}

func (w *fakeDropWindow) close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed++
	if w.late {
		return ErrMainThreadLate
	}
	if !w.sessionPending {
		sessionOver(w.drop)
	}
	return nil
}

func (w *fakeDropWindow) seen() (refused bool, movedTo []Point, closed int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.refused, slices.Clone(w.movedTo), w.closed
}

func writeNothing(context.Context, int, string) error {
	return nil
}

func registeredDrop(t *testing.T, screen *dropScreen, write WriteFile, window *fakeDropWindow) *Drop {
	t.Helper()
	drop, err := registerDrop(1, write, newHeldDown(screen, leftEdge))
	if err != nil {
		t.Fatalf("registerDrop: %v", err)
	}
	drop.releaseLimit = 50 * time.Millisecond
	window.drop = drop.id
	drop.window = window
	t.Cleanup(func() {
		drop.Close()
		sessionOver(drop.id)
	})
	return drop
}

func ourWindow() *fakeDropWindow {
	return &fakeDropWindow{answers: []bool{true}}
}

func endsOnTheUp(screen *dropScreen, drop **Drop) {
	screen.onPost = func(mouse MouseEvent) {
		if mouse.Action == MouseUp {
			go endedSession((*drop).id, OperationNone, false)
		}
	}
}

func runningSession(t *testing.T, window *fakeDropWindow) (*dropScreen, *Drop) {
	t.Helper()
	screen := &dropScreen{cursor: leftEdge}
	var drop *Drop
	endsOnTheUp(screen, &drop)
	drop = registeredDrop(t, screen, writeNothing, window)
	drop.button.press()
	drop.button.drag(Point{X: 3, Y: 400})
	screen.moveTo(finderUnderTheCursor)
	return screen, drop
}

var finderUnderTheCursor = Point{X: 803, Y: 250}

func TestADropNeedsTheAppKitRunLoopOfTheMainThread(t *testing.T) {
	drop, err := StartDrop(context.Background(), []PromisedFile{{Name: "report.pdf"}}, writeNothing)
	if drop != nil {
		drop.Close()
		t.Error("a drop started in a program whose main thread runs no AppKit")
	}
	if !errors.Is(err, ErrNoMainThread) {
		t.Errorf("StartDrop = %v, want ErrNoMainThread", err)
	}
}

func TestADropChecksItsFilesAndItsWriterFirst(t *testing.T) {
	if _, err := StartDrop(context.Background(), []PromisedFile{{Name: "../report.pdf"}}, writeNothing); !errors.Is(err, ErrPromisedName) {
		t.Errorf("a name with a slash gave %v, want ErrPromisedName", err)
	}
	if _, err := StartDrop(context.Background(), []PromisedFile{{Name: "report.pdf"}}, nil); !errors.Is(err, ErrNoWriter) {
		t.Errorf("no writer gave %v, want ErrNoWriter", err)
	}
}

func TestOnlyOneDragSessionOfLanhopRunsAtOnce(t *testing.T) {
	first := registeredDrop(t, &dropScreen{cursor: leftEdge}, writeNothing, ourWindow())
	if _, err := registerDrop(1, writeNothing, newHeldDown(&dropScreen{cursor: leftEdge}, leftEdge)); !errors.Is(err, ErrDropRunning) {
		t.Errorf("a second drop during the session gave %v, want ErrDropRunning", err)
	}

	endedSession(first.id, OperationCopy, false)

	registeredDrop(t, &dropScreen{cursor: leftEdge}, writeNothing, ourWindow())
}

func TestASessionStillRunningAfterCloseKeepsOthersOutUntilItsEnd(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{true}, sessionPending: true}
	first := registeredDrop(t, &dropScreen{cursor: leftEdge}, writeNothing, window)

	first.Close()

	if _, _, closed := window.seen(); closed != 1 {
		t.Errorf("the window was closed %d times, want once", closed)
	}
	if _, err := registerDrop(1, writeNothing, newHeldDown(&dropScreen{cursor: leftEdge}, leftEdge)); !errors.Is(err, ErrDropRunning) {
		t.Fatalf("a drop while the closed session has not ended gave %v, want ErrDropRunning", err)
	}
	endedSession(first.id, OperationNone, false)
	registeredDrop(t, &dropScreen{cursor: leftEdge}, writeNothing, ourWindow())
}

func TestASessionThatNeverEndsIsAbandonedAndLetsTheNextDropStart(t *testing.T) {
	first := registeredDrop(t, &dropScreen{cursor: leftEdge}, writeNothing, &fakeDropWindow{sessionPending: true})
	first.Close()

	sessionOver(first.id)

	registeredDrop(t, &dropScreen{cursor: leftEdge}, writeNothing, ourWindow())
}

func TestAWindowCloseTheMainThreadDidNotAnswerKeepsTheNextDropOut(t *testing.T) {
	first := registeredDrop(t, &dropScreen{cursor: leftEdge}, writeNothing, &fakeDropWindow{late: true})

	first.Close()

	if _, err := registerDrop(1, writeNothing, newHeldDown(&dropScreen{cursor: leftEdge}, leftEdge)); !errors.Is(err, ErrDropRunning) {
		t.Errorf("a drop after a close the main thread did not answer gave %v, want ErrDropRunning", err)
	}
}

func TestADropTheDestinationAcceptedPostsNothingMoreWhenItCloses(t *testing.T) {
	screen := &dropScreen{cursor: leftEdge}
	drop := registeredDrop(t, screen, writeNothing, ourWindow())
	drop.button.press()

	endedSession(drop.id, OperationCopy, false)
	drop.Close()

	if !isClosed(drop.Ended()) {
		t.Error("Ended stayed open after the session ended")
	}
	if got := drop.Operation(); got != OperationCopy {
		t.Errorf("Operation = %v, want copy", got)
	}
	expectEvents(t, screen.events(), postedDown(leftEdge))
}

func TestClosingARunningSessionRefusesDropsAndReleasesOverTheWindowMovedUnderTheCursor(t *testing.T) {
	window := ourWindow()
	screen, drop := runningSession(t, window)

	drop.Close()

	if !isClosed(drop.Ended()) {
		t.Error("Close returned before the session ended")
	}
	refused, movedTo, _ := window.seen()
	if !refused {
		t.Error("drops were not refused before the release")
	}
	if !slices.Equal(movedTo, []Point{finderUnderTheCursor}) {
		t.Errorf("the window moved to %+v, want once under the cursor at %+v", movedTo, finderUnderTheCursor)
	}
	expectEvents(t, screen.events(),
		postedDown(leftEdge),
		postedDrag(Point{X: 3, Y: 400}, 3),
		postedUp(finderUnderTheCursor),
	)
}

func TestClosingARunningSessionWaitsForTheWindowToTakeTheMouseBeforeTheRelease(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{false, false, true}}
	screen, drop := runningSession(t, window)
	var upsBeforeTheWindowTookTheMouse int
	react := screen.onPost
	screen.onPost = func(mouse MouseEvent) {
		if _, movedTo, _ := window.seen(); mouse.Action == MouseUp && len(movedTo) < 3 {
			upsBeforeTheWindowTookTheMouse++
		}
		react(mouse)
	}

	drop.Close()

	if upsBeforeTheWindowTookTheMouse != 0 {
		t.Error("the button was released over a window that is not lanhop's")
	}
	if _, movedTo, _ := window.seen(); len(movedTo) != 3 {
		t.Errorf("the window was placed under the cursor %d times, want 3", len(movedTo))
	}
	expectEvents(t, screen.events(),
		postedDown(leftEdge),
		postedDrag(Point{X: 3, Y: 400}, 3),
		postedUp(finderUnderTheCursor),
	)
}

func TestClosingARunningSessionWhoseWindowNeverTakesTheMouseReleasesWithDropsRefused(t *testing.T) {
	window := &fakeDropWindow{answers: []bool{false}}
	screen, drop := runningSession(t, window)

	drop.Close()

	refused, movedTo, _ := window.seen()
	if !refused {
		t.Error("the button was released without refusing drops first")
	}
	if len(movedTo) < 2 {
		t.Errorf("the window was placed under the cursor %d times, want it tried until the limit", len(movedTo))
	}
	expectEvents(t, screen.events(),
		postedDown(leftEdge),
		postedDrag(Point{X: 3, Y: 400}, 3),
		postedUp(finderUnderTheCursor),
	)
}

func TestASessionEndedWithTheButtonStillDownIsReleasedWhereTheCursorIs(t *testing.T) {
	screen := &dropScreen{cursor: leftEdge}
	drop := registeredDrop(t, screen, writeNothing, ourWindow())
	drop.button.press()
	screen.moveTo(Point{X: 803, Y: 250})
	endedSession(drop.id, OperationNone, true)

	drop.Close()

	expectEvents(t, screen.events(), postedDown(leftEdge), postedUp(Point{X: 803, Y: 250}))
}

func TestTheDestinationWritesThroughTheDropUntilItCloses(t *testing.T) {
	var writing context.Context
	write := func(ctx context.Context, file int, destination string) error {
		writing = ctx
		if file != 0 || destination != "/Users/me/Desktop/report.pdf" {
			return fmt.Errorf("asked for file %d at %q", file, destination)
		}
		return nil
	}
	drop := registeredDrop(t, &dropScreen{cursor: leftEdge}, write, ourWindow())
	endedSession(drop.id, OperationCopy, false)

	if err := writePromisedFile(drop.id, 0, "/Users/me/Desktop/report.pdf"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !isClosed(drop.Kept()) {
		t.Error("Kept stayed open after the only file was written")
	}
	drop.Close()
	if writing.Err() == nil {
		t.Error("closing the drop did not stop the writes it had started")
	}
	if err := writePromisedFile(drop.id, 0, "/Users/me/Desktop/report.pdf"); !errors.Is(err, ErrDropClosed) {
		t.Errorf("a write after Close gave %v, want ErrDropClosed", err)
	}
}
