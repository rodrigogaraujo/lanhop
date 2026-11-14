//go:build darwin && cgo

package macos

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

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
	if window.onClose == nil {
		window.onClose = func() { sessionOver(drop.id) }
	}
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

func pendingSession() *fakeDropWindow {
	return &fakeDropWindow{answers: []bool{true}, onClose: func() {}}
}

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

func TestAShieldNeedsTheAppKitRunLoopOfTheMainThread(t *testing.T) {
	shield, err := ShieldCursor(context.Background())
	if shield != nil {
		shield.Close()
		t.Error("a shield stood in a program whose main thread runs no AppKit")
	}
	if !errors.Is(err, ErrNoMainThread) {
		t.Errorf("ShieldCursor = %v, want ErrNoMainThread", err)
	}
}

func TestAShieldAskedWithACancelledContextOpensNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	shield, err := ShieldCursor(ctx)
	if shield != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("ShieldCursor = %v, %v, want no shield and context.Canceled", shield, err)
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
	window := pendingSession()
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
	first := registeredDrop(t, &dropScreen{cursor: leftEdge}, writeNothing, pendingSession())
	first.Close()

	sessionOver(first.id)

	registeredDrop(t, &dropScreen{cursor: leftEdge}, writeNothing, ourWindow())
}

func TestAWindowCloseTheMainThreadDidNotAnswerKeepsTheNextDropOut(t *testing.T) {
	first := registeredDrop(t, &dropScreen{cursor: leftEdge}, writeNothing, &fakeDropWindow{closeLate: true})

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

func TestClosingARunningDropReleasesOverItsWindowAndWaitsForTheEnd(t *testing.T) {
	screen := &dropScreen{cursor: leftEdge}
	window := ourWindow()
	drop := registeredDrop(t, screen, writeNothing, window)
	screen.onPost = func(mouse MouseEvent) {
		if mouse.Action == MouseUp {
			go endedSession(drop.id, OperationNone, false)
		}
	}
	drop.button.press()
	drop.button.drag(Point{X: 3, Y: 400})
	screen.moveTo(finderUnderTheCursor)

	drop.Close()

	if !isClosed(drop.Ended()) {
		t.Error("Close returned before the session ended")
	}
	if refused, movedTo, _ := window.seen(); !refused || !slices.Equal(movedTo, []Point{finderUnderTheCursor}) {
		t.Errorf("refused %v, window moved to %+v", refused, movedTo)
	}
	expectEvents(t, screen.events(),
		postedDown(leftEdge),
		postedDrag(Point{X: 3, Y: 400}, 3),
		MouseEvent{Action: MouseDragged, At: finderUnderTheCursor, Clicks: 1},
		postedUp(finderUnderTheCursor),
	)
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
