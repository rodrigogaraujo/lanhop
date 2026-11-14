package macos

import (
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

func TestAButtonHeldForADropIsDraggedAndReleasedWithoutAPostedDown(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.HoldButton(0)
	replay.Emit(event.Relative(event.RelX, 4), event.Sync())
	replay.Emit(release(event.ButtonLeft), event.Sync())

	if len(screen.posted) != 2 {
		t.Fatalf("posted %+v, want a drag and a release", screen.posted)
	}
	if dragged := screen.posted[0].(MouseEvent); dragged.Action != MouseDragged || dragged.Button != 0 {
		t.Errorf("the move was posted as %+v, want a drag of the left button", dragged)
	}
	if released := screen.posted[1].(MouseEvent); released.Action != MouseUp || released.Button != 0 || released.Clicks != 1 {
		t.Errorf("the release was posted as %+v, want a left mouse up with one click", released)
	}
}

func TestHoldingAButtonAlreadyPressedKeepsItsClickCount(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)
	replay.Emit(press(event.ButtonLeft), event.Sync(), release(event.ButtonLeft), event.Sync(), press(event.ButtonLeft), event.Sync())

	replay.HoldButton(0)
	replay.Emit(release(event.ButtonLeft), event.Sync())

	if up := screen.posted[len(screen.posted)-1].(MouseEvent); up.Action != MouseUp || up.Clicks != 2 {
		t.Errorf("the release was posted as %+v, want the up of a double click", up)
	}
}

func TestAForgottenButtonPostsNoUpWhenItIsReleased(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)
	replay.Emit(press(event.ButtonLeft), event.Sync())
	posted := len(screen.posted)

	replay.ForgetButton(0)
	replay.Emit(event.Relative(event.RelX, 4), event.Sync())
	replay.Emit(release(event.ButtonLeft), event.Sync())

	after := screen.posted[posted:]
	if len(after) != 1 {
		t.Fatalf("posted %+v after the button was forgotten, want only the move", after)
	}
	if moved := after[0].(MouseEvent); moved.Action != MouseMoved {
		t.Errorf("the move was posted as %+v, want a plain move", moved)
	}
}
