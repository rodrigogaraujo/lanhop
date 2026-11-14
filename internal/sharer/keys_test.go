package sharer

import (
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

func escapeIsPressed(duringADrag bool) string {
	if duringADrag {
		return "Escape is pressed on the central during a drag"
	}
	return "Escape is pressed on the central outside a drag"
}

func escapeTyped() []event.Event {
	return eachFollowedBySync(press(event.KeyEsc), release(event.KeyEsc))
}

func TestEscapePressedOnTheCentralIsToldWithWhetherADragIsInProgress(t *testing.T) {
	t.Run("a drag that crossed to a side machine keeps the Escape away from it, release included, until the next press", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		s.connectTakingDrags(desk)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 1, noDock)
		s.pushes(1, 0, dragReportsToCross)
		s.captures(press(event.KeyEsc), event.Sync(), repeat(event.KeyEsc), event.Sync())
		s.releasesTheButton()
		s.captures(release(event.KeyEsc), event.Sync())
		s.captures(escapeTyped()...)

		s.expectJournal(story(
			then(dragged(thisMachine, thisMachine, "desk", 1), tookInput),
			gets(desk, enteredAt(0, 16384)...),
			then(entered(thisMachine, thisMachine, "desk", 1)),
			then(escapeIsPressed(true)),
			gets(desk, release(event.ButtonLeft), event.Sync()),
			then(escapeIsPressed(false)),
			gets(desk, escapeTyped()...),
		))
	})
	t.Run("a drag on the side machine it started on lets the Escape reach that machine", func(t *testing.T) {
		s := startSharing(t, desk)
		s.connect(desk)

		s.focusOn(2)
		s.holdsTheButton()
		s.dragStartsOn("desk", 3, noDock)
		s.captures(escapeTyped()...)

		s.expectJournal(story(
			then(tookInput),
			gets(desk, event.Enter()),
			gets(desk, press(event.ButtonLeft), event.Sync()),
			then(escapeIsPressed(true)),
			gets(desk, escapeTyped()...),
		))
	})
	t.Run("a drag on the central is told, and nothing is sent anywhere", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		s.connectTakingDrags(desk)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 1, noDock)
		s.captures(escapeTyped()...)

		s.expectJournal(then(escapeIsPressed(true)))
	})
	t.Run("without a drag the Escape is told and reaches the machine with focus as before", func(t *testing.T) {
		s := startSharing(t, desk)
		s.connect(desk)

		s.focusOn(2)
		s.captures(escapeTyped()...)

		s.expectJournal(story(
			then(tookInput),
			gets(desk, event.Enter()),
			then(escapeIsPressed(false)),
			gets(desk, escapeTyped()...),
		))
	})
	t.Run("a drag that ended with the release of the button is no longer in progress", func(t *testing.T) {
		s := startSharing(t, desk)
		s.connect(desk)

		s.focusOn(2)
		s.holdsTheButton()
		s.dragStartsOn("desk", 3, noDock)
		s.releasesTheButton()
		s.captures(escapeTyped()...)

		s.expectJournal(story(
			then(tookInput),
			gets(desk, event.Enter()),
			gets(desk, press(event.ButtonLeft), event.Sync()),
			gets(desk, release(event.ButtonLeft), event.Sync()),
			then(escapeIsPressed(false)),
			gets(desk, escapeTyped()...),
		))
	})
}

func leftButtonIs(pressed bool) string {
	if pressed {
		return "the left button is pressed on the central"
	}
	return "the left button is released on the central"
}

func TestEveryPressAndReleaseOfTheLeftButtonOnTheCentralIsTold(t *testing.T) {
	s := newSharing(t, desk)
	s.leftButtons = func(pressed bool) { s.journal.note(leftButtonIs(pressed)) }
	s.start(s.capture)
	s.connect(desk)

	s.holdsTheButton()
	s.captures(repeat(event.ButtonLeft), event.Sync())
	s.captures(eachFollowedBySync(press(event.ButtonRight), release(event.ButtonRight))...)
	s.releasesTheButton()
	s.focusOn(2)
	s.holdsTheButton()
	s.releasesTheButton()

	s.expectJournal(story(
		then(leftButtonIs(true), leftButtonIs(false), tookInput),
		gets(desk, event.Enter()),
		then(leftButtonIs(true)),
		gets(desk, press(event.ButtonLeft), event.Sync()),
		then(leftButtonIs(false)),
		gets(desk, release(event.ButtonLeft), event.Sync()),
	))
}
