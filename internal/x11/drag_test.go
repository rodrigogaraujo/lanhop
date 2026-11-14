//go:build linux

package x11

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

var (
	arrivedPaths = []string{"/home/someone/.cache/lanhop/arrived/7/report one.pdf", "/home/someone/.cache/lanhop/arrived/7/ação"}
	arrivedList  = "file:///home/someone/.cache/lanhop/arrived/7/report%20one.pdf\r\nfile:///home/someone/.cache/lanhop/arrived/7/a%C3%A7%C3%A3o\r\n"
)

func checkMessages(t *testing.T, seen []string, want ...string) {
	t.Helper()
	compact := slices.Compact(slices.Clone(seen))
	if !slices.Equal(compact, want) {
		t.Errorf("the target saw %v, want %v", seen, want)
	}
}

func TestFilesThatArrivedBeforeTheReleaseAreDroppedWhereTheButtonIsReleased(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.dragIntoTheTarget(t)
	desk.input.release(t, 1)

	if err := waitForDrag(t, drag); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := desk.target.droppedList(t); got != arrivedList {
		t.Errorf("the target received %q, want %q", got, arrivedList)
	}
	if !wasAccepted(drag) {
		t.Error("Accepted was not closed for a drop the target took")
	}
	checkMessages(t, desk.target.messages(), "XdndEnter", "XdndPosition", "XdndDrop")
	if version, types := desk.target.entered(); version != xdndVersion || !slices.Equal(types, []uint32{desk.target.atoms["text/uri-list"]}) {
		t.Errorf("XdndEnter carried version %d and types %v, want version %d and text/uri-list only", version, types, xdndVersion)
	}
	if at := desk.target.lastPosition(); at != [2]int{200, 200} {
		t.Errorf("the last XdndPosition was at %v, want the release point 200,200", at)
	}
	if owner := dragSelectionOwner(t, desk.source); owner != atomNone {
		t.Errorf("XdndSelection is owned by 0x%x after the drop, want nobody", owner)
	}
}

func TestADropBeforeTheFilesArrivedWaitsForThemWithTheMouseFree(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	drag := desk.start(t)
	desk.input.press(t, 1)
	desk.dragIntoTheTarget(t)
	desk.input.release(t, 1)
	waitForAccepted(t, drag)

	if !pointerIsFree(t, desk.display) {
		t.Error("lanhop holds the pointer while the files travel")
	}
	desk.input.moveTo(t, 400, 400)
	time.Sleep(100 * time.Millisecond)
	if seen := desk.target.messages(); len(seen) != 0 {
		t.Errorf("before the files arrived the target saw %v, want nothing", seen)
	}
	select {
	case <-drag.Done():
		t.Fatalf("the drag ended before the files arrived: %v", drag.Wait())
	default:
	}

	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	if err := waitForDrag(t, drag); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := desk.target.droppedList(t); got != arrivedList {
		t.Errorf("the target received %q, want %q", got, arrivedList)
	}
	checkMessages(t, desk.target.messages(), "XdndEnter", "XdndPosition", "XdndDrop")
	if at := desk.target.lastPosition(); at != [2]int{200, 200} {
		t.Errorf("the drop went to %v, want the release point 200,200 although the cursor moved on", at)
	}
}

func TestADragStartedOnThisMachineWhileTheDropWaitsDoesNotSpoilIt(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	drag := desk.start(t)
	desk.input.press(t, 1)
	desk.dragIntoTheTarget(t)
	desk.input.release(t, 1)
	waitForAccepted(t, drag)

	takeXdndSelection(t, desk.display)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}

	if err := waitForDrag(t, drag); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := desk.target.droppedList(t); got != arrivedList {
		t.Errorf("the target received %q, want %q", got, arrivedList)
	}
}

func TestFilesThatArriveWhileTheCursorIsOverTheTargetEnterItThen(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	drag := desk.start(t)
	desk.input.press(t, 1)
	desk.dragIntoTheTarget(t)
	time.Sleep(100 * time.Millisecond)
	if seen := desk.target.messages(); len(seen) != 0 {
		t.Errorf("before the files arrived the target saw %v, want nothing", seen)
	}

	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	checkMessages(t, desk.target.messagesOnceItSaw("XdndPosition"), "XdndEnter", "XdndPosition")
	if wasAccepted(drag) {
		t.Error("Accepted was closed before the release")
	}
	desk.input.release(t, 1)

	if err := waitForDrag(t, drag); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := desk.target.droppedList(t); got != arrivedList {
		t.Errorf("the target received %q, want %q", got, arrivedList)
	}
}

func TestTheCursorMovedByRelativeInputUnderTheGrabCarriesTheDrag(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	for range 6 {
		desk.input.moveBy(t, 30, 30)
		time.Sleep(10 * time.Millisecond)
	}
	desk.input.release(t, 1)

	if err := waitForDrag(t, drag); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if at := desk.target.lastPosition(); at != [2]int{200, 200} {
		t.Errorf("the drop went to %v, want 200,200 after six moves of 30,30 from 20,20", at)
	}
}

func TestADropWhoseFilesNeverArriveEndsAfterTheLimitWithoutTouchingTheTarget(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	desk.source.limits.files = 300 * time.Millisecond
	drag := desk.start(t)
	desk.input.press(t, 1)
	desk.dragIntoTheTarget(t)
	released := time.Now()
	desk.input.release(t, 1)

	err := waitForDrag(t, drag)
	waited := time.Since(released)

	if !errors.Is(err, ErrFilesLate) {
		t.Errorf("Wait: error = %v, want %v", err, ErrFilesLate)
	}
	if limit := desk.source.limits.files; waited < limit || waited > limit+2*time.Second {
		t.Errorf("the drop was given up %v after the release, want about %v", waited, limit)
	}
	if seen := desk.target.messages(); len(seen) != 0 {
		t.Errorf("the target saw %v, want nothing", seen)
	}
	if err := drag.FilesArrived(arrivedPaths); !errors.Is(err, ErrDragEnded) {
		t.Errorf("FilesArrived after the end: error = %v, want %v", err, ErrDragEnded)
	}
}

func TestATargetThatDoesNotTakeACopyGetsXdndLeaveAndNoDrop(t *testing.T) {
	behaviours := map[string]struct {
		behaviour targetBehaviour
		named     string
	}{
		"it refuses":        {targetBehaviour{refuses: true}, ""},
		"it chooses a move": {targetBehaviour{action: "XdndActionMove"}, "XdndActionMove"},
	}
	for name, test := range behaviours {
		t.Run(name, func(t *testing.T) {
			desk := newDragDesk(t, test.behaviour)
			drag := desk.start(t)
			desk.input.press(t, 1)
			if err := drag.FilesArrived(arrivedPaths); err != nil {
				t.Fatalf("FilesArrived: %v", err)
			}
			desk.dragIntoTheTarget(t)
			desk.input.release(t, 1)

			err := waitForDrag(t, drag)

			if !errors.Is(err, ErrDropRefused) || !strings.Contains(err.Error(), test.named) {
				t.Errorf("Wait: error = %v, want %v naming %q", err, ErrDropRefused, test.named)
			}
			if wasAccepted(drag) {
				t.Error("Accepted was closed for a refused drop")
			}
			checkMessages(t, desk.target.messagesOnceItSaw("XdndLeave"), "XdndEnter", "XdndPosition", "XdndLeave")
		})
	}
}

func TestATargetThatNeverAnswersAPositionIsLeftAfterTheLimit(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{silent: true})
	desk.source.limits.status = 300 * time.Millisecond
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.dragIntoTheTarget(t)
	desk.input.release(t, 1)

	if err := waitForDrag(t, drag); !errors.Is(err, ErrTargetSilent) {
		t.Errorf("Wait: error = %v, want %v", err, ErrTargetSilent)
	}
	checkMessages(t, desk.target.messagesOnceItSaw("XdndLeave"), "XdndEnter", "XdndPosition", "XdndLeave")
}

func TestATargetThatTookTheDropAndNeverFinishesIsGivenUpAfterTheLimit(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{keepsTheDrop: true})
	desk.source.limits.finish = 300 * time.Millisecond
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.dragIntoTheTarget(t)
	released := time.Now()
	desk.input.release(t, 1)

	err := waitForDrag(t, drag)
	waited := time.Since(released)

	if !errors.Is(err, ErrNotFinished) {
		t.Errorf("Wait: error = %v, want %v", err, ErrNotFinished)
	}
	if limit := desk.source.limits.finish; waited < limit || waited > limit+2*time.Second {
		t.Errorf("the source gave up %v after the release, want about %v", waited, limit)
	}
	if got := desk.target.droppedList(t); got != arrivedList {
		t.Errorf("the target received %q, want %q", got, arrivedList)
	}
	checkMessages(t, desk.target.messages(), "XdndEnter", "XdndPosition", "XdndDrop")
}

func TestATargetThatFinishesWithoutTakingTheFilesIsReported(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{failsTheDrop: true})
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.dragIntoTheTarget(t)
	desk.input.release(t, 1)

	if err := waitForDrag(t, drag); !errors.Is(err, ErrDropFailed) {
		t.Errorf("Wait: error = %v, want %v", err, ErrDropFailed)
	}
}

func TestATargetOlderThanVersionFiveFinishesWithoutSayingWhetherItTookTheFiles(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{version: 3, failsTheDrop: true})
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.dragIntoTheTarget(t)
	desk.input.release(t, 1)

	if err := waitForDrag(t, drag); err != nil {
		t.Errorf("Wait: %v", err)
	}
	if version, _ := desk.target.entered(); version != 3 {
		t.Errorf("XdndEnter carried version %d, want 3", version)
	}
}

func TestADropThroughAProxyWindowGoesToTheProxy(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{behindAProxy: true})
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.dragIntoTheTarget(t)
	desk.input.release(t, 1)

	if err := waitForDrag(t, drag); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := desk.target.droppedList(t); got != arrivedList {
		t.Errorf("the proxy received %q, want %q", got, arrivedList)
	}
	checkMessages(t, desk.target.messages(), "XdndEnter", "XdndPosition", "XdndDrop")
}

func TestThePortalTypesAreRefusedAndTheAddressListIsGiven(t *testing.T) {
	asks := []string{"TARGETS", "application/vnd.portal.filetransfer", "application/vnd.portal.files", "text/uri-list"}
	desk := newDragDesk(t, targetBehaviour{asksOnPosition: asks})
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.dragIntoTheTarget(t)
	desk.input.release(t, 1)

	if err := waitForDrag(t, drag); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	var offered []byte
	for _, name := range []string{"TARGETS", "TIMESTAMP", "text/uri-list"} {
		offered = binary.LittleEndian.AppendUint32(offered, desk.target.atoms[name])
	}
	want := []conversion{
		{target: "TARGETS", data: string(offered)},
		{target: "application/vnd.portal.filetransfer", refused: true},
		{target: "application/vnd.portal.files", refused: true},
		{target: "text/uri-list", data: arrivedList},
	}
	if got := desk.target.conversions(); !slices.Equal(got, want) {
		t.Errorf("the target was answered %+v, want %+v", got, want)
	}
}

func TestAListLargerThanOnePropertyTravelsInParts(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	paths := make([]string, 1500)
	var list strings.Builder
	for i := range paths {
		paths[i] = fmt.Sprintf("/home/someone/.cache/lanhop/arrived/9/%s%04d.txt", strings.Repeat("a-long-name-", 6), i)
		list.WriteString("file://" + paths[i] + "\r\n")
	}
	if list.Len() <= largestChunk {
		t.Fatalf("the list has %d bytes, too few to need several parts", list.Len())
	}
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(paths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.dragIntoTheTarget(t)
	desk.input.release(t, 1)

	if err := waitForDrag(t, drag); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := desk.target.droppedList(t); got != list.String() {
		t.Errorf("the target received %d bytes, want the %d bytes of the list", len(got), list.Len())
	}
}

func TestReleasingOverNoWindowThatTakesDropsEndsWithoutADrop(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.moveAlong(t, [2]int{40, 40}, [2]int{60, 60})
	desk.input.release(t, 1)

	if err := waitForDrag(t, drag); !errors.Is(err, ErrNoDropTarget) {
		t.Errorf("Wait: error = %v, want %v", err, ErrNoDropTarget)
	}
	if seen := desk.target.messages(); len(seen) != 0 {
		t.Errorf("the target saw %v, want nothing", seen)
	}
}

func TestLeavingTheTargetBeforeTheReleaseSendsXdndLeave(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.dragIntoTheTarget(t)
	desk.moveAlong(t, [2]int{350, 350}, [2]int{400, 400})
	desk.input.release(t, 1)

	if err := waitForDrag(t, drag); !errors.Is(err, ErrNoDropTarget) {
		t.Errorf("Wait: error = %v, want %v", err, ErrNoDropTarget)
	}
	checkMessages(t, desk.target.messages(), "XdndEnter", "XdndPosition", "XdndLeave")
}

func TestCancelEndsTheDragWithoutADropAndFreesThePointerAtOnce(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.dragIntoTheTarget(t)
	desk.target.messagesOnceItSaw("XdndPosition")

	drag.Cancel()

	if !pointerIsFree(t, desk.display) {
		t.Error("lanhop still holds the pointer when Cancel returns")
	}
	desk.input.release(t, 1)
	if err := waitForDrag(t, drag); !errors.Is(err, ErrDragCancelled) {
		t.Errorf("Wait: error = %v, want %v", err, ErrDragCancelled)
	}
	checkMessages(t, desk.target.messagesOnceItSaw("XdndLeave"), "XdndEnter", "XdndPosition", "XdndLeave")
}

func TestCancelWhileTheDropWaitsForTheFilesTouchesNothing(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	drag := desk.start(t)
	desk.input.press(t, 1)
	desk.dragIntoTheTarget(t)
	desk.input.release(t, 1)
	waitForAccepted(t, drag)

	drag.Cancel()

	if err := waitForDrag(t, drag); !errors.Is(err, ErrDragCancelled) {
		t.Errorf("Wait: error = %v, want %v", err, ErrDragCancelled)
	}
	if err := drag.FilesArrived(arrivedPaths); !errors.Is(err, ErrDragEnded) {
		t.Errorf("FilesArrived after Cancel: error = %v, want %v", err, ErrDragEnded)
	}
	if seen := desk.target.messages(); len(seen) != 0 {
		t.Errorf("the target saw %v, want nothing", seen)
	}
}

func TestAButtonHeldLongerThanADragMayLastLetsThePointerGo(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	desk.source.limits.hold = time.Second
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.dragIntoTheTarget(t)

	if err := waitForDrag(t, drag); !errors.Is(err, ErrDragTooLong) {
		t.Errorf("Wait: error = %v, want %v", err, ErrDragTooLong)
	}
	checkMessages(t, desk.target.messagesOnceItSaw("XdndLeave"), "XdndEnter", "XdndPosition", "XdndLeave")
	desk.input.release(t, 1)
}

func TestTheWheelAndOtherButtonsDoNotDrop(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	desk.input.moveTo(t, 150, 150)
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	for _, button := range []byte{4, 5, 3, 2} {
		desk.input.press(t, button)
		desk.input.release(t, button)
	}

	time.Sleep(200 * time.Millisecond)
	select {
	case <-drag.Done():
		t.Fatalf("another button ended the drag: %v", drag.Wait())
	default:
	}
	if seen := desk.target.messages(); slices.Contains(seen, "XdndDrop") {
		t.Errorf("after the other buttons the target saw %v, want no XdndDrop", seen)
	}
	desk.input.release(t, 1)
	if err := waitForDrag(t, drag); err != nil {
		t.Errorf("Wait: %v", err)
	}
}

func TestAnotherProgramTakingXdndSelectionEndsTheDrag(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.dragIntoTheTarget(t)
	desk.target.messagesOnceItSaw("XdndPosition")

	taker := takeXdndSelection(t, desk.display)

	if err := waitForDrag(t, drag); !errors.Is(err, ErrDragTaken) {
		t.Errorf("Wait: error = %v, want %v", err, ErrDragTaken)
	}
	checkMessages(t, desk.target.messagesOnceItSaw("XdndLeave"), "XdndEnter", "XdndPosition", "XdndLeave")
	if owner := dragSelectionOwner(t, desk.source); owner != taker {
		t.Errorf("after the drag XdndSelection is owned by 0x%x, want the program that took it, 0x%x", owner, taker)
	}
	desk.input.release(t, 1)
}

func TestAPointerHeldByAnotherProgramStopsTheDragAfterRetrying(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	holder := dialForTest(t, desk.display, func([]byte) {})
	if status := grabFrom(t, holder); status != grabSuccess {
		t.Fatalf("the other program could not grab the pointer: status %d", status)
	}
	desk.source.limits.grab = 200 * time.Millisecond
	started := time.Now()

	drag, err := desk.source.Start()

	if !errors.Is(err, ErrNotGrabbed) || drag != nil {
		t.Fatalf("Start = %v, %v, want no drag and %v", drag, err, ErrNotGrabbed)
	}
	if waited := time.Since(started); waited < desk.source.limits.grab {
		t.Errorf("Start gave up after %v, want at least %v of retries", waited, desk.source.limits.grab)
	}
	if owner := dragSelectionOwner(t, desk.source); owner != atomNone {
		t.Errorf("XdndSelection is owned by 0x%x, want nobody", owner)
	}
	ungrabFrom(t, holder)
	desk.start(t).Cancel()
}

func TestOnlyOneDragRunsAtATime(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	first := desk.start(t)

	if second, err := desk.source.Start(); !errors.Is(err, ErrDragUnderWay) || second != nil {
		t.Errorf("a second Start = %v, %v, want no drag and %v", second, err, ErrDragUnderWay)
	}
	first.Cancel()
	if err := waitForDrag(t, first); !errors.Is(err, ErrDragCancelled) {
		t.Errorf("Wait: error = %v, want %v", err, ErrDragCancelled)
	}
	desk.start(t).Cancel()
}

func TestFilesGivenByARelativePathANULByteOrNothingAreRefusedAndTheDragGoesOn(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	drag := desk.start(t)
	refused := map[string]struct {
		paths []string
		want  error
	}{
		"a relative path": {[]string{"/home/someone/a.txt", "relative/b.txt"}, ErrNotAbsolute},
		"a NUL byte":      {[]string{"/home/someone/nul\x00byte.txt"}, ErrNULInPath},
		"no files":        {nil, clipboard.ErrEmpty},
	}
	for name, test := range refused {
		if err := drag.FilesArrived(test.paths); !errors.Is(err, test.want) {
			t.Errorf("FilesArrived with %s: error = %v, want %v", name, err, test.want)
		}
	}

	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Errorf("FilesArrived after the refusals: %v", err)
	}
	if err := drag.FilesArrived(arrivedPaths); !errors.Is(err, ErrFilesAlreadyArrived) {
		t.Errorf("FilesArrived twice: error = %v, want %v", err, ErrFilesAlreadyArrived)
	}
	select {
	case <-drag.Done():
		t.Errorf("the drag ended: %v", drag.Wait())
	default:
	}
}

func TestLosingTheXServerEndsTheDrag(t *testing.T) {
	display, stop := startXvfbThatCanStop(t)
	source := openDragsOn(t, display)
	drag, err := source.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	stop()

	if err := waitForDrag(t, drag); !errors.Is(err, ErrClosed) {
		t.Errorf("Wait: error = %v, want %v", err, ErrClosed)
	}
}

func TestAHeldDropIsGivenUpWhenItsWindowChangedBeforeTheFilesArrived(t *testing.T) {
	changes := map[string]struct {
		change func(t *testing.T, desk *dragDesk) []*xdndTarget
		named  string
	}{
		"it moved, and another window took its place": {func(t *testing.T, desk *dragDesk) []*xdndTarget {
			desk.target.configure(t, configureX|configureY, 400, 400)
			return []*xdndTarget{startXdndTarget(t, desk.display, targetBehaviour{})}
		}, "it moved or changed size"},
		"it changed size": {func(t *testing.T, desk *dragDesk) []*xdndTarget {
			desk.target.configure(t, configureWidth|configureHeight, 150, 150)
			return nil
		}, "it moved or changed size"},
		"it was closed": {func(t *testing.T, desk *dragDesk) []*xdndTarget {
			desk.target.close(t)
			return nil
		}, "it was closed"},
		"another window covers it": {func(t *testing.T, desk *dragDesk) []*xdndTarget {
			return []*xdndTarget{startXdndTarget(t, desk.display, targetBehaviour{})}
		}, "it is no longer the window under the release point"},
	}
	for name, test := range changes {
		t.Run(name, func(t *testing.T) {
			desk := newDragDesk(t, targetBehaviour{})
			drag := desk.start(t)
			desk.input.press(t, 1)
			desk.dragIntoTheTarget(t)
			desk.input.release(t, 1)
			waitForAccepted(t, drag)
			others := test.change(t, desk)

			if err := drag.FilesArrived(arrivedPaths); err != nil {
				t.Fatalf("FilesArrived: %v", err)
			}
			err := waitForDrag(t, drag)

			if !errors.Is(err, ErrDropTargetChanged) || !strings.Contains(err.Error(), test.named) {
				t.Errorf("Wait: error = %v, want %v saying %q", err, ErrDropTargetChanged, test.named)
			}
			if seen := desk.target.messages(); len(seen) != 0 {
				t.Errorf("the window released over saw %v, want nothing", seen)
			}
			for _, other := range others {
				if seen := other.messages(); len(seen) != 0 {
					t.Errorf("the window now under the release point saw %v, want nothing", seen)
				}
			}
		})
	}
}

func TestAHeldDropWaitsForAButtonHeldOnThisMachineBeforeTakingXdndSelection(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	drag := desk.start(t)
	desk.input.press(t, 1)
	desk.dragIntoTheTarget(t)
	desk.input.release(t, 1)
	waitForAccepted(t, drag)
	localDrag := takeXdndSelection(t, desk.display)
	desk.input.press(t, 1)

	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if owner := dragSelectionOwner(t, desk.source); owner != localDrag {
		t.Errorf("while its button was down, the drag on this machine lost XdndSelection to 0x%x", owner)
	}
	if seen := desk.target.messages(); len(seen) != 0 {
		t.Errorf("while a button was down here the target saw %v, want nothing", seen)
	}
	select {
	case <-drag.Done():
		t.Fatalf("the drag ended while a button was down here: %v", drag.Wait())
	default:
	}

	desk.input.release(t, 1)
	if err := waitForDrag(t, drag); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := desk.target.droppedList(t); got != arrivedList {
		t.Errorf("the target received %q, want %q", got, arrivedList)
	}
	checkMessages(t, desk.target.messages(), "XdndEnter", "XdndPosition", "XdndDrop")
}

func TestAHeldDropIsGivenUpWhenAButtonStaysDownOnThisMachine(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	desk.source.limits.buttonsUp = 300 * time.Millisecond
	drag := desk.start(t)
	desk.input.press(t, 1)
	desk.dragIntoTheTarget(t)
	desk.input.release(t, 1)
	waitForAccepted(t, drag)
	localDrag := takeXdndSelection(t, desk.display)
	desk.input.press(t, 1)

	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	err := waitForDrag(t, drag)

	if !errors.Is(err, ErrButtonHeldHere) {
		t.Errorf("Wait: error = %v, want %v", err, ErrButtonHeldHere)
	}
	if owner := dragSelectionOwner(t, desk.source); owner != localDrag {
		t.Errorf("the drag on this machine lost XdndSelection to 0x%x", owner)
	}
	if seen := desk.target.messages(); len(seen) != 0 {
		t.Errorf("the target saw %v, want nothing", seen)
	}
	desk.input.release(t, 1)
}

func TestFilesArrivingWithTheReleaseAreDroppedOnlyIfTheTargetAgrees(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{refuses: true})
	for round := range 5 {
		drag := desk.start(t)
		desk.input.press(t, 1)
		desk.dragIntoTheTarget(t)
		if err := drag.FilesArrived(arrivedPaths); err != nil {
			t.Fatalf("round %d: FilesArrived: %v", round, err)
		}
		desk.input.release(t, 1)

		if err := waitForDrag(t, drag); !errors.Is(err, ErrDropRefused) {
			t.Errorf("round %d: Wait: error = %v, want %v", round, err, ErrDropRefused)
		}
		if wasAccepted(drag) {
			t.Errorf("round %d: Accepted was closed for a drop the target refused", round)
		}
		desk.input.moveTo(t, 20, 20)
	}
}

func TestOnlyOnePositionWaitsForItsStatusAndLaterMovesAreMerged(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{silent: true})
	desk.source.limits.status = 10 * time.Second
	desk.input.moveTo(t, 150, 150)
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.target.messagesOnceItSaw("XdndPosition")
	for i := range 10 {
		desk.input.moveTo(t, 160+3*i, 160+3*i)
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if n := desk.target.count("XdndPosition"); n != 1 {
		t.Errorf("before any XdndStatus the source sent %d positions, want 1", n)
	}

	desk.target.answerWithCopy()
	time.Sleep(300 * time.Millisecond)

	if n := desk.target.count("XdndPosition"); n != 2 {
		t.Errorf("after one XdndStatus the source had sent %d positions, want 2", n)
	}
	if at := desk.target.lastPosition(); at != [2]int{187, 187} {
		t.Errorf("the merged position was at %v, want the latest, 187,187", at)
	}
	drag.Cancel()
	desk.input.release(t, 1)
}

func TestARequestTimedBeforeLanhopTookXdndSelectionIsRefused(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{silent: true})
	desk.source.limits.status = 10 * time.Second
	drag := desk.start(t)
	desk.input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.dragIntoTheTarget(t)
	desk.target.messagesOnceItSaw("XdndPosition")

	if _, refused := askXdndSelection(t, desk.display, "text/uri-list", 1); !refused {
		t.Error("a request timed 1 ms after the X server started was answered")
	}
	if got, refused := askXdndSelection(t, desk.display, "text/uri-list", currentTime); refused || got != arrivedList {
		t.Errorf("a request at CurrentTime gave %q, refused %v, want %q", got, refused, arrivedList)
	}
	drag.Cancel()
	desk.input.release(t, 1)
}

func TestTIMESTAMPGivesTheTimeLanhopTookXdndSelection(t *testing.T) {
	desk := newDragDesk(t, targetBehaviour{})
	drag := desk.start(t)
	desk.input.press(t, 1)
	desk.dragIntoTheTarget(t)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	desk.target.messagesOnceItSaw("XdndPosition")

	got, refused := askXdndSelection(t, desk.display, "TIMESTAMP", currentTime)

	times := desk.target.positionTimes()
	if refused || len(times) == 0 || got != string(binary.LittleEndian.AppendUint32(nil, times[0])) {
		t.Errorf("TIMESTAMP gave %x, refused %v, want the time of the first XdndPosition, sent as lanhop took XdndSelection (%v)", got, refused, times)
	}
	desk.input.release(t, 1)
	if err := waitForDrag(t, drag); err != nil {
		t.Errorf("Wait: %v", err)
	}
}
