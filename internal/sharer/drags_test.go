package sharer

import (
	"fmt"
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
)

func dragCrosses(crossing DragCrossing) string {
	return fmt.Sprintf("a drag of %d files from %s crosses from %s to %s with button %#x held",
		crossing.Files, crossing.Origin, crossing.From, crossing.To, crossing.Button)
}

func dragIsBlocked(blocked BlockedDrag) string {
	switch blocked.Block {
	case DragCannotReach:
		return "the central says a dragged file cannot reach " + blocked.Machine
	case DragCannotLeave:
		return "the central says a dragged file cannot leave " + blocked.Machine
	}
	return "the central says something unknown about " + blocked.Machine
}

func dragged(origin, from, to string, files int) string {
	return dragCrosses(DragCrossing{Origin: origin, From: from, To: to, Files: files, Button: event.ButtonLeft})
}

func cannotReach(machine string) string {
	return dragIsBlocked(BlockedDrag{Machine: machine, Block: DragCannotReach})
}

func cannotLeave(machine string) string {
	return dragIsBlocked(BlockedDrag{Machine: machine, Block: DragCannotLeave})
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

func takingDragsHere() *features.Set {
	own := new(features.Set)
	own.Turn(features.Drag, true)
	return own
}

func TestAFileDraggedOnTheCentralCrossesOnlyUnderTheRulesOfADrag(t *testing.T) {
	crossedToDesk := story(
		then(dragged(thisMachine, thisMachine, "desk", 2), tookInput),
		gets(desk, enteredAt(0, 16384)...),
	)
	tests := []struct {
		name      string
		layout    string
		dragsStay bool
		cursorY   int
		connect   func(s *sharing)
		act       func(s *sharing)
		want      []string
	}{
		{
			name: "it crosses with the button held after a push of 600 ms toward a neighbour that takes dragged files",
			act: func(s *sharing) {
				s.holdsTheButton()
				s.dragStartsOn(thisMachine, 2, noDock)
				s.pushes(1, 0, dragReportsToCross)
			},
			want: crossedToDesk,
		},
		{
			name: "it does not cross after the push of the cursor alone, nor just before 600 ms",
			act: func(s *sharing) {
				s.holdsTheButton()
				s.dragStartsOn(thisMachine, 2, noDock)
				s.pushes(1, 0, dragReportsToCross-1)
			},
			want: nil,
		},
		{
			name: "a button held without a file drag never crosses",
			act: func(s *sharing) {
				s.holdsTheButton()
				s.pushes(1, 0, 3*dragReportsToCross)
			},
			want: nil,
		},
		{
			name: "a drag told before the button is held is ignored",
			act: func(s *sharing) {
				s.dragStartsOn(thisMachine, 2, noDock)
				s.holdsTheButton()
				s.pushes(1, 0, 3*dragReportsToCross)
			},
			want: nil,
		},
		{
			name: "a drag told for a machine that does not have the cursor is ignored",
			act: func(s *sharing) {
				s.holdsTheButton()
				s.dragStartsOn("desk", 2, noDock)
				s.pushes(1, 0, 3*dragReportsToCross)
			},
			want: nil,
		},
		{
			name:    "toward a neighbour that does not take dragged files nothing crosses, and the central says so once",
			connect: func(s *sharing) { s.connectNotTakingDrags(desk) },
			act: func(s *sharing) {
				s.holdsTheButton()
				s.dragStartsOn(thisMachine, 2, noDock)
				s.pushes(1, 0, 3*dragReportsToCross)
				s.releasesTheButton()
				s.holdsTheButton()
				s.dragStartsOn(thisMachine, 1, noDock)
				s.pushes(1, 0, 2*dragReportsToCross)
			},
			want: then(cannotReach("desk")),
		},
		{
			name:    "a neighbour that announced nothing does not take dragged files",
			connect: func(s *sharing) { s.connect(desk) },
			act: func(s *sharing) {
				s.holdsTheButton()
				s.dragStartsOn(thisMachine, 2, noDock)
				s.pushes(1, 0, 2*dragReportsToCross)
			},
			want: then(cannotReach("desk")),
		},
		{
			name: "a neighbour that stopped taking dragged files is not crossed",
			connect: func(s *sharing) {
				receiver := s.connectTakingDrags(desk)
				s.hears(receiver, takesNoDrags)
			},
			act: func(s *sharing) {
				s.holdsTheButton()
				s.dragStartsOn(thisMachine, 2, noDock)
				s.pushes(1, 0, 2*dragReportsToCross)
			},
			want: then(cannotReach("desk")),
		},
		{
			name:    "a neighbour that is not connected stays a plain edge, with nothing said",
			layout:  "me laptop desk",
			connect: func(s *sharing) { s.connectTakingDrags(desk) },
			act: func(s *sharing) {
				s.holdsTheButton()
				s.dragStartsOn(thisMachine, 2, noDock)
				s.pushes(1, 0, 2*dragReportsToCross)
			},
			want: nil,
		},
		{
			name:    "in a corner nothing crosses",
			cursorY: 10,
			act: func(s *sharing) {
				s.holdsTheButton()
				s.dragStartsOn(thisMachine, 2, noDock)
				s.pushes(1, 0, 3*dragReportsToCross)
			},
			want: nil,
		},
		{
			name: "nothing crosses through the edge where the Dock sits, and nothing is said",
			act: func(s *sharing) {
				s.holdsTheButton()
				s.dragStartsOn(thisMachine, 2, edge.Right)
				s.pushes(1, 0, 3*dragReportsToCross)
			},
			want: nil,
		},
		{
			name: "the Dock on another edge leaves this one crossing",
			act: func(s *sharing) {
				s.holdsTheButton()
				s.dragStartsOn(thisMachine, 2, edge.Bottom)
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
			want: nil,
		},
		{
			name: "a key held on the central keeps blocking, so it is not left held here",
			act: func(s *sharing) {
				s.captures(press(keyLeftShift), event.Sync())
				s.holdsTheButton()
				s.dragStartsOn(thisMachine, 2, noDock)
				s.pushes(1, 0, 3*dragReportsToCross)
			},
			want: nil,
		},
		{
			name: "the drag ending before the push time blocks again while the button stays held",
			act: func(s *sharing) {
				s.holdsTheButton()
				s.dragStartsOn(thisMachine, 2, noDock)
				s.pushes(1, 0, 30)
				s.sharer.DragEnded()
				s.pushes(1, 0, 3*dragReportsToCross)
			},
			want: nil,
		},
		{
			name: "the button released before the push time ends the drag, and the cursor alone crosses after its own push",
			act: func(s *sharing) {
				s.holdsTheButton()
				s.dragStartsOn(thisMachine, 2, noDock)
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
				s.holdsTheButton()
				s.dragStartsOn(thisMachine, 2, noDock)
				s.pushes(1, 0, 3*dragReportsToCross)
				s.releasesTheButton()
				s.holdsTheButton()
				s.dragStartsOn(thisMachine, 2, noDock)
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
		gets(desk, positioned(300, 16384)...),
		gets(desk, release(event.ButtonLeft), event.Sync()),
	))
}

func TestADragInProgressCrossesOnAndBackUnderTheSameRules(t *testing.T) {
	t.Run("A to B to A: back to the machine where the drag started, which needs no bit 2", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		s.connectTakingDrags(desk)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 1, noDock)
		s.pushes(1, 0, dragReportsToCross)
		s.pushes(-1, 0, dragReportsToCross)

		s.expectJournal(story(
			then(dragged(thisMachine, thisMachine, "desk", 1), tookInput),
			gets(desk, enteredAt(0, 16384)...),
			gets(desk, times(dragReportsToCross, positioned(0, 16384)...)...),
			then(dragged(thisMachine, "desk", thisMachine, 1)),
			gets(desk, event.Leave()),
			then(gaveInputBack, cursorPlacedAt(1000, 125)),
		))
	})
	t.Run("A to B to C: on to a third machine that takes dragged files", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
		s.connectTakingDrags(desk)
		s.connectTakingDrags(laptop)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 1, noDock)
		s.pushes(1, 0, dragReportsToCross)
		s.moves(700, 0)
		s.pushes(1, 0, dragReportsToCross-1)

		s.expectJournal(story(
			then(dragged(thisMachine, thisMachine, "desk", 1), tookInput),
			gets(desk, enteredAt(0, 16384)...),
			gets(desk, times(dragReportsToCross, positioned(farEdge, 16384)...)...),
			then(dragged(thisMachine, "desk", "laptop", 1)),
			gets(desk, event.Leave()),
			gets(laptop, enteredAt(0, 16384)...),
		))
	})
	t.Run("A to B to C: not on to a third machine that does not take dragged files", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
		s.connectTakingDrags(desk)
		s.connectNotTakingDrags(laptop)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 1, noDock)
		s.pushes(1, 0, dragReportsToCross)
		s.moves(700, 0)
		s.pushes(1, 0, 2*dragReportsToCross-1)

		s.expectJournal(story(
			then(dragged(thisMachine, thisMachine, "desk", 1), tookInput),
			gets(desk, enteredAt(0, 16384)...),
			gets(desk, times(dragReportsToCross, positioned(farEdge, 16384)...)...),
			then(cannotReach("laptop")),
			gets(desk, times(dragReportsToCross, positioned(farEdge, 16384)...)...),
		))
	})
	t.Run("A to B to C: not on before the longer push", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
		s.connectTakingDrags(desk)
		s.connectTakingDrags(laptop)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 1, noDock)
		s.pushes(1, 0, dragReportsToCross)
		s.moves(700, 0)
		s.pushes(1, 0, dragReportsToCross-2)

		s.expectJournal(story(
			then(dragged(thisMachine, thisMachine, "desk", 1), tookInput),
			gets(desk, enteredAt(0, 16384)...),
			gets(desk, times(dragReportsToCross-1, positioned(farEdge, 16384)...)...),
		))
	})
	t.Run("A to B, then the drag ends and the held button blocks again", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		s.connectTakingDrags(desk)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 1, noDock)
		s.pushes(1, 0, dragReportsToCross)
		s.sharer.DragEnded()
		s.pushes(-1, 0, 2*dragReportsToCross)

		s.expectJournal(story(
			then(dragged(thisMachine, thisMachine, "desk", 1), tookInput),
			gets(desk, enteredAt(0, 16384)...),
			gets(desk, times(2*dragReportsToCross, positioned(0, 16384)...)...),
		))
	})
	t.Run("A to B, then the button is released and the cursor alone crosses back without a drag", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
		s.connectTakingDrags(desk)
		s.screen.cursorIsAt(1000, 125)

		s.holdsTheButton()
		s.dragStartsOn(thisMachine, 1, noDock)
		s.pushes(1, 0, dragReportsToCross)
		s.releasesTheButton()
		s.pushes(-1, 0, reportsToCross)

		s.expectJournal(story(
			then(dragged(thisMachine, thisMachine, "desk", 1), tookInput),
			gets(desk, enteredAt(0, 16384)...),
			gets(desk, release(event.ButtonLeft), event.Sync()),
			gets(desk, times(reportsToCross, positioned(0, 16384)...)...),
			gets(desk, event.Leave()),
			then(gaveInputBack, cursorPlacedAt(1000, 125)),
		))
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

	t.Run("A to B to A: on to the next machine, and back to the origin, which needs no bit 2", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk laptop", desk, laptop)
		s.connect(desk)
		s.connectTakingDrags(laptop)

		startOnDesk(s, noDock, 400)
		s.pushes(1, 0, dragReportsToCross)
		s.moves(-700, 0)
		s.pushes(-1, 0, dragReportsToCross-1)

		s.expectJournal(story(
			holdingOnDesk(farEdge),
			gets(desk, times(dragReportsToCross, positioned(farEdge, centre)...)...),
			then(dragged("desk", "desk", "laptop", 3)),
			gets(desk, release(event.ButtonLeft), event.Sync(), event.Leave()),
			gets(laptop, enteredAt(0, centre)...),
			gets(laptop, times(dragReportsToCross, positioned(0, centre)...)...),
			then(dragged("desk", "laptop", "desk", 3)),
			gets(laptop, event.Leave()),
			gets(desk, enteredAt(farEdge, centre)...),
		))
	})
	t.Run("toward the central, which does not take dragged files", func(t *testing.T) {
		s := startSharingWithLayout(t, "me desk", desk)
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
	t.Run("toward the central, which takes dragged files", func(t *testing.T) {
		s := newSharing(t, desk)
		s.features = takingDragsHere()
		s.desk.Store(drawn(t, "me desk"))
		s.startWithAScreen(ownDesktop)
		s.connectTakingDrags(desk)

		startOnDesk(s, noDock, -400)
		s.pushes(-1, 0, dragReportsToCross)

		s.expectJournal(story(
			holdingOnDesk(0),
			gets(desk, times(dragReportsToCross, positioned(0, centre)...)...),
			then(dragged("desk", "desk", thisMachine, 3)),
			gets(desk, release(event.ButtonLeft), event.Sync(), event.Leave()),
			then(gaveInputBack, cursorPlacedAt(1000, 250)),
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
	)
}
