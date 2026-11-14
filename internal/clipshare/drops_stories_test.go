package clipshare

import (
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

func expectTheFilesKept(t *testing.T, name, directory string) {
	t.Helper()
	time.Sleep(settling)
	if len(arrivalsLeftIn(t, directory)) == 0 {
		t.Errorf("%s removed the files of a drop that was taken", name)
	}
}

func TestAFileDraggedFromOneSideMachineIsDroppedWholeOnAnotherThroughTheCentral(t *testing.T) {
	d := newDropDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilTakingDrops(left, right)
	source := t.TempDir()
	report := writeFile(t, filepath.Join(source, "relatório.txt"), []byte("números\n"))
	photos := copiedFolder(t, filepath.Join(source, "photos"))

	d.dragCrosses(left, right, report, photos)
	session := waitForADrop(t, "right", right.drops)
	if want := []DroppedFile{{Name: "relatório.txt"}, {Name: "photos", Folder: true}}; !slices.Equal(session.files, want) {
		t.Errorf("the drop on the right started with %+v, want %+v", session.files, want)
	}
	session.take()

	expectSameFiles(t, waitUntilHanded(t, "right", session), right.arrivalDir, report, photos)
	d.expectNoTransfersLeft(left, right)
	d.expectNoDropsLeft(left, right)
	expectTheFilesKept(t, "right", right.arrivalDir)
	if left := arrivalsLeftIn(t, d.arrivalDir); len(left) > 0 {
		t.Errorf("the central kept %q of a drop that only passed through it", left)
	}
	expectNoFilesWritten(t, "right", right.clipboard)
	expectNoDropStarted(t, "the central", d.drops)
	expectNoDropStarted(t, "left", left.drops)
	if session.wasCancelled() {
		t.Error("the drop that was taken was cancelled")
	}
}

func TestAFileDraggedFromASideMachineIsDroppedOnTheCentral(t *testing.T) {
	d := newDropDesk(t, "left")
	left := d.sideJoins("left")
	d.waitUntilTakingDrops(left)
	notes := writeFile(t, filepath.Join(t.TempDir(), "notes.md"), randomContent(t, 300<<10))

	d.dragCrosses(left, onTheCentral, notes)
	session := waitForADrop(t, "the central", d.drops)
	session.take()

	expectSameFiles(t, waitUntilHanded(t, "the central", session), d.arrivalDir, notes)
	d.expectNoTransfersLeft(left)
	d.expectNoDropsLeft(left)
	expectTheFilesKept(t, "the central", d.arrivalDir)
	expectNoFilesWritten(t, "the central", d.clipboard)
}

func TestAFileDraggedOnTheCentralIsDroppedOnASideMachine(t *testing.T) {
	d := newDropDesk(t, "right")
	right := d.sideJoins("right")
	d.waitUntilTakingDrops(right)
	project := copiedFolder(t, filepath.Join(t.TempDir(), "project"))

	d.dragCrosses(onTheCentral, right, project)
	session := waitForADrop(t, "right", right.drops)
	session.take()

	expectSameFiles(t, waitUntilHanded(t, "right", session), right.arrivalDir, project)
	d.expectNoTransfersLeft(right)
	d.expectNoDropsLeft(right)
	expectTheFilesKept(t, "right", right.arrivalDir)
}

func TestADragBackToItsSourceOpensNoDrop(t *testing.T) {
	d := newDropDesk(t, "left")
	left := d.sideJoins("left")
	d.waitUntilTakingDrops(left)
	file := writeFile(t, filepath.Join(t.TempDir(), "back.txt"), []byte("back"))

	d.focusMovesTo(left)
	d.sideDrags(left, file)
	if err := d.crosses(left, left); !errors.Is(err, clipboard.ErrDragBackToItsSource) {
		t.Errorf("a drag back to the left = %v, want ErrDragBackToItsSource", err)
	}
	d.focusMovesTo(onTheCentral)
	d.centralDrags(file)
	if err := d.crosses(onTheCentral, onTheCentral); !errors.Is(err, clipboard.ErrDragBackToItsSource) {
		t.Errorf("a drag back to the central = %v, want ErrDragBackToItsSource", err)
	}

	time.Sleep(settling)
	d.expectNoTransfersLeft(left)
	expectNoDropStarted(t, "the central", d.drops)
	expectNoDropStarted(t, "left", left.drops)
}

func TestADropNotTakenWhenFocusLeavesStopsAndLeavesNothing(t *testing.T) {
	for _, target := range []string{"right", "the central"} {
		t.Run("on "+target, func(t *testing.T) {
			d := newSlowDropDesk(t, oneMebibytePerSecond, "left", "right")
			left, right := d.sideJoins("left"), d.sideJoins("right")
			d.waitUntilTakingDrops(left, right)
			to, arrivalDir, away := right, right.arrivalDir, onTheCentral
			if target == "the central" {
				to, arrivalDir, away = onTheCentral, d.arrivalDir, right
			}
			large := writeFile(t, filepath.Join(t.TempDir(), "video.mp4"), randomContent(t, 3<<20))

			d.dragCrosses(left, to, large)
			session := waitForADrop(t, target, dropsOn(to, d))
			waitUntilArriving(t, target, arrivalDir)
			d.focusMovesTo(away)

			waitUntilCancelled(t, target, session)
			expectNothingLeftIn(t, target, arrivalDir)
			d.expectNoTransfersLeft(left, right)
			d.expectNoDropsLeft(left, right)
			if handed := session.handedPaths(); len(handed) > 0 {
				t.Errorf("the cancelled drop got the files %q", handed)
			}
		})
	}
}

func TestADropTakenBeforeFocusLeavesCarriesOnAndFinishes(t *testing.T) {
	for _, target := range []string{"right", "the central"} {
		t.Run("on "+target, func(t *testing.T) {
			d := newSlowDropDesk(t, oneMebibytePerSecond, "left", "right")
			left, right := d.sideJoins("left"), d.sideJoins("right")
			d.waitUntilTakingDrops(left, right)
			to, arrivalDir, away := right, right.arrivalDir, onTheCentral
			if target == "the central" {
				to, arrivalDir, away = onTheCentral, d.arrivalDir, right
			}
			large := writeFile(t, filepath.Join(t.TempDir(), "backup.tar"), randomContent(t, 3<<20))

			d.dragCrosses(left, to, large)
			session := waitForADrop(t, target, dropsOn(to, d))
			waitUntilArriving(t, target, arrivalDir)
			session.take()
			d.waitUntilTakenOn(to)
			d.focusMovesTo(away)

			expectSameFiles(t, waitUntilHanded(t, target, session), arrivalDir, large)
			if session.wasCancelled() {
				t.Error("the drop that was taken was cancelled when focus left")
			}
			d.expectNoTransfersLeft(left, right)
			d.expectNoDropsLeft(left, right)
		})
	}
}

func TestADropTakenOnASideMachineWhileTheDragGoesBackStillFinishes(t *testing.T) {
	d := newSlowDropDesk(t, oneMebibytePerSecond, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilTakingDrops(left, right)
	large := writeFile(t, filepath.Join(t.TempDir(), "slides.key"), randomContent(t, 2<<20))
	d.dragCrosses(left, right, large)
	session := waitForADrop(t, "right", right.drops)

	held, release := make(chan struct{}), make(chan struct{})
	right.runtime.inbox.put(func() {
		close(held)
		<-release
	})
	<-held
	session.take()
	eventually(t, "the right queueing the drop it took", func() bool { return queuedOn(right.runtime) >= 1 })
	d.focusMovesTo(left)
	eventually(t, "the right queueing the leave and the cancel behind the drop it took", func() bool { return queuedOn(right.runtime) >= 3 })
	close(release)

	expectSameFiles(t, waitUntilHanded(t, "right", session), right.arrivalDir, large)
	if session.wasCancelled() {
		t.Error("the drop taken before the cancel reached the right was cancelled")
	}
	d.expectNoTransfersLeft(left, right)
	d.expectNoDropsLeft(left, right)
}

func everyDropTaken(runs map[int]*dropRun) bool {
	for _, run := range runs {
		if !run.taken {
			return false
		}
	}
	return true
}

func (d *deskUnderTest) waitUntilTakenOn(side *sideUnderTest) {
	d.t.Helper()
	if side == onTheCentral {
		eventually(d.t, "the central taking its drop", func() bool {
			return askCentral(d.t, d.runtime, func() bool { return everyDropTaken(d.runtime.drops) })
		})
		return
	}
	eventually(d.t, side.name+" taking its drop", func() bool {
		return askSide(d.t, side.runtime, func() bool { return everyDropTaken(side.runtime.drops) })
	})
}

func queuedOn(s *Side) int {
	s.inbox.mutex.Lock()
	defer s.inbox.mutex.Unlock()
	return len(s.inbox.queued)
}

func TestEscapeStopsADropNotTakenAndLeavesNothing(t *testing.T) {
	for _, target := range []string{"right", "the central"} {
		t.Run("on "+target, func(t *testing.T) {
			d := newSlowDropDesk(t, oneMebibytePerSecond, "left", "right")
			left, right := d.sideJoins("left"), d.sideJoins("right")
			d.waitUntilTakingDrops(left, right)
			to, arrivalDir := right, right.arrivalDir
			if target == "the central" {
				to, arrivalDir = onTheCentral, d.arrivalDir
			}
			large := writeFile(t, filepath.Join(t.TempDir(), "disk.img"), randomContent(t, 3<<20))

			d.dragCrosses(left, to, large)
			session := waitForADrop(t, target, dropsOn(to, d))
			waitUntilArriving(t, target, arrivalDir)
			d.runtime.DragCancelled()

			waitUntilCancelled(t, target, session)
			expectNothingLeftIn(t, target, arrivalDir)
			d.expectNoTransfersLeft(left, right)
			d.expectNoDropsLeft(left, right)
		})
	}
}

func TestEscapeAfterTheFilesArrivedOnASideMachineRemovesThem(t *testing.T) {
	d := newDropDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilTakingDrops(left, right)
	small := writeFile(t, filepath.Join(t.TempDir(), "small.txt"), []byte("small"))

	d.dragCrosses(left, right, small)
	session := waitForADrop(t, "right", right.drops)
	waitUntilHanded(t, "right", session)
	d.expectNoTransfersLeft(left, right)
	d.runtime.DragCancelled()

	waitUntilCancelled(t, "right", session)
	expectNothingLeftIn(t, "right", right.arrivalDir)
	d.expectNoDropsLeft(left, right)
}

func TestEscapeWhileTheDropStillTakesItsFilesCancelsAndRemovesThemOnlyAfterward(t *testing.T) {
	d := newDropDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilTakingDrops(left, right)
	released := make(chan struct{})
	var releasing sync.Once
	letArrivedReturn := func() { releasing.Do(func() { close(released) }) }
	t.Cleanup(letArrivedReturn)
	right.drops.holdArrivedUntil(released)
	small := writeFile(t, filepath.Join(t.TempDir(), "held.txt"), []byte("held"))

	d.dragCrosses(left, right, small)
	session := waitForADrop(t, "right", right.drops)
	waitUntilHanded(t, "right", session)
	d.expectNoTransfersLeft(left, right)
	d.runtime.DragCancelled()
	d.expectNoDropsLeft(left, right)
	time.Sleep(settling)
	if session.wasCancelled() {
		t.Error("the drop was cancelled while it still took its files")
	}
	if len(arrivalsLeftIn(t, right.arrivalDir)) == 0 {
		t.Error("the files were removed while the drop still took them")
	}
	letArrivedReturn()

	waitUntilCancelled(t, "right", session)
	expectNothingLeftIn(t, "right", right.arrivalDir)
	if calls := session.callsInOrder(); !slices.Equal(calls, []string{"Arrived", "Cancel"}) {
		t.Errorf("the drop saw %q, want Arrived to return before Cancel", calls)
	}
	if gone := session.pathsGoneBeforeArrivedReturned(); len(gone) > 0 {
		t.Errorf("%q were removed before Arrived returned", gone)
	}
}

func TestADropRefusedAfterItsFilesArrivedLeavesNothing(t *testing.T) {
	for _, target := range []string{"right", "the central"} {
		t.Run("on "+target, func(t *testing.T) {
			d := newDropDesk(t, "left", "right")
			left, right := d.sideJoins("left"), d.sideJoins("right")
			d.waitUntilTakingDrops(left, right)
			to, arrivalDir := right, right.arrivalDir
			if target == "the central" {
				to, arrivalDir = onTheCentral, d.arrivalDir
			}
			small := writeFile(t, filepath.Join(t.TempDir(), "refused.txt"), []byte("refused"))

			d.dragCrosses(left, to, small)
			session := waitForADrop(t, target, dropsOn(to, d))
			waitUntilHanded(t, target, session)
			session.endWithoutDrop()

			expectNothingLeftIn(t, target, arrivalDir)
			d.expectNoTransfersLeft(left, right)
			d.expectNoDropsLeft(left, right)
		})
	}
}

func TestSwitchingFilesOffStopsATakenDropAndLeavesNothing(t *testing.T) {
	for _, where := range []string{"on the machine it goes to", "on the central"} {
		t.Run(where, func(t *testing.T) {
			d := newSlowDropDesk(t, oneMebibytePerSecond, "left", "right")
			left, right := d.sideJoins("left"), d.sideJoins("right")
			d.waitUntilTakingDrops(left, right)
			large := writeFile(t, filepath.Join(t.TempDir(), "archive.zip"), randomContent(t, 3<<20))

			d.dragCrosses(left, right, large)
			session := waitForADrop(t, "right", right.drops)
			waitUntilArriving(t, "right", right.arrivalDir)
			session.take()
			d.waitUntilTakenOn(right)
			if where == "on the central" {
				d.filesOn.Store(false)
			} else {
				right.filesOn.Store(false)
			}
			d.focusMovesTo(onTheCentral)

			waitUntilCancelled(t, "right", session)
			expectNothingLeftIn(t, "right", right.arrivalDir)
			d.expectNoTransfersLeft(left, right)
			d.expectNoDropsLeft(left, right)
			if handed := session.handedPaths(); len(handed) > 0 {
				t.Errorf("the drop got the files %q after files were switched off", handed)
			}
		})
	}
}

func TestADragTowardASideMachineWithoutTheDragBitMovesNothing(t *testing.T) {
	d := newDropDesk(t, "left", "plain")
	left, plain := d.sides["left"], d.sides["plain"]
	plain.drops = nil
	d.sideJoins("left")
	d.sideJoins("plain")
	d.waitUntilTakingDrops(left)
	d.waitUntilSharingFiles(plain)
	file := writeFile(t, filepath.Join(t.TempDir(), "plan.pdf"), randomContent(t, 4<<10))

	d.focusMovesTo(left)
	d.sideDrags(left, file)
	d.focusMovesTo(plain)
	if err := d.crosses(left, plain); !errors.Is(err, clipboard.ErrDropsNotTaken) {
		t.Errorf("a drag toward a machine without the drag bit = %v, want ErrDropsNotTaken", err)
	}

	time.Sleep(settling)
	d.expectNoTransfersLeft(left, plain)
	expectNoDropStarted(t, "left", left.drops)
	expectNoDropStarted(t, "the central", d.drops)
	expectNothingLeftIn(t, "plain", plain.arrivalDir)
}

func (d *deskUnderTest) originReportsItsDragGone(origin *sideUnderTest) {
	d.t.Helper()
	if origin == onTheCentral {
		d.runtime.DragGone()
		askCentral(d.t, d.runtime, func() bool { return true })
		return
	}
	endsBefore := d.handled.dragEndsFrom(origin.name)
	origin.runtime.DragGone()
	eventually(d.t, "the central handling the end of the drag on "+origin.name, func() bool {
		return d.handled.dragEndsFrom(origin.name) > endsBefore
	})
	askCentral(d.t, d.runtime, func() bool { return true })
}

func expectNoDragEndTold(t *testing.T, d *deskUnderTest) {
	t.Helper()
	if _, ends := d.described.seen(); len(ends) > 0 {
		t.Errorf("the central told the end of the drags of %q, want none before the drag is over", ends)
	}
}

func TestADragThatDidNotCrossIsForgottenWhenItsOriginReportsItGone(t *testing.T) {
	d := newDropDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilTakingDrops(left, right)
	file := writeFile(t, filepath.Join(t.TempDir(), "gone.txt"), []byte("gone"))

	d.focusMovesTo(left)
	d.sideDrags(left, file)
	left.runtime.DragGone()
	eventually(t, "the central telling that the drag on the left ended", func() bool {
		_, ends := d.described.seen()
		return slices.Equal(ends, []string{"left"})
	})
	d.focusMovesTo(right)
	if err := d.crosses(left, right); !errors.Is(err, ErrNoFileDrag) {
		t.Errorf("a crossing after the drag on the left ended = %v, want ErrNoFileDrag", err)
	}

	d.focusMovesTo(onTheCentral)
	d.centralDrags(file)
	d.originReportsItsDragGone(onTheCentral)
	if err := d.crosses(onTheCentral, right); !errors.Is(err, ErrNoFileDrag) {
		t.Errorf("a crossing after the drag on the central ended = %v, want ErrNoFileDrag", err)
	}

	time.Sleep(settling)
	d.expectNoTransfersLeft(left, right)
	expectNoDropStarted(t, "right", right.drops)
}

func TestADragGoesOnToAThirdMachineAfterItsOriginReportedItGone(t *testing.T) {
	for _, origin := range []string{"left", "the central"} {
		t.Run("from "+origin, func(t *testing.T) {
			d := newDropDesk(t, "left", "right", "third")
			left, right, third := d.sideJoins("left"), d.sideJoins("right"), d.sideJoins("third")
			d.waitUntilTakingDrops(left, right, third)
			from := left
			if origin == "the central" {
				from = onTheCentral
			}
			report := writeFile(t, filepath.Join(t.TempDir(), "report.txt"), randomContent(t, 64<<10))

			d.dragCrosses(from, right, report)
			onTheRight := waitForADrop(t, "right", right.drops)
			d.originReportsItsDragGone(from)
			expectNoDragEndTold(t, d)
			d.focusMovesTo(third)
			if err := d.crosses(from, third); err != nil {
				t.Fatalf("the drag going on from the right to the third machine = %v, want a new drop", err)
			}

			waitUntilCancelled(t, "right", onTheRight)
			onTheThird := waitForADrop(t, "third", third.drops)
			onTheThird.take()
			expectSameFiles(t, waitUntilHanded(t, "third", onTheThird), third.arrivalDir, report)
			expectNothingLeftIn(t, "right", right.arrivalDir)
			d.expectNoTransfersLeft(left, right, third)
			d.expectNoDropsLeft(left, right, third)
			expectNoDragEndTold(t, d)
		})
	}
}

func TestFocusBackOnTheOriginCancelsTheDropOfADragReportedGoneAndDragCrossedStartsANewOne(t *testing.T) {
	d := newDropDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilTakingDrops(left, right)
	notes := writeFile(t, filepath.Join(t.TempDir(), "notes.md"), randomContent(t, 64<<10))

	d.dragCrosses(left, right, notes)
	first := waitForADrop(t, "right", right.drops)
	d.originReportsItsDragGone(left)
	d.focusMovesTo(left)
	if err := d.crosses(left, left); !errors.Is(err, clipboard.ErrDragBackToItsSource) {
		t.Errorf("the drag back to the left = %v, want ErrDragBackToItsSource", err)
	}
	waitUntilCancelled(t, "right", first)
	expectNothingLeftIn(t, "right", right.arrivalDir)

	d.focusMovesTo(right)
	if err := d.crosses(left, right); err != nil {
		t.Fatalf("the drag crossing to the right again = %v, want a new drop", err)
	}
	eventually(t, "a second drop starting on the right", func() bool { return len(right.drops.started()) == 2 })
	second := right.drops.started()[1]
	second.take()
	expectSameFiles(t, waitUntilHanded(t, "right", second), right.arrivalDir, notes)
	d.expectNoTransfersLeft(left, right)
	expectNoDragEndTold(t, d)
}

func TestDragOverForgetsADragThatCrossedAndTellsItsEnd(t *testing.T) {
	d := newDropDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilTakingDrops(left, right)
	file := writeFile(t, filepath.Join(t.TempDir(), "over.txt"), []byte("over"))

	d.dragCrosses(left, right, file)
	waitForADrop(t, "right", right.drops).take()
	d.originReportsItsDragGone(left)
	d.runtime.DragOver()
	eventually(t, "the central telling that the drag on the left ended", func() bool {
		_, ends := d.described.seen()
		return slices.Equal(ends, []string{"left"})
	})
	if err := d.crosses(left, right); !errors.Is(err, ErrNoFileDrag) {
		t.Errorf("a crossing after the drag was over = %v, want ErrNoFileDrag", err)
	}

	d.focusMovesTo(onTheCentral)
	d.centralDrags(file)
	d.focusMovesTo(right)
	if err := d.crosses(onTheCentral, right); err != nil {
		t.Fatalf("DragCrossed: %v", err)
	}
	d.runtime.DragOver()
	eventually(t, "the central forgetting its own drag that was over", func() bool { return !d.seesADragOn(onTheCentral) })
	if err := d.crosses(onTheCentral, right); !errors.Is(err, ErrNoFileDrag) {
		t.Errorf("a crossing after the drag of the central was over = %v, want ErrNoFileDrag", err)
	}
}

func TestADragThatCannotBeListedIsNotDescribedAndSaysWhy(t *testing.T) {
	d := newDropDesk(t, "left")
	left := d.sideJoins("left")
	d.waitUntilTakingDrops(left)

	d.focusMovesTo(left)
	left.runtime.DragSeen([]string{filepath.Join(t.TempDir(), "missing.txt")}, 0)
	left.log.waitFor(t, "The file drag on this machine cannot cross")
	time.Sleep(settling)
	if d.seesADragOn(left) {
		t.Error("the central took the description of a drag that could not be listed")
	}
}

func TestALostControlConnectionInTheMiddleOfADropClosesItsDataAndLeavesNothing(t *testing.T) {
	d := newSlowDropDesk(t, oneMebibytePerSecond, "left", "right")
	left, right := d.sides["left"], d.sides["right"]
	d.sideJoins("left")
	control := cuttableProxyTo(t, d.address)
	d.startSide(right, control.address, d.clipboardOpener(right.clipboard))
	d.waitUntilTakingDrops(left, right)
	large := writeFile(t, filepath.Join(t.TempDir(), "film.mov"), randomContent(t, 3<<20))

	d.dragCrosses(left, right, large)
	session := waitForADrop(t, "right", right.drops)
	waitUntilArriving(t, "right", right.arrivalDir)
	session.take()
	d.waitUntilTakenOn(right)
	control.cut()

	waitUntilCancelled(t, "right", session)
	expectNothingLeftIn(t, "right", right.arrivalDir)
	d.expectNoTransfersLeft(left, right)
	d.expectNoDropsLeft(left, right)
	if handed := session.handedPaths(); len(handed) > 0 {
		t.Errorf("the drop got the files %q after the control connection was lost", handed)
	}
}
