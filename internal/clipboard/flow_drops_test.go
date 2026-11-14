package clipboard

import (
	"errors"
	"slices"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/features"
)

var central = Place[string]{}

func fetchDrop(from string, transfer int) action {
	return action{Do: Fetch, To: from, Transfer: transfer, Drop: true}
}

func deliverDrop(to string, transfer int, files FileCopy) action {
	return action{Do: Deliver, To: to, Transfer: transfer, FileCopy: files, Drop: true}
}

func startDrop(transfer int, files FileCopy) action {
	return action{Do: StartDrop, Transfer: transfer, FileCopy: files}
}

func cancelDropOn(side string, transfer int) action {
	return action{Do: CancelDrop, To: side, Transfer: transfer}
}

func stopDrop(transfer int) action   { return action{Do: Stop, Transfer: transfer, Drop: true} }
func finishDrop(transfer int) action { return action{Do: FinishDrop, Transfer: transfer} }
func cancelDrop(transfer int) action { return action{Do: CancelDrop, Transfer: transfer} }

func sideStartsDrop(transfer int, files FileCopy) toCentral {
	return toCentral{Do: StartDrop, Transfer: transfer, FileCopy: files}
}

func sideReceivesDrop(transfer int) toCentral {
	return toCentral{Do: ReceiveFiles, Transfer: transfer, Drop: true}
}

func sideSendsDrop(transfer int) toCentral {
	return toCentral{Do: SendFiles, Transfer: transfer, Drop: true}
}
func sideStopsDrop(transfer int) toCentral {
	return toCentral{Do: Stop, Transfer: transfer, Drop: true}
}
func sideFinishesDrop(transfer int) toCentral { return toCentral{Do: FinishDrop, Transfer: transfer} }
func sideCancelsDrop(transfer int) toCentral  { return toCentral{Do: CancelDrop, Transfer: transfer} }
func dropTaken(transfer int) toCentral        { return toCentral{Do: DropTaken, Transfer: transfer} }

func deskTakingDrops(t *testing.T, sides ...string) *Desk[string] {
	t.Helper()
	d := deskSharingFiles(t, sides...)
	expect(t, "the central takes drops", d.CentralTakesDrops(true))
	for _, side := range sides {
		expect(t, side+" takes drops", d.SaysDrops(side, true))
	}
	return d
}

func expectDrops(t *testing.T, step string, d *Desk[string], transfers ...int) {
	t.Helper()
	var held []int
	for _, dropped := range d.drops {
		held = append(held, dropped.id)
	}
	if !slices.Equal(held, transfers) {
		t.Errorf("%s: the desk holds the drops %v, want %v", step, held, transfers)
	}
}

func expectSideDrops(t *testing.T, step string, s *Side, transfers ...int) {
	t.Helper()
	var held []int
	for _, dropped := range s.dropsIn {
		held = append(held, dropped.transfer)
	}
	if !slices.Equal(held, transfers) {
		t.Errorf("%s: the side machine holds the drops %v, want %v", step, held, transfers)
	}
}

func crossed(t *testing.T, d *Desk[string], from, to Place[string], files FileCopy) []action {
	t.Helper()
	actions, err := d.DragCrossed(from, to, files)
	if err != nil {
		t.Fatalf("DragCrossed(%+v, %+v, %+v): %v", from, to, files, err)
	}
	return actions
}

func focusMovesTo(d *Desk[string], to Place[string]) []action {
	if to.OnASide {
		return d.FocusOn(to.Side)
	}
	return d.FocusOnCentral()
}

func dragFromAAcross(t *testing.T, d *Desk[string], to Place[string]) {
	t.Helper()
	d.FocusOn("A")
	d.ReadUnchanged()
	focusMovesTo(d, to)
	if to.OnASide {
		expect(t, "the drag crosses from A to "+to.Side, crossed(t, d, OnSide("A"), to, report), fetchDrop("A", 1), deliverDrop(to.Side, 1, report))
		return
	}
	expect(t, "the drag crosses from A to the central", crossed(t, d, OnSide("A"), to, report), fetchDrop("A", 1), startDrop(1, report))
}

func TestADropGoesOnAfterFocusLeavesOnlyOnceItIsTaken(t *testing.T) {
	cases := []struct {
		name         string
		to           Place[string]
		taken        bool
		focusBackOnA []action
		filesArrive  []action
	}{
		{name: "not taken on B", to: OnSide("B"), focusBackOnA: []action{cancelDropOn("B", 1)}},
		{name: "taken on B", to: OnSide("B"), taken: true},
		{name: "not taken on the central", to: central, focusBackOnA: []action{read(), stopDrop(1), cancelDrop(1)}},
		{name: "taken on the central", to: central, taken: true, focusBackOnA: []action{read()}, filesArrive: []action{finishDrop(1)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := deskTakingDrops(t, "A", "B")
			dragFromAAcross(t, d, c.to)
			if c.taken {
				expect(t, "the drop is taken", d.DropTaken(1))
			}
			expect(t, "focus goes back to A", d.FocusOn("A"), c.focusBackOnA...)
			expect(t, "the files of the drop arrive", d.FilesArrived(1), c.filesArrive...)
			expect(t, "the same transfer arrives again", d.FilesArrived(1))
			expectDrops(t, "after its files arrived", d)
		})
	}
}

func TestADropWhoseFilesArriveBeforeItIsTakenFinishesWhenTaken(t *testing.T) {
	d := deskTakingDrops(t, "A")
	dragFromAAcross(t, d, central)
	expect(t, "the files arrive while the button is held", d.FilesArrived(1), finishDrop(1))
	expect(t, "the drop is taken", d.DropTaken(1))
	expect(t, "focus goes to A", d.FocusOn("A"), read())
}

func TestADropWhoseFilesArrivedIsCancelledWhenFocusLeavesBeforeItIsTaken(t *testing.T) {
	d := deskTakingDrops(t, "A")
	dragFromAAcross(t, d, central)
	expect(t, "the files arrive while the button is held", d.FilesArrived(1), finishDrop(1))
	expect(t, "the drag goes back to A", d.FocusOn("A"), read(), cancelDrop(1))
}

func TestADragGoingOnToAThirdMachineFetchesAgainFromItsSource(t *testing.T) {
	t.Run("the crossing reported after focus moves", func(t *testing.T) {
		d := deskTakingDrops(t, "A", "B", "C")
		dragFromAAcross(t, d, OnSide("B"))
		expect(t, "focus goes on to C", d.FocusOn("C"), cancelDropOn("B", 1))
		expect(t, "the drag crosses from B to C", crossed(t, d, OnSide("A"), OnSide("C"), report), fetchDrop("A", 2), deliverDrop("C", 2, report))
		expect(t, "B stops the data of its drop", d.DropStopped(1))
		expectDrops(t, "once B stopped its drop", d, 2)
	})
	t.Run("the crossing reported before focus moves", func(t *testing.T) {
		d := deskTakingDrops(t, "A", "B", "C")
		dragFromAAcross(t, d, OnSide("B"))
		expect(t, "the drag crosses from B to C", crossed(t, d, OnSide("A"), OnSide("C"), report), cancelDropOn("B", 1), fetchDrop("A", 2), deliverDrop("C", 2, report))
		expect(t, "focus goes on to C", d.FocusOn("C"))
		expect(t, "B stops the data of its drop", d.DropStopped(1))
		expectDrops(t, "once B stopped its drop", d, 2)
	})
}

func TestADragBackToItsSourceOpensNoDrop(t *testing.T) {
	d := deskTakingDrops(t, "A")
	actions, err := d.DragCrossed(OnSide("A"), OnSide("A"), report)
	if !errors.Is(err, ErrDragBackToItsSource) || len(actions) != 0 {
		t.Errorf("DragCrossed(A, A) = %+v, %v, want no action and ErrDragBackToItsSource", actions, err)
	}
	actions, err = d.DragCrossed(central, central, report)
	if !errors.Is(err, ErrDragBackToItsSource) || len(actions) != 0 {
		t.Errorf("DragCrossed(central, central) = %+v, %v, want no action and ErrDragBackToItsSource", actions, err)
	}
}

func TestADragOpensNoDropWhereFilesCannotGo(t *testing.T) {
	cases := []struct {
		name  string
		desk  func(t *testing.T) *Desk[string]
		from  Place[string]
		to    Place[string]
		files FileCopy
		want  error
	}{
		{"a side machine without the drag bit", func(t *testing.T) *Desk[string] { return deskSharingFiles(t, "A", "B") }, OnSide("A"), OnSide("B"), report, ErrDropsNotTaken},
		{"files switched off on the central", func(t *testing.T) *Desk[string] { return deskSharing(t, Text, "A") }, central, OnSide("A"), report, ErrFilesNotShared},
		{"a target that takes only text", func(t *testing.T) *Desk[string] {
			d := deskTakingDrops(t, "A", "B")
			d.Says("B", Text)
			return d
		}, OnSide("A"), OnSide("B"), report, ErrFilesNotShared},
		{"a source that takes only text", func(t *testing.T) *Desk[string] {
			d := deskTakingDrops(t, "A", "B")
			d.Says("A", Text)
			return d
		}, OnSide("A"), OnSide("B"), report, ErrFilesNotShared},
		{"a target that is not connected", func(t *testing.T) *Desk[string] { return deskTakingDrops(t, "A") }, OnSide("A"), OnSide("B"), report, ErrFilesNotShared},
		{"no files in the drag", func(t *testing.T) *Desk[string] { return deskTakingDrops(t, "A") }, central, OnSide("A"), FileCopy{}, ErrEmpty},
		{"too many files in the drag", func(t *testing.T) *Desk[string] { return deskTakingDrops(t, "A") }, central, OnSide("A"), FileCopy{FilesAndFolders: FilesAndFoldersLimit + 1}, ErrTooManyFiles},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			actions, err := c.desk(t).DragCrossed(c.from, c.to, c.files)
			if !errors.Is(err, c.want) || len(actions) != 0 {
				t.Errorf("DragCrossed = %+v, %v, want no action and %v", actions, err, c.want)
			}
		})
	}
}

func TestADropFromTheCentralGoesToTheSideMachineAndNeedsNoFetch(t *testing.T) {
	d := deskTakingDrops(t, "A")
	d.FocusOn("A")
	expect(t, "the drag crosses from the central to A", crossed(t, d, central, OnSide("A"), project), deliverDrop("A", 1, project))
	expect(t, "the drop is taken on A", d.DropTaken(1))
	expect(t, "focus comes back", d.FocusOnCentral())
	expect(t, "the files reach A", d.FilesArrived(1))
	expectDrops(t, "after its files reached A", d)
}

func TestADropStopsWhatEverItIsTakenOrNot(t *testing.T) {
	cases := []struct {
		name  string
		to    Place[string]
		taken bool
		event func(d *Desk[string]) []action
		want  []action
	}{
		{"the target loses the drag bit", OnSide("B"), false, func(d *Desk[string]) []action { return d.SaysDrops("B", false) }, []action{stopDrop(1)}},
		{"the target loses the drag bit after taking it", OnSide("B"), true, func(d *Desk[string]) []action { return d.SaysDrops("B", false) }, []action{stopDrop(1)}},
		{"the target is lost", OnSide("B"), true, func(d *Desk[string]) []action { return d.Lost("B") }, []action{stopDrop(1)}},
		{"the target connects again", OnSide("B"), true, func(d *Desk[string]) []action { return d.Connected("B") }, []action{announce("B", Text|Files), stopDrop(1)}},
		{"the source is lost", OnSide("B"), true, func(d *Desk[string]) []action { return d.Lost("A") }, []action{stopDrop(1)}},
		{"the source is lost on the way to the central", central, true, func(d *Desk[string]) []action { return d.Lost("A") }, []action{stopDrop(1), cancelDrop(1)}},
		{"the source stops taking files", OnSide("B"), false, func(d *Desk[string]) []action { return d.Says("A", Text) }, []action{stopDrop(1)}},
		{"files are switched off", OnSide("B"), true, func(d *Desk[string]) []action { return d.Switch(Text) }, []action{announce("A", Text), announce("B", Text), stopDrop(1)}},
		{"files are switched off on the way to the central", central, true, func(d *Desk[string]) []action { return d.Switch(Text) }, []action{announce("A", Text), announce("B", Text), stopDrop(1), cancelDrop(1)}},
		{"the drop is refused on the central", central, false, func(d *Desk[string]) []action { return d.DropRefused(1) }, []action{stopDrop(1)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := deskTakingDrops(t, "A", "B")
			dragFromAAcross(t, d, c.to)
			if c.taken {
				expect(t, "the drop is taken", d.DropTaken(1))
			}
			expect(t, c.name, c.event(d), c.want...)
			expectDrops(t, "after "+c.name, d)
			expect(t, "the files of the stopped drop arrive", d.FilesArrived(1))
			expect(t, "the stopped drop ends", d.DropStopped(1))
		})
	}
}

func TestEscapeOnTheCentralStopsADropNotTakenWhereverItGoes(t *testing.T) {
	t.Run("to a side machine", func(t *testing.T) {
		d := deskTakingDrops(t, "A", "B")
		dragFromAAcross(t, d, OnSide("B"))
		expect(t, "Escape", d.DragCancelled(1), cancelDropOn("B", 1))
		expect(t, "Escape again", d.DragCancelled(1))

		s := sideSharingFiles(t)
		expect(t, "the drop starts on B", s.CentralDropsFiles(1, report), sideStartsDrop(1, report), sideReceivesDrop(1))
		expect(t, "the cancel reaches B", s.CentralCancelsDrop(1), sideCancelsDrop(1), sideStopsDrop(1))
		expect(t, "the files of the cancelled drop arrive", s.FilesArrived(1))

		expect(t, "the stop of its data reaches the central", d.DropStopped(1))
		expectDrops(t, "once the data stopped", d)
	})
	t.Run("to a side machine that has the files already", func(t *testing.T) {
		d := deskTakingDrops(t, "A", "B")
		dragFromAAcross(t, d, OnSide("B"))
		expect(t, "the files reach B while the button is held", d.FilesArrived(1))
		expect(t, "Escape", d.DragCancelled(1), cancelDropOn("B", 1))
		expectDrops(t, "after Escape", d)

		s := sideSharingFiles(t)
		s.CentralDropsFiles(1, report)
		expect(t, "the files arrive on B", s.FilesArrived(1), sideFinishesDrop(1))
		expect(t, "the cancel reaches B", s.CentralCancelsDrop(1), sideCancelsDrop(1))
		expectSideDrops(t, "after the cancel", s)
	})
	t.Run("to the central", func(t *testing.T) {
		d := deskTakingDrops(t, "A")
		dragFromAAcross(t, d, central)
		expect(t, "Escape", d.DragCancelled(1), stopDrop(1), cancelDrop(1))
	})
	t.Run("after the drop was taken", func(t *testing.T) {
		d := deskTakingDrops(t, "A")
		dragFromAAcross(t, d, central)
		expect(t, "the drop is taken", d.DropTaken(1))
		expect(t, "Escape", d.DragCancelled(1))
		expect(t, "the files arrive", d.FilesArrived(1), finishDrop(1))
	})
}

func TestTheDataOfADropToTheCentralEndingEarlyCancelsTheDrop(t *testing.T) {
	d := deskTakingDrops(t, "A")
	dragFromAAcross(t, d, central)
	expect(t, "the drop is taken", d.DropTaken(1))
	d.TransferEnded(1)
	expect(t, "the transfer of the drop ended without its files", d.DropStopped(1), cancelDrop(1))
	expect(t, "it ends again", d.DropStopped(1))
}

func TestANewCopyDuringATakenDropLeavesTheDropAlone(t *testing.T) {
	d := deskTakingDrops(t, "A", "B")
	dragFromAAcross(t, d, OnSide("B"))
	expect(t, "the drop is taken on B", d.DropTaken(1))
	expect(t, "focus comes back to the central", d.FocusOnCentral())
	expect(t, "A has nothing new", d.UnchangedOn("A"))
	expect(t, "B copied some text", d.CopyFrom("B", "notes"), write("notes"))
	expectDrops(t, "after the new copy", d, 1)
	expect(t, "the files of the drop reach B", d.FilesArrived(1))
	expectDrops(t, "after its files reached B", d)
}

func TestADropCrossingDuringAPasteTransferLeavesThePasteToItsOwnRule(t *testing.T) {
	d := deskTakingDrops(t, "A", "B", "C")
	aFileCopyOfAReaches(t, d, "B")
	expect(t, "focus goes on to C, away from the paste on B", d.FocusOn("C"), stop(1))
	expect(t, "a drag of B crosses to C", crossed(t, d, OnSide("B"), OnSide("C"), project), fetchDrop("B", 2), deliverDrop("C", 2, project))
	expect(t, "B had nothing new", d.UnchangedOn("B"), fetch("A", 3), deliver("C", 3))
	expect(t, "the drop is taken on C", d.DropTaken(2))
	expect(t, "focus comes back to the central", d.FocusOnCentral(), stop(3))
	expectDrops(t, "after focus left C", d, 2)
	expect(t, "the files of the drop reach C", d.FilesArrived(2))
	expectDrops(t, "after its files reached C", d)
}

func TestDropsInReadsOnlyTheDragBit(t *testing.T) {
	if !DropsIn(1<<features.Drag | 1<<features.Files) {
		t.Error("DropsIn missed the drag bit")
	}
	if DropsIn(1<<features.Clipboard | 1<<features.Files | 1<<features.Follows) {
		t.Error("DropsIn found a drag bit that is not set")
	}
}

func TestASideMachineServesADropOnlyOnceTakenAfterFocusLeaves(t *testing.T) {
	cases := []struct {
		name        string
		taken       bool
		arrived     bool
		focusLeaves []toCentral
		filesArrive []toCentral
	}{
		{name: "not taken", focusLeaves: []toCentral{{Do: Read}, sideCancelsDrop(7), sideStopsDrop(7)}},
		{name: "not taken, files here", arrived: true, focusLeaves: []toCentral{{Do: Read}, sideCancelsDrop(7)}},
		{name: "taken", taken: true, focusLeaves: []toCentral{{Do: Read}}, filesArrive: []toCentral{sideFinishesDrop(7)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := sideSharingFiles(t)
			expect(t, "the drop starts", s.CentralDropsFiles(7, report), sideStartsDrop(7, report), sideReceivesDrop(7))
			if c.arrived {
				expect(t, "the files arrive while the button is held", s.FilesArrived(7), sideFinishesDrop(7))
			}
			if c.taken {
				expect(t, "the drop is taken here", s.DropTaken(7), dropTaken(7))
			}
			expect(t, "focus leaves", s.FocusLeaves(), c.focusLeaves...)
			expect(t, "the files arrive", s.FilesArrived(7), c.filesArrive...)
			expect(t, "the files arrive again", s.FilesArrived(7))
		})
	}
}

func TestASideMachineEndsADropItRefusedOrThatStoppedAndKeepsOneTaken(t *testing.T) {
	s := sideSharingFiles(t)
	s.CentralDropsFiles(7, report)
	expect(t, "the drop is refused here", s.DropRefused(7), sideStopsDrop(7))
	expect(t, "the refused drop is taken late", s.DropTaken(7))

	s.CentralDropsFiles(8, report)
	expect(t, "a second crossing cancels the drop not taken", s.CentralDropsFiles(9, project), sideCancelsDrop(8), sideStopsDrop(8), sideStartsDrop(9, project), sideReceivesDrop(9))
	expect(t, "the drop is taken here", s.DropTaken(9), dropTaken(9))
	expect(t, "another crossing leaves the taken drop alone", s.CentralDropsFiles(10, report), sideStartsDrop(10, report), sideReceivesDrop(10))
	expect(t, "the data of the new drop stops", s.DropStopped(10), sideCancelsDrop(10))
	expect(t, "the taken drop arrives", s.FilesArrived(9), sideFinishesDrop(9))
	expectSideDrops(t, "at the end", s)
}

func TestASideMachineWithFilesOffServesNoDrop(t *testing.T) {
	s := sideTaking(t, Text, Text|Files)
	expect(t, "a drop to it", s.CentralDropsFiles(7, report), sideStopsDrop(7))
	expect(t, "a drag of it is fetched", s.CentralWantsDraggedFiles(8), sideStopsDrop(8))
}

func TestSwitchingFilesOffStopsTheDropsOfASideMachine(t *testing.T) {
	t.Run("switched off here", func(t *testing.T) {
		s := sideSharingFiles(t)
		expect(t, "the central fetches a drag", s.CentralWantsDraggedFiles(5), sideSendsDrop(5))
		expect(t, "a drop starts", s.CentralDropsFiles(7, report), sideStartsDrop(7, report), sideReceivesDrop(7))
		expect(t, "the drop is taken", s.DropTaken(7), dropTaken(7))
		expect(t, "files are switched off", s.Switch(Text), toCentral{Do: Announce, Takes: Text}, sideCancelsDrop(7), sideStopsDrop(7), sideStopsDrop(5))
		expect(t, "the files arrive anyway", s.FilesArrived(7))
	})
	t.Run("switched off on the central", func(t *testing.T) {
		s := sideSharingFiles(t)
		expect(t, "a drop starts", s.CentralDropsFiles(7, report), sideStartsDrop(7, report), sideReceivesDrop(7))
		expect(t, "the central stops taking files", s.CentralSays(Text), sideCancelsDrop(7), sideStopsDrop(7))
	})
}

func TestASideMachineForgetsADragItSentOnceItsTransferEnds(t *testing.T) {
	s := sideSharingFiles(t)
	expect(t, "the central fetches a drag", s.CentralWantsDraggedFiles(5), sideSendsDrop(5))
	s.TransferEnded(5)
	expect(t, "files are switched off", s.Switch(Text), toCentral{Do: Announce, Takes: Text})
}

func TestASideMachineThatConnectsAgainCancelsItsDrops(t *testing.T) {
	s := sideSharingFiles(t)
	s.CentralDropsFiles(7, report)
	expect(t, "it connects again", s.Connected(), sideCancelsDrop(7), toCentral{Do: Announce, Takes: Text | Files})
	expect(t, "the old drop is taken late", s.DropTaken(7))
}

func TestATakenDropOnASideMachineCarriesOnWhenItsAnswerComesAfterFocusLeft(t *testing.T) {
	d := deskTakingDrops(t, "A", "B")
	dragFromAAcross(t, d, OnSide("B"))
	expect(t, "focus goes back to A before B says it took the drop", d.FocusOn("A"), cancelDropOn("B", 1))
	expect(t, "B says it took the drop", d.DropTaken(1))
	expectDrops(t, "after B took it", d, 1)
	expect(t, "files are switched off, which stops even a taken drop", d.Switch(Text), announce("A", Text), announce("B", Text), stopDrop(1))

	s := sideSharingFiles(t)
	s.CentralDropsFiles(1, report)
	expect(t, "the drop is taken on B", s.DropTaken(1), dropTaken(1))
	expect(t, "the late cancel reaches B", s.CentralCancelsDrop(1))
	expect(t, "focus leaves B", s.FocusLeaves(), toCentral{Do: Read})
	expect(t, "the files arrive", s.FilesArrived(1), sideFinishesDrop(1))
}

func TestACancelThatReachesASideMachineBeforeItsDropRefusesTheDrop(t *testing.T) {
	s := sideSharingFiles(t)
	expect(t, "the cancel comes first", s.CentralCancelsDrop(4))
	expect(t, "the drop comes after", s.CentralDropsFiles(4, report), sideStopsDrop(4))
	expect(t, "another drop", s.CentralDropsFiles(5, report), sideStartsDrop(5, report), sideReceivesDrop(5))
	for transfer := 100; transfer < 100+cancelsRemembered+5; transfer++ {
		s.CentralCancelsDrop(transfer)
	}
	if len(s.cancelled) != cancelsRemembered {
		t.Errorf("the side machine remembers %d cancels, want at most %d", len(s.cancelled), cancelsRemembered)
	}
}

func TestDropsWhoseFilesArrivedNoLongerNeedTheirSource(t *testing.T) {
	d := deskTakingDrops(t, "A", "B")
	dragFromAAcross(t, d, OnSide("B"))
	expect(t, "the files reach B", d.FilesArrived(1))
	expect(t, "A is lost", d.Lost("A"))
	expectDrops(t, "after A was lost", d, 1)
	expect(t, "the drop is taken on B", d.DropTaken(1))
	expectDrops(t, "after B took it", d)
}

func TestADragThatCannotCrossLeavesTheDropBeforeIt(t *testing.T) {
	d := deskTakingDrops(t, "A", "B")
	d.SaysDrops("C", false)
	expect(t, "C connects", d.Connected("C"), announce("C", Text|Files))
	d.Says("C", Text|Files)
	dragFromAAcross(t, d, OnSide("B"))
	actions, err := d.DragCrossed(OnSide("A"), OnSide("C"), report)
	if !errors.Is(err, ErrDropsNotTaken) || len(actions) != 0 {
		t.Fatalf("DragCrossed to C without the drag bit = %+v, %v, want no action and ErrDropsNotTaken", actions, err)
	}
	expectDrops(t, "after the refused crossing", d, 1)
	expect(t, "Escape still cancels the drop on B", d.DragCancelled(1), cancelDropOn("B", 1))
}

func TestADropRefusedAfterItsFilesArrivedNeedsNoStop(t *testing.T) {
	d := deskTakingDrops(t, "A")
	dragFromAAcross(t, d, central)
	expect(t, "the files arrive", d.FilesArrived(1), finishDrop(1))
	expect(t, "the drop is refused on the central", d.DropRefused(1))
	expectDrops(t, "after the refusal", d)

	s := sideSharingFiles(t)
	s.CentralDropsFiles(7, report)
	expect(t, "the files arrive", s.FilesArrived(7), sideFinishesDrop(7))
	expect(t, "the drop is refused here", s.DropRefused(7))
	expectSideDrops(t, "after the refusal", s)
}

func TestTheEndOfAPasteTransferIsNotTheEndOfADrop(t *testing.T) {
	d := deskSharingFiles(t, "A")
	expect(t, "focus to A", d.FocusOn("A"), read())
	expect(t, "the central read a file copy", readGaveFiles(t, d, report), deliver("A", 1))
	expect(t, "the paste transfer reported as a stopped drop", d.DropStopped(1))
	expect(t, "focus stays on A", d.FocusOn("A"))
	expect(t, "the paste transfer still runs and ends", d.FilesArrived(1))

	s := sideSharingFiles(t)
	expect(t, "the central sends files", s.CentralSendsFiles(3), receive(3))
	expect(t, "the paste transfer reported as a stopped drop", s.DropStopped(3))
	expect(t, "the paste transfer still runs and ends", s.FilesArrived(3), written(3))
}

func TestTheCentralTakesDropsOnlyWhenItSaysSo(t *testing.T) {
	d := deskSharingFiles(t, "A")
	d.SaysDrops("A", true)
	actions, err := d.DragCrossed(OnSide("A"), central, report)
	if !errors.Is(err, ErrDropsNotTaken) || len(actions) != 0 {
		t.Fatalf("DragCrossed to a central that takes no drops = %+v, %v, want no action and ErrDropsNotTaken", actions, err)
	}
	expect(t, "the central takes drops", d.CentralTakesDrops(true))
	expect(t, "the drag crosses to the central", crossed(t, d, OnSide("A"), central, report), fetchDrop("A", 1), startDrop(1, report))
	expect(t, "the central stops taking drops", d.CentralTakesDrops(false), stopDrop(1), cancelDrop(1))
}

func TestAPlaceOnTheCentralIgnoresItsSide(t *testing.T) {
	d := deskTakingDrops(t, "A")
	actions, err := d.DragCrossed(Place[string]{Side: "A"}, central, report)
	if !errors.Is(err, ErrDragBackToItsSource) || len(actions) != 0 {
		t.Fatalf("DragCrossed(central with a side, central) = %+v, %v, want no action and ErrDragBackToItsSource", actions, err)
	}
	d.FocusOn("A")
	d.ReadUnchanged()
	d.FocusOnCentral()
	expect(t, "the drag crosses to a central named oddly", crossed(t, d, OnSide("A"), Place[string]{Side: "junk"}, report), fetchDrop("A", 1), startDrop(1, report))
	expect(t, "the drag goes back to A", d.FocusOn("A"), read(), stopDrop(1), cancelDrop(1))
}

func TestTheActionsOfADropHaveNames(t *testing.T) {
	for do, name := range map[Do]string{StartDrop: "start drop", FinishDrop: "finish drop", CancelDrop: "cancel drop", DropTaken: "drop taken"} {
		if do.String() != name {
			t.Errorf("Do(%d).String() = %q, want %q", int(do), do.String(), name)
		}
	}
}
