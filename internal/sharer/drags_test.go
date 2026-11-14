package sharer

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	dragReportsToCross = 61
	noDock             = edge.Side(0)
)

var (
	takesDrags   = event.Features(event.FeatureClipboard | 1<<features.Files | 1<<features.Drag)
	takesNoDrags = event.Features(event.FeatureClipboard | 1<<features.Files)
	filesAreOff  = event.Features(event.FeatureClipboard)
)

func dragCrosses(crossing DragCrossing) string {
	return fmt.Sprintf("a drag of %d files from %s crosses from %s to %s with button %#x held",
		crossing.Files, crossing.Origin, crossing.From, crossing.To, crossing.Button)
}

func dragEnters(crossing DragCrossing) string {
	return fmt.Sprintf("a drag of %d files from %s entered %s from %s with button %#x held",
		crossing.Files, crossing.Origin, crossing.To, crossing.From, crossing.Button)
}

func dragIsBlocked(blocked BlockedDrag) string {
	switch blocked.Block {
	case DragCannotReach:
		return "the central says a dragged file cannot reach " + blocked.Machine
	case FilesOffThere:
		return "the central says file sharing is off on " + blocked.Machine
	case DragCannotLeave:
		return "the central says a dragged file cannot leave " + blocked.Machine
	}
	return "the central says something unknown about " + blocked.Machine
}

func dragIsUnblocked(unblocked BlockedDrag) string {
	return "the central takes back that " + dragIsBlocked(unblocked)
}

func crossingOf(origin, from, to string, files int) DragCrossing {
	return DragCrossing{Origin: origin, From: from, To: to, Files: files, Button: event.ButtonLeft}
}

func dragged(origin, from, to string, files int) string {
	return dragCrosses(crossingOf(origin, from, to, files))
}

func entered(origin, from, to string, files int) string {
	return dragEnters(crossingOf(origin, from, to, files))
}

func cannotReach(machine string) string {
	return dragIsBlocked(BlockedDrag{Machine: machine, Block: DragCannotReach})
}

func filesOff(machine string) string {
	return dragIsBlocked(BlockedDrag{Machine: machine, Block: FilesOffThere})
}

func cannotLeave(machine string) string {
	return dragIsBlocked(BlockedDrag{Machine: machine, Block: DragCannotLeave})
}

func takenBack(machine string, block DragBlock) string {
	return dragIsUnblocked(BlockedDrag{Machine: machine, Block: block})
}

func (s *sharing) hears(receiver *fakeReceiver, frames ...event.Event) {
	s.t.Helper()
	for _, frame := range append(frames, event.Ping()) {
		select {
		case receiver.says <- frame:
		case <-time.After(testPatience):
			s.t.Fatalf("the sharer did not read %+v from %s", frame, receiver.peer.Name)
		}
	}
}

func (s *sharing) connectTakingDrags(peer trust.Peer) *fakeReceiver {
	s.t.Helper()
	receiver := s.connectSaying(peer)
	s.hears(receiver, takesDrags)
	return receiver
}

func (s *sharing) connectNotTakingDrags(peer trust.Peer) *fakeReceiver {
	s.t.Helper()
	receiver := s.connectSaying(peer)
	s.hears(receiver, takesNoDrags)
	return receiver
}

func (s *sharing) holdsTheButton() {
	s.t.Helper()
	s.captures(press(event.ButtonLeft), event.Sync())
}

func (s *sharing) releasesTheButton() {
	s.t.Helper()
	s.captures(release(event.ButtonLeft), event.Sync())
}

func (s *sharing) dragStartsOn(origin string, files int, dock edge.Side) {
	s.sharer.DragStarted(Drag{Origin: origin, Files: files, Dock: dock})
}

func (s *sharing) waitsForTheInputBack() {
	s.t.Helper()
	s.eventually("giving the input back", func() bool { return s.journal.has(gaveInputBack) })
}

func announcingHere(bits ...int) *features.Set {
	own := new(features.Set)
	for _, bit := range bits {
		own.Turn(bit, true)
	}
	return own
}

func TestAFileDraggedOnTheCentralCrossesOnlyUnderTheRulesOfADrag(t *testing.T) {
	crossedToDesk := story(
		then(dragged(thisMachine, thisMachine, "desk", 2), tookInput),
		gets(desk, enteredAt(0, 16384)...),
		then(entered(thisMachine, thisMachine, "desk", 2)),
	)
	dragsHere := func(s *sharing, dock edge.Side) {
		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 2, dock)
	}
	tests := []struct {
		name      string
		layout    string
		dragsStay bool
		noHooks   bool
		cursorY   int
		connect   func(s *sharing)
		act       func(s *sharing)
		want      []string
	}{
		{
			name: "it crosses with the button held after a push of 600 ms toward a neighbour that takes dragged files",
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.pushes(1, 0, dragReportsToCross)
			},
			want: crossedToDesk,
		},
		{
			name: "it does not cross after the push of the cursor alone, nor just before 600 ms",
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.pushes(1, 0, dragReportsToCross-1)
			},
		},
		{
			name:    "without a hook for the crossing, the held button blocks as before",
			noHooks: true,
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.pushes(1, 0, 3*dragReportsToCross)
			},
		},
		{
			name: "a button held without a file drag never crosses",
			act: func(s *sharing) {
				s.holdsTheButton()
				s.pushes(1, 0, 3*dragReportsToCross)
			},
		},
		{
			name: "a drag told before the button is held is ignored",
			act: func(s *sharing) {
				s.dragStartsOn(thisMachine, 2, noDock)
				s.holdsTheButton()
				s.pushes(1, 0, 3*dragReportsToCross)
			},
		},
		{
			name: "a drag told for a machine that does not have the cursor is ignored",
			act: func(s *sharing) {
				s.holdsTheButton()
				s.dragStartsOn("desk", 2, noDock)
				s.pushes(1, 0, 3*dragReportsToCross)
			},
		},
		{
			name: "a second drag told while one is in progress is ignored",
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.moves(0, 1)
				s.dragStartsOn(thisMachine, 5, noDock)
				s.pushes(1, 0, dragReportsToCross)
			},
			want: crossedToDesk,
		},
		{
			name: "a release of the button ends the drag, so a new press does not cross",
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.pushes(1, 0, 30)
				s.releasesTheButton()
				s.holdsTheButton()
				s.pushes(1, 0, 3*dragReportsToCross)
			},
		},
		{
			name:    "toward a neighbour that shares files but does not take dragged files nothing crosses, and the central says so once",
			connect: func(s *sharing) { s.connectNotTakingDrags(desk) },
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.pushes(1, 0, 3*dragReportsToCross)
				s.releasesTheButton()
				dragsHere(s, noDock)
				s.pushes(1, 0, 2*dragReportsToCross)
			},
			want: then(cannotReach("desk")),
		},
		{
			name:    "toward a neighbour with file sharing off nothing crosses, and the central says that file sharing is off there",
			connect: func(s *sharing) { s.hears(s.connectSaying(desk), filesAreOff) },
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.pushes(1, 0, 3*dragReportsToCross)
			},
			want: then(filesOff("desk")),
		},
		{
			name:    "toward a neighbour that never announced anything, as an older lanhop, nothing crosses and nothing is said",
			connect: func(s *sharing) { s.connect(desk) },
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.pushes(1, 0, 3*dragReportsToCross)
			},
		},
		{
			name: "a neighbour that stopped taking dragged files is not crossed",
			connect: func(s *sharing) {
				receiver := s.connectTakingDrags(desk)
				s.hears(receiver, takesNoDrags)
			},
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.pushes(1, 0, 2*dragReportsToCross)
			},
			want: then(cannotReach("desk")),
		},
		{
			name:   "a neighbour that is not connected stays a plain edge, with nothing said",
			layout: "me laptop desk",
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.pushes(1, 0, 2*dragReportsToCross)
			},
		},
		{
			name:    "in a corner nothing crosses",
			cursorY: 10,
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.pushes(1, 0, 3*dragReportsToCross)
			},
		},
		{
			name: "nothing crosses through the edge where the Dock of the drag sits, and nothing is said",
			act: func(s *sharing) {
				dragsHere(s, edge.Right)
				s.pushes(1, 0, 3*dragReportsToCross)
			},
		},
		{
			name: "nothing crosses through the edge where the Dock of this machine was set",
			act: func(s *sharing) {
				s.sharer.SetDock(thisMachine, edge.Right)
				dragsHere(s, noDock)
				s.pushes(1, 0, 3*dragReportsToCross)
			},
		},
		{
			name: "the Dock on another edge leaves this one crossing",
			act: func(s *sharing) {
				s.sharer.SetDock(thisMachine, edge.Right)
				dragsHere(s, edge.Bottom)
				s.pushes(1, 0, dragReportsToCross)
			},
			want: crossedToDesk,
		},
		{
			name: "another button held with the left one keeps blocking",
			act: func(s *sharing) {
				s.holdsTheButton()
				s.captures(press(event.ButtonRight), event.Sync())
				s.dragStartsOn(thisMachine, 2, noDock)
				s.pushes(1, 0, 3*dragReportsToCross)
			},
		},
		{
			name: "a key held on the central keeps blocking, so it is not left held here",
			act: func(s *sharing) {
				s.captures(press(keyLeftShift), event.Sync())
				dragsHere(s, noDock)
				s.pushes(1, 0, 3*dragReportsToCross)
			},
		},
		{
			name: "a drag cancelled before the push time blocks again while the button stays held",
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.pushes(1, 0, 30)
				s.sharer.DragCancelled()
				s.pushes(1, 0, 3*dragReportsToCross)
			},
		},
		{
			name: "the drag ending on its origin before the push time blocks again",
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.pushes(1, 0, 30)
				s.sharer.SourceDragEnded(thisMachine)
				s.pushes(1, 0, 3*dragReportsToCross)
			},
		},
		{
			name: "the end of a drag on another machine changes nothing",
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.pushes(1, 0, 30)
				s.sharer.SourceDragEnded("desk")
				s.pushes(1, 0, dragReportsToCross-30)
			},
			want: crossedToDesk,
		},
		{
			name: "the button released before the push time ends the drag, and the cursor alone crosses after its own push",
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.pushes(1, 0, 30)
				s.releasesTheButton()
				s.pushes(1, 0, reportsToCross)
			},
			want: story(
				then(tookInput),
				gets(desk, enteredAt(0, 16384)...),
			),
		},
		{
			name:      "a central whose file drags cannot leave never lets one cross, and says so once",
			dragsStay: true,
			act: func(s *sharing) {
				dragsHere(s, noDock)
				s.pushes(1, 0, 3*dragReportsToCross)
				s.releasesTheButton()
				dragsHere(s, noDock)
				s.pushes(1, 0, 2*dragReportsToCross)
			},
			want: then(cannotLeave(thisMachine)),
		},
		{
			name:      "a central whose file drags cannot leave still crosses with the cursor alone",
			dragsStay: true,
			act: func(s *sharing) {
				s.pushes(1, 0, reportsToCross)
			},
			want: story(
				then(tookInput),
				gets(desk, enteredAt(0, 16384)...),
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout := test.layout
			if layout == "" {
				layout = "me desk"
			}
			s := newSharing(t, desk, laptop)
			s.dragsStay = test.dragsStay
			s.noDragHooks = test.noHooks
			s.desk.Store(drawn(t, layout))
			s.startWithAScreen(ownDesktop)
			if test.connect == nil {
				s.connectTakingDrags(desk)
			} else {
				test.connect(s)
			}
			cursorY := test.cursorY
			if cursorY == 0 {
				cursorY = 125
			}
			s.screen.cursorIsAt(1000, cursorY)

			test.act(s)

			s.expectJournal(test.want)
		})
	}
}

func TestTheFileDraggedAcrossIsCarriedOnTheNeighbourWithoutReplayingThePress(t *testing.T) {
	s := startSharingWithLayout(t, "me desk", desk)
	s.connectTakingDrags(desk)
	s.screen.cursorIsAt(1000, 125)

	s.holdsTheButton()
	s.dragStartsOn(thisMachine, 4, noDock)
	s.pushes(1, 0, dragReportsToCross)
	s.moves(3, 0)
	s.releasesTheButton()

	s.expectJournal(story(
		then(dragged(thisMachine, thisMachine, "desk", 4), tookInput),
		gets(desk, enteredAt(0, 16384)...),
		then(entered(thisMachine, thisMachine, "desk", 4)),
		gets(desk, positioned(300, 16384)...),
		gets(desk, release(event.ButtonLeft), event.Sync()),
	))
}

func TestADragInProgressCrossesOnAndBackUnderTheSameRules(t *testing.T) {
	toDesk := story(
		then(dragged(thisMachine, thisMachine, "desk", 1), tookInput),
		gets(desk, enteredAt(0, 16384)...),
		then(entered(thisMachine, thisMachine, "desk", 1)),
	)
	dragToDesk := func(s *sharing) {
		s.screen.cursorIsAt(1000, 125)
		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 1, noDock)
		s.pushes(1, 0, dragReportsToCross)
	}

	t.Run("A to B to A: back to the origin, which needs no bit 2 and ends the drag", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		s.connectTakingDrags(desk)

		dragToDesk(s)
		s.pushes(-1, 0, dragReportsToCross)
		s.pushes(1, 0, 3*dragReportsToCross)

		s.expectJournal(story(
			toDesk,
			gets(desk, times(dragReportsToCross, positioned(0, 16384)...)...),
			then(dragged(thisMachine, "desk", thisMachine, 1)),
			gets(desk, event.Leave()),
			then(gaveInputBack, cursorPlacedAt(1000, 125), entered(thisMachine, "desk", thisMachine, 1)),
		))
	})
	t.Run("A to B to C: on to a third machine that takes dragged files", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
		s.connectTakingDrags(desk)
		s.connectTakingDrags(laptop)

		dragToDesk(s)
		s.moves(700, 0)
		s.pushes(1, 0, dragReportsToCross-1)

		s.expectJournal(story(
			toDesk,
			gets(desk, times(dragReportsToCross, positioned(farEdge, 16384)...)...),
			then(dragged(thisMachine, "desk", "laptop", 1)),
			gets(desk, event.Leave()),
			gets(laptop, enteredAt(0, 16384)...),
			then(entered(thisMachine, "desk", "laptop", 1)),
		))
	})
	t.Run("A to B to C: not through the edge of B where the Dock of B sits", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
		s.connectTakingDrags(desk)
		s.connectTakingDrags(laptop)
		s.sharer.SetDock("desk", edge.Right)

		dragToDesk(s)
		s.moves(700, 0)
		s.pushes(1, 0, 2*dragReportsToCross-1)

		s.expectJournal(story(
			toDesk,
			gets(desk, times(2*dragReportsToCross, positioned(farEdge, 16384)...)...),
		))
	})
	t.Run("A to B to C: not on to a third machine that does not take dragged files", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
		s.connectTakingDrags(desk)
		s.connectNotTakingDrags(laptop)

		dragToDesk(s)
		s.moves(700, 0)
		s.pushes(1, 0, 2*dragReportsToCross-1)

		s.expectJournal(story(
			toDesk,
			gets(desk, times(dragReportsToCross, positioned(farEdge, 16384)...)...),
			then(cannotReach("laptop")),
			gets(desk, times(dragReportsToCross, positioned(farEdge, 16384)...)...),
		))
	})
	t.Run("A to B to C: not on before the longer push", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
		s.connectTakingDrags(desk)
		s.connectTakingDrags(laptop)

		dragToDesk(s)
		s.moves(700, 0)
		s.pushes(1, 0, dragReportsToCross-2)

		s.expectJournal(story(
			toDesk,
			gets(desk, times(dragReportsToCross-1, positioned(farEdge, 16384)...)...),
		))
	})
	t.Run("A to B to C: the drag ending on its origin after it crossed away changes nothing", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
		s.connectTakingDrags(desk)
		s.connectTakingDrags(laptop)

		dragToDesk(s)
		s.sharer.SourceDragEnded(thisMachine)
		s.moves(700, 0)
		s.pushes(1, 0, dragReportsToCross-1)

		s.expectJournal(story(
			toDesk,
			gets(desk, times(dragReportsToCross, positioned(farEdge, 16384)...)...),
			then(dragged(thisMachine, "desk", "laptop", 1)),
			gets(desk, event.Leave()),
			gets(laptop, enteredAt(0, 16384)...),
			then(entered(thisMachine, "desk", "laptop", 1)),
		))
	})
	t.Run("A to B, then a cancel ends the drag and the held button blocks again", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		s.connectTakingDrags(desk)

		dragToDesk(s)
		s.sharer.DragCancelled()
		s.pushes(-1, 0, 2*dragReportsToCross)

		s.expectJournal(story(
			toDesk,
			gets(desk, times(2*dragReportsToCross, positioned(0, 16384)...)...),
		))
	})
	t.Run("A to B, then the button is released and the cursor alone crosses back without a drag", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		s.connectTakingDrags(desk)

		dragToDesk(s)
		s.releasesTheButton()
		s.pushes(-1, 0, reportsToCross)

		s.expectJournal(story(
			toDesk,
			gets(desk, release(event.ButtonLeft), event.Sync()),
			gets(desk, times(reportsToCross, positioned(0, 16384)...)...),
			gets(desk, event.Leave()),
			then(gaveInputBack, cursorPlacedAt(1000, 125)),
		))
	})
}

func TestADragEndsWhenFocusMovesOtherwiseThanByItsCrossing(t *testing.T) {
	t.Run("the origin side is lost while it has the cursor, and the button held on the central does not cross", func(t *testing.T) {
		s := startSharingWithLayout(t, "laptop me desk", desk, laptop)
		lost := s.connect(desk)
		s.connectTakingDrags(laptop)

		s.focusOn(2)
		s.holdsTheButton()
		s.dragStartsOn("desk", 1, noDock)
		s.moves(1, 0)
		lost.loseConnection()
		s.waitsForTheInputBack()
		s.screen.cursorIsAt(0, 125)
		s.pushes(-1, 0, 2*dragReportsToCross)

		s.expectJournal(story(
			then(tookInput),
			gets(desk, enteredAt(centre, centre)...),
			gets(desk, press(event.ButtonLeft), event.Sync()),
			gets(desk, positioned(32867, centre)...),
			then(closed(desk), gaveInputBack),
		))
	})
	t.Run("the origin is the central, the drag crossed to B, and B is lost", func(t *testing.T) {
		s := startSharingWithLayout(t, "laptop me desk", desk, laptop)
		lost := s.connectTakingDrags(desk)
		s.connectTakingDrags(laptop)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 1, noDock)
		s.pushes(1, 0, dragReportsToCross)
		lost.loseConnection()
		s.waitsForTheInputBack()
		s.screen.cursorIsAt(0, 125)
		s.pushes(-1, 0, 2*dragReportsToCross)

		s.expectJournal(story(
			then(dragged(thisMachine, thisMachine, "desk", 1), tookInput),
			gets(desk, enteredAt(0, 16384)...),
			then(entered(thisMachine, thisMachine, "desk", 1)),
			then(closed(desk), gaveInputBack),
		))
	})
	t.Run("the machine holding the drag connects again, so its input is given back", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		s.connectTakingDrags(desk)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 1, noDock)
		s.pushes(1, 0, dragReportsToCross)
		s.connectTakingDrags(desk)
		s.pushes(1, 0, 2*dragReportsToCross)

		s.expectJournal(story(
			then(dragged(thisMachine, thisMachine, "desk", 1), tookInput),
			gets(desk, enteredAt(0, 16384)...),
			then(entered(thisMachine, thisMachine, "desk", 1)),
			then(gaveInputBack, closed(desk)),
		))
	})
	t.Run("the origin side is forgotten while the drag leaves it, so the drag ends without entering", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop tablet", desk, laptop, tablet)
		leaving := s.connect(desk)
		s.connectTakingDrags(laptop)
		s.connectTakingDrags(tablet)

		s.focusOn(2)
		s.moves(400, 0)
		s.holdsTheButton()
		s.dragStartsOn("desk", 1, noDock)
		leaving.leaveFails.Store(true)
		s.pushes(1, 0, dragReportsToCross)
		s.moves(700, 0)
		s.pushes(1, 0, 2*dragReportsToCross-1)

		s.expectJournal(story(
			then(tookInput),
			gets(desk, enteredAt(centre, centre)...),
			gets(desk, positioned(farEdge, centre)...),
			gets(desk, press(event.ButtonLeft), event.Sync()),
			gets(desk, times(dragReportsToCross, positioned(farEdge, centre)...)...),
			then(dragged("desk", "desk", "laptop", 1), closed(desk)),
			gets(laptop, enteredAt(0, centre)...),
			gets(laptop, times(2*dragReportsToCross, positioned(farEdge, centre)...)...),
		))
	})
	t.Run("the neighbour cannot be entered, so the drag ends without entering", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		refusing := s.connectTakingDrags(desk)
		refusing.sendFails.Store(true)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 1, noDock)
		s.pushes(1, 0, dragReportsToCross)
		s.connectTakingDrags(desk)
		s.pushes(1, 0, 2*dragReportsToCross)

		s.expectJournal(then(dragged(thisMachine, thisMachine, "desk", 1), tookInput, gaveInputBack, closed(desk)))
	})
}

func TestADragThatStartsOnASideMachine(t *testing.T) {
	startOnDesk := func(s *sharing, dock edge.Side, dx int) {
		s.focusOn(2)
		s.moves(dx, 0)
		s.holdsTheButton()
		s.dragStartsOn("desk", 3, dock)
	}
	holdingOnDesk := func(x int) []string {
		return story(
			then(tookInput),
			gets(desk, enteredAt(centre, centre)...),
			gets(desk, positioned(x, centre)...),
			gets(desk, press(event.ButtonLeft), event.Sync()),
		)
	}

	t.Run("A to B to A: the origin side gets no release, and back at the origin the drag ends", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
		s.connect(desk)
		s.connectTakingDrags(laptop)

		startOnDesk(s, noDock, 400)
		s.pushes(1, 0, dragReportsToCross)
		s.moves(-700, 0)
		s.pushes(-1, 0, dragReportsToCross-1)
		s.pushes(1, 0, 2*dragReportsToCross)

		s.expectJournal(story(
			holdingOnDesk(farEdge),
			gets(desk, times(dragReportsToCross, positioned(farEdge, centre)...)...),
			then(dragged("desk", "desk", "laptop", 3)),
			gets(desk, event.Sync(), event.Leave()),
			gets(laptop, enteredAt(0, centre)...),
			then(entered("desk", "desk", "laptop", 3)),
			gets(laptop, times(dragReportsToCross, positioned(0, centre)...)...),
			then(dragged("desk", "laptop", "desk", 3)),
			gets(laptop, event.Leave()),
			gets(desk, enteredAt(farEdge, centre)...),
			then(entered("desk", "laptop", "desk", 3)),
			gets(desk, times(2*dragReportsToCross, positioned(farEdge, centre)...)...),
		))
	})
	t.Run("the origin side gets the release of every other key, but not of the button", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
		s.connect(desk)
		s.connectTakingDrags(laptop)

		s.focusOn(2)
		s.moves(400, 0)
		s.captures(press(keyLeftShift), event.Sync())
		s.holdsTheButton()
		s.dragStartsOn("desk", 3, noDock)
		s.pushes(1, 0, dragReportsToCross)

		s.expectJournal(story(
			then(tookInput),
			gets(desk, enteredAt(centre, centre)...),
			gets(desk, positioned(farEdge, centre)...),
			gets(desk, press(keyLeftShift), event.Sync()),
			gets(desk, press(event.ButtonLeft), event.Sync()),
			gets(desk, times(dragReportsToCross, positioned(farEdge, centre)...)...),
			then(dragged("desk", "desk", "laptop", 3)),
			gets(desk, release(keyLeftShift), event.Sync(), event.Leave()),
			gets(laptop, enteredAt(0, centre)...),
			then(entered("desk", "desk", "laptop", 3)),
		))
	})
	t.Run("toward the central, which shares files but does not take dragged files", func(t *testing.T) {
		s := newSharing(t, desk)
		s.features = announcingHere(features.Clipboard, features.Files)
		s.desk.Store(drawn(t, "me desk"))
		s.startWithAScreen(ownDesktop)
		s.connectTakingDrags(desk)

		startOnDesk(s, noDock, -400)
		s.pushes(-1, 0, 2*dragReportsToCross)

		s.expectJournal(story(
			holdingOnDesk(0),
			gets(desk, times(dragReportsToCross, positioned(0, centre)...)...),
			then(cannotReach(thisMachine)),
			gets(desk, times(dragReportsToCross, positioned(0, centre)...)...),
		))
	})
	t.Run("toward the central, which has file sharing off", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		s.connectTakingDrags(desk)

		startOnDesk(s, noDock, -400)
		s.pushes(-1, 0, 2*dragReportsToCross)

		s.expectJournal(story(
			holdingOnDesk(0),
			gets(desk, times(dragReportsToCross, positioned(0, centre)...)...),
			then(filesOff(thisMachine)),
			gets(desk, times(dragReportsToCross, positioned(0, centre)...)...),
		))
	})
	t.Run("toward the central, which takes dragged files", func(t *testing.T) {
		s := newSharing(t, desk)
		s.features = announcingHere(features.Clipboard, features.Files, features.Drag)
		s.desk.Store(drawn(t, "me desk"))
		s.startWithAScreen(ownDesktop)
		s.connectTakingDrags(desk)

		startOnDesk(s, noDock, -400)
		s.pushes(-1, 0, dragReportsToCross)

		s.expectJournal(story(
			holdingOnDesk(0),
			gets(desk, times(dragReportsToCross, positioned(0, centre)...)...),
			then(dragged("desk", "desk", thisMachine, 3)),
			gets(desk, event.Sync(), event.Leave()),
			then(gaveInputBack, cursorPlacedAt(1000, 250), entered("desk", "desk", thisMachine, 3)),
		))
	})
	t.Run("into a central whose own drags stay there, and on to the origin and to a machine without bit 2", func(t *testing.T) {
		s := newSharing(t, desk, laptop)
		s.features = announcingHere(features.Clipboard, features.Files, features.Drag)
		s.dragsStay = true
		s.desk.Store(drawn(t, "laptop me desk"))
		s.startWithAScreen(ownDesktop)
		s.connect(desk)
		s.connectNotTakingDrags(laptop)

		startOnDesk(s, noDock, -400)
		s.pushes(-1, 0, dragReportsToCross)
		s.screen.cursorIsAt(0, 125)
		s.pushes(-1, 0, dragReportsToCross)
		s.screen.cursorIsAt(1000, 125)
		s.pushes(1, 0, dragReportsToCross)

		s.expectJournal(story(
			holdingOnDesk(0),
			gets(desk, times(dragReportsToCross, positioned(0, centre)...)...),
			then(dragged("desk", "desk", thisMachine, 3)),
			gets(desk, event.Sync(), event.Leave()),
			then(gaveInputBack, cursorPlacedAt(1000, 250), entered("desk", "desk", thisMachine, 3)),
			then(cannotReach("laptop")),
			then(dragged("desk", thisMachine, "desk", 3), tookInput),
			gets(desk, enteredAt(0, 16384)...),
			then(entered("desk", thisMachine, "desk", 3)),
		))
	})
	t.Run("not through the edge where the Dock of the side machine sits", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
		s.connectTakingDrags(desk)
		s.connectTakingDrags(laptop)

		startOnDesk(s, edge.Right, 400)
		s.pushes(1, 0, 2*dragReportsToCross)

		s.expectJournal(story(
			holdingOnDesk(farEdge),
			gets(desk, times(2*dragReportsToCross, positioned(farEdge, centre)...)...),
		))
	})
	t.Run("not through the edge where the Dock set for the side machine sits", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
		s.connectTakingDrags(desk)
		s.connectTakingDrags(laptop)
		s.sharer.SetDock("desk", edge.Right)

		startOnDesk(s, noDock, 400)
		s.pushes(1, 0, 2*dragReportsToCross)

		s.expectJournal(story(
			holdingOnDesk(farEdge),
			gets(desk, times(2*dragReportsToCross, positioned(farEdge, centre)...)...),
		))
	})
	t.Run("the drag ending on the origin side before it crossed ends it", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
		s.connectTakingDrags(desk)
		s.connectTakingDrags(laptop)

		startOnDesk(s, noDock, 400)
		s.pushes(1, 0, 30)
		s.sharer.SourceDragEnded("desk")
		s.pushes(1, 0, 2*dragReportsToCross)

		s.expectJournal(story(
			holdingOnDesk(farEdge),
			gets(desk, times(30+2*dragReportsToCross, positioned(farEdge, centre)...)...),
		))
	})
	t.Run("a drag told for the central while a side machine has the cursor is ignored", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
		s.connectTakingDrags(desk)
		s.connectTakingDrags(laptop)

		s.focusOn(2)
		s.moves(400, 0)
		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 3, noDock)
		s.dragStartsOn("laptop", 3, noDock)
		s.pushes(1, 0, 2*dragReportsToCross)

		s.expectJournal(story(
			holdingOnDesk(farEdge),
			gets(desk, times(2*dragReportsToCross, positioned(farEdge, centre)...)...),
		))
	})
}

func TestTheCentralSaysOncePerRunForEachMachineThatADraggedFileCannotReach(t *testing.T) {
	s := startSharingWithLayout(t, "laptop me desk", desk, laptop)
	s.connectNotTakingDrags(desk)
	s.connectNotTakingDrags(laptop)

	for range 2 {
		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 1, noDock)
		s.screen.cursorIsAt(1000, 125)
		s.pushes(1, 0, dragReportsToCross)
		s.screen.cursorIsAt(0, 125)
		s.pushes(-1, 0, dragReportsToCross)
		s.releasesTheButton()
	}

	s.expectJournal(then(cannotReach("desk"), cannotReach("laptop")))
}

func TestANoticeIsTakenBackOnceTheMachineAnnouncesWhatAllowsTheDrag(t *testing.T) {
	s := startSharingWithLayout(t, "me desk", desk)
	receiver := s.connectSaying(desk)
	s.screen.cursorIsAt(1000, 125)
	dragAcross := func() {
		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 1, noDock)
		s.pushes(1, 0, dragReportsToCross)
		s.releasesTheButton()
	}

	dragAcross()
	s.hears(receiver, filesAreOff)
	dragAcross()
	dragAcross()
	s.hears(receiver, takesNoDrags)
	dragAcross()
	s.hears(receiver, takesNoDrags)
	dragAcross()
	s.hears(receiver, takesDrags)
	dragAcross()

	s.expectJournal(story(
		then(
			filesOff("desk"),
			takenBack("desk", FilesOffThere),
			cannotReach("desk"),
			takenBack("desk", DragCannotReach),
			dragged(thisMachine, thisMachine, "desk", 1),
			tookInput,
		),
		gets(desk, enteredAt(0, 16384)...),
		then(entered(thisMachine, thisMachine, "desk", 1)),
		gets(desk, release(event.ButtonLeft), event.Sync()),
	))
}

func TestTheClipboardAndTheEdgesHearTheSameFeaturesFrame(t *testing.T) {
	s := newSharing(t, desk)
	s.clipboard = recordedClipboard{journal: s.journal}
	s.desk.Store(drawn(t, "me desk"))
	s.startWithAScreen(ownDesktop)
	s.connectTakingDrags(desk)
	s.screen.cursorIsAt(1000, 125)

	s.holdsTheButton()
	s.dragStartsOn(thisMachine, 1, noDock)
	s.pushes(1, 0, dragReportsToCross)

	s.expectInOrder(
		clipboardHears("desk connected"),
		clipboardHears(fmt.Sprintf("desk says %+v", takesDrags)),
		dragged(thisMachine, thisMachine, "desk", 1),
		tookInput,
		entered(thisMachine, thisMachine, "desk", 1),
	)
}

func dragEnds(drag Drag) string {
	return fmt.Sprintf("the drag of %d files from %s ended", drag.Files, drag.Origin)
}

func ended(origin string, files int) string {
	return dragEnds(Drag{Origin: origin, Files: files})
}

func (s *sharing) dragEnded(drag Drag) {
	if s.sharer.drag != nil {
		s.journal.note("a drag is still in progress when the end of the last one is told")
	}
	s.dragsEnded.note(dragEnds(drag))
	if s.endsShown {
		s.journal.note(dragEnds(drag))
	}
	if s.whenEnded != nil {
		s.whenEnded(drag)
	}
}

func (s *sharing) expectDragsEnded(want ...string) {
	s.t.Helper()
	s.eventually("telling the end of every drag", func() bool { return len(s.dragsEnded.read()) >= len(want) })
	if got := s.dragsEnded.read(); !slices.Equal(got, want) {
		s.t.Errorf("the ends of drags told are %q, want %q", got, want)
	}
}

func startShowingTheEndOfDrags(t *testing.T, desk string, peers ...trust.Peer) *sharing {
	t.Helper()
	s := newSharing(t, peers...)
	s.endsShown = true
	s.desk.Store(drawn(t, desk))
	s.startWithAScreen(ownDesktop)
	return s
}

func TestTheEndOfADragIsToldOnceForEachReasonItEnds(t *testing.T) {
	t.Run("the left button is released", func(t *testing.T) {
		s := startShowingTheEndOfDrags(t, "me desk", desk)
		s.connectTakingDrags(desk)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 1, noDock)
		s.moves(0, 1)
		s.releasesTheButton()
		s.pushes(1, 0, reportsToCross)

		s.expectJournal(story(
			then(ended(thisMachine, 1), tookInput),
			gets(desk, enteredAt(0, 16384)...),
		))
		s.expectDragsEnded(ended(thisMachine, 1))
	})
	t.Run("the drag is cancelled", func(t *testing.T) {
		s := startShowingTheEndOfDrags(t, "me desk", desk)
		s.connectTakingDrags(desk)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 2, noDock)
		s.moves(0, 1)
		s.sharer.DragCancelled()
		s.pushes(1, 0, 2*dragReportsToCross)
		s.releasesTheButton()

		s.expectJournal(then(ended(thisMachine, 2)))
		s.expectDragsEnded(ended(thisMachine, 2))
	})
	t.Run("the drag ends on its origin before it crossed", func(t *testing.T) {
		s := startShowingTheEndOfDrags(t, "me desk", desk)
		s.connectTakingDrags(desk)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 3, noDock)
		s.moves(0, 1)
		s.sharer.SourceDragEnded(thisMachine)
		s.pushes(1, 0, 2*dragReportsToCross)
		s.releasesTheButton()

		s.expectJournal(then(ended(thisMachine, 3)))
		s.expectDragsEnded(ended(thisMachine, 3))
	})
	t.Run("the drag crosses back to its origin", func(t *testing.T) {
		s := startShowingTheEndOfDrags(t, "me desk", desk)
		s.connectTakingDrags(desk)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 4, noDock)
		s.pushes(1, 0, dragReportsToCross)
		s.pushes(-1, 0, dragReportsToCross)
		s.releasesTheButton()

		s.expectJournal(story(
			then(dragged(thisMachine, thisMachine, "desk", 4), tookInput),
			gets(desk, enteredAt(0, 16384)...),
			then(entered(thisMachine, thisMachine, "desk", 4)),
			gets(desk, times(dragReportsToCross, positioned(0, 16384)...)...),
			then(dragged(thisMachine, "desk", thisMachine, 4)),
			gets(desk, event.Leave()),
			then(gaveInputBack, cursorPlacedAt(1000, 125), entered(thisMachine, "desk", thisMachine, 4), ended(thisMachine, 4)),
		))
		s.expectDragsEnded(ended(thisMachine, 4))
	})
	t.Run("the origin side is lost while it has the cursor", func(t *testing.T) {
		s := startShowingTheEndOfDrags(t, "me desk", desk)
		lost := s.connect(desk)

		s.focusOn(2)
		s.holdsTheButton()
		s.dragStartsOn("desk", 5, noDock)
		s.moves(1, 0)
		lost.loseConnection()
		s.waitsForTheInputBack()

		s.expectJournal(story(
			then(tookInput),
			gets(desk, enteredAt(centre, centre)...),
			gets(desk, press(event.ButtonLeft), event.Sync()),
			gets(desk, positioned(32867, centre)...),
			then(closed(desk), ended("desk", 5), gaveInputBack),
		))
		s.expectDragsEnded(ended("desk", 5))
	})
	t.Run("the machine holding the drag connects again", func(t *testing.T) {
		s := startShowingTheEndOfDrags(t, "me desk", desk)
		s.connectTakingDrags(desk)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 6, noDock)
		s.pushes(1, 0, dragReportsToCross)
		s.connectTakingDrags(desk)
		s.moves(0, 1)

		s.expectJournal(story(
			then(dragged(thisMachine, thisMachine, "desk", 6), tookInput),
			gets(desk, enteredAt(0, 16384)...),
			then(entered(thisMachine, thisMachine, "desk", 6)),
			then(ended(thisMachine, 6), gaveInputBack, closed(desk)),
		))
		s.expectDragsEnded(ended(thisMachine, 6))
	})
	t.Run("the origin side is forgotten while the drag leaves it", func(t *testing.T) {
		s := startShowingTheEndOfDrags(t, "me desk laptop", desk, laptop)
		leaving := s.connect(desk)
		s.connectTakingDrags(laptop)

		s.focusOn(2)
		s.moves(400, 0)
		s.holdsTheButton()
		s.dragStartsOn("desk", 7, noDock)
		leaving.leaveFails.Store(true)
		s.pushes(1, 0, dragReportsToCross)

		s.expectJournal(story(
			then(tookInput),
			gets(desk, enteredAt(centre, centre)...),
			gets(desk, positioned(farEdge, centre)...),
			gets(desk, press(event.ButtonLeft), event.Sync()),
			gets(desk, times(dragReportsToCross, positioned(farEdge, centre)...)...),
			then(dragged("desk", "desk", "laptop", 7), ended("desk", 7), closed(desk)),
			gets(laptop, enteredAt(0, centre)...),
		))
		s.expectDragsEnded(ended("desk", 7))
	})
	t.Run("the neighbour cannot be entered", func(t *testing.T) {
		s := startShowingTheEndOfDrags(t, "me desk", desk)
		refusing := s.connectTakingDrags(desk)
		refusing.sendFails.Store(true)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 8, noDock)
		s.pushes(1, 0, dragReportsToCross)

		s.expectJournal(then(dragged(thisMachine, thisMachine, "desk", 8), tookInput, ended(thisMachine, 8), gaveInputBack, closed(desk)))
		s.expectDragsEnded(ended(thisMachine, 8))
	})
}

func TestTheEndOfEachDragIsToldExactlyOnce(t *testing.T) {
	s := startSharingWithLayout(t, "me desk", desk)
	s.connectTakingDrags(desk)
	s.screen.cursorIsAt(1000, 125)

	s.holdsTheButton()
	s.dragStartsOn(thisMachine, 1, noDock)
	s.moves(0, 1)
	s.sharer.DragCancelled()
	s.sharer.DragCancelled()
	s.sharer.SourceDragEnded(thisMachine)
	s.moves(0, 1)
	s.releasesTheButton()

	s.holdsTheButton()
	s.dragStartsOn(thisMachine, 2, noDock)
	s.pushes(1, 0, dragReportsToCross)
	s.pushes(-1, 0, dragReportsToCross)
	s.sharer.DragCancelled()
	s.releasesTheButton()

	s.holdsTheButton()
	s.dragStartsOn(thisMachine, 3, noDock)
	s.pushes(1, 0, dragReportsToCross)
	s.sharer.SourceDragEnded(thisMachine)
	s.releasesTheButton()
	s.sharer.DragCancelled()
	s.moves(0, 1)

	s.expectDragsEnded(ended(thisMachine, 1), ended(thisMachine, 2), ended(thisMachine, 3))
}

func TestInputsCanBeCalledFromTheEndOfADrag(t *testing.T) {
	s := newSharing(t, desk)
	s.desk.Store(drawn(t, "me desk"))
	calledOnce := false
	s.whenEnded = func(Drag) {
		if calledOnce {
			return
		}
		calledOnce = true
		s.sharer.DragCancelled()
		s.sharer.SourceDragEnded(thisMachine)
		s.sharer.SetDock(thisMachine, edge.Top)
		s.sharer.DragStarted(Drag{Origin: "desk", Files: 9})
	}
	s.startWithAScreen(ownDesktop)
	s.connectTakingDrags(desk)
	s.screen.cursorIsAt(1000, 125)

	s.holdsTheButton()
	s.dragStartsOn(thisMachine, 1, noDock)
	s.moves(0, 1)
	s.releasesTheButton()
	s.holdsTheButton()
	s.dragStartsOn(thisMachine, 2, noDock)
	s.pushes(1, 0, dragReportsToCross)

	s.expectJournal(story(
		then(dragged(thisMachine, thisMachine, "desk", 2), tookInput),
		gets(desk, enteredAt(0, 16384)...),
		then(entered(thisMachine, thisMachine, "desk", 2)),
	))
	s.expectDragsEnded(ended(thisMachine, 1))
}
