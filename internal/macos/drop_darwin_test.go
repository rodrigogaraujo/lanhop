//go:build darwin && cgo

package macos

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func writeNothing(context.Context, int, string) error {
	return nil
}

func registeredDrop(t *testing.T, screen *dropScreen, write WriteFile) *Drop {
	t.Helper()
	drop, err := registerDrop(1, write, newHeldDown(screen, leftEdge))
	if err != nil {
		t.Fatalf("registerDrop: %v", err)
	}
	t.Cleanup(drop.Close)
	return drop
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

func TestADropChecksItsFilesAndItsWriterFirst(t *testing.T) {
	if _, err := StartDrop(context.Background(), []PromisedFile{{Name: "../report.pdf"}}, writeNothing); !errors.Is(err, ErrPromisedName) {
		t.Errorf("a name with a slash gave %v, want ErrPromisedName", err)
	}
	if _, err := StartDrop(context.Background(), []PromisedFile{{Name: "report.pdf"}}, nil); !errors.Is(err, ErrNoWriter) {
		t.Errorf("no writer gave %v, want ErrNoWriter", err)
	}
}

func TestOnlyOneDragSessionOfLanhopRunsAtOnce(t *testing.T) {
	first := registeredDrop(t, &dropScreen{cursor: leftEdge}, writeNothing)
	if _, err := registerDrop(1, writeNothing, newHeldDown(&dropScreen{cursor: leftEdge}, leftEdge)); !errors.Is(err, ErrDropRunning) {
		t.Errorf("a second drop during the session gave %v, want ErrDropRunning", err)
	}

	first.sessionEnded(OperationCopy, false)

	registeredDrop(t, &dropScreen{cursor: leftEdge}, writeNothing)
}

func TestADropTheDestinationAcceptedPostsNothingMoreWhenItCloses(t *testing.T) {
	screen := &dropScreen{cursor: leftEdge}
	drop := registeredDrop(t, screen, writeNothing)
	drop.button.press()

	drop.sessionEnded(OperationCopy, false)
	drop.Close()

	if !isClosed(drop.Ended()) {
		t.Error("Ended stayed open after the session ended")
	}
	if got := drop.Operation(); got != OperationCopy {
		t.Errorf("Operation = %v, want copy", got)
	}
	expectEvents(t, screen.events(), postedDown(leftEdge))
}

func TestClosingARunningSessionReleasesTheButtonInTheWindowOfLanhop(t *testing.T) {
	screen := &dropScreen{cursor: leftEdge}
	drop := registeredDrop(t, screen, writeNothing)
	screen.onPost = func(mouse MouseEvent) {
		if mouse.Action == MouseUp {
			go drop.sessionEnded(OperationNone, false)
		}
	}
	drop.button.press()
	drop.button.drag(Point{X: 3, Y: 400})
	screen.moveTo(Point{X: 803, Y: 250})

	drop.Close()

	if !isClosed(drop.Ended()) {
		t.Error("Close returned before the session ended")
	}
	expectEvents(t, screen.events(),
		postedDown(leftEdge),
		postedDrag(Point{X: 3, Y: 400}, 3),
		MouseEvent{Action: MouseDragged, At: leftEdge, Clicks: 1, DX: -803, DY: 150},
		postedUp(leftEdge),
	)
}

func TestASessionEndedWithTheButtonStillDownIsReleasedWhereTheCursorIs(t *testing.T) {
	screen := &dropScreen{cursor: leftEdge}
	drop := registeredDrop(t, screen, writeNothing)
	drop.button.press()
	screen.moveTo(Point{X: 803, Y: 250})
	drop.sessionEnded(OperationNone, true)

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
	drop := registeredDrop(t, &dropScreen{cursor: leftEdge}, write)
	drop.sessionEnded(OperationCopy, false)

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
