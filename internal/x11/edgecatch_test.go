//go:build linux

package x11

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

const (
	opGrabServer   = 36
	opUngrabServer = 37
	badWindow      = 3
	inTheStripX    = 280
	inTheStripY    = 150
)

var edgeStrip = Rect{X: 260, Y: 0, Width: 40, Height: 600}

func catchOn(t *testing.T, drags *Drags) *EdgeCatch {
	t.Helper()
	catch, err := drags.CatchAtEdge(edgeStrip)
	if err != nil {
		t.Fatalf("CatchAtEdge: %v", err)
	}
	t.Cleanup(func() { catch.Close() })
	return catch
}

type windowState struct {
	exists           bool
	class            uint16
	viewable         bool
	overrideRedirect bool
}

func stateOf(t *testing.T, c *conn, window uint32) windowState {
	t.Helper()
	reply, err := c.call(newRequest(opGetWindowAttributes, 0).u32(window).done())
	var failed RequestError
	if errors.As(err, &failed) && failed.Code == badWindow {
		return windowState{}
	}
	if err != nil {
		t.Fatalf("GetWindowAttributes 0x%x: %v", window, err)
	}
	return windowState{
		exists:           true,
		class:            binary.LittleEndian.Uint16(reply[12:14]),
		viewable:         reply[26] == mapStateViewable,
		overrideRedirect: reply[27] == 1,
	}
}

func topWindowAt(t *testing.T, drags *Drags, x, y int) uint32 {
	t.Helper()
	top, err := drags.childAt(drags.c.root, x, y)
	if err != nil {
		t.Fatalf("the window at %d,%d: %v", x, y, err)
	}
	return top
}

func nextDrop(t *testing.T, catch *EdgeCatch) (EdgeDrop, bool) {
	t.Helper()
	select {
	case drop, delivered := <-catch.Dropped():
		return drop, delivered
	case <-time.After(10 * time.Second):
		t.Fatal("the edge neither reported a drop nor ended")
		return EdgeDrop{}, false
	}
}

func waitForEdgeAccepted(t *testing.T, catch *EdgeCatch) {
	t.Helper()
	select {
	case <-catch.Accepted():
	case <-time.After(5 * time.Second):
		t.Fatal("the edge did not accept the drag")
	}
}

func edgeAccepted(catch *EdgeCatch) bool {
	select {
	case <-catch.Accepted():
		return true
	default:
		return false
	}
}

func checkGone(t *testing.T, c *conn, catch *EdgeCatch) {
	t.Helper()
	if stateOf(t, c, catch.window).exists {
		t.Error("the window at the edge still exists")
	}
	select {
	case drop, delivered := <-catch.Dropped():
		if delivered {
			t.Errorf("the edge delivered %+v after its end, want Dropped closed", drop)
		}
	case <-time.After(5 * time.Second):
		t.Error("Dropped was not closed")
	}
}

func fileToDrag(t *testing.T) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "report for the neighbour.txt")
	content := "the content that must stay where it is"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path, content
}

func checkUntouched(t *testing.T, path, content string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != content {
		t.Errorf("the dragged file now reads %q, %v, want it untouched with %q", got, err, content)
	}
}

func enterTheEdge(t *testing.T, drags *Drags, source *xdndSource, catch *EdgeCatch) uint32 {
	t.Helper()
	found, err := drags.dropTargetAt(inTheStripX, inTheStripY)
	if err != nil {
		t.Fatalf("the drop target at %d,%d: %v", inTheStripX, inTheStripY, err)
	}
	if want := (dragTarget{window: catch.window, destination: catch.window, version: xdndVersion}); found != want {
		t.Fatalf("an XDND source finds %+v under %d,%d, want the edge window %+v", found, inTheStripX, inTheStripY, want)
	}
	source.own(t)
	source.enter(found.window)
	return found.window
}

func TestTheEdgeWindowIsAnInputOnlyOverrideRedirectDropTargetKeptAboveTheOthers(t *testing.T) {
	display := startXvfb(t)
	drags := openDragsOn(t, display)
	drags.limits.raise = 50 * time.Millisecond
	catch := catchOn(t, drags)

	if state := stateOf(t, drags.c, catch.window); !state.exists || state.class != classInputOnly || !state.viewable || !state.overrideRedirect {
		t.Errorf("the edge window is %+v, want an existing, viewable, InputOnly, override-redirect window", state)
	}
	area, err := drags.areaOf(catch.window)
	if want := (windowArea{x: edgeStrip.X, y: edgeStrip.Y, width: edgeStrip.Width, height: edgeStrip.Height}); err != nil || area != want {
		t.Errorf("the edge window covers %+v, %v, want the strip %+v", area, err, want)
	}
	if version, aware := drags.windowProperty(catch.window, drags.atoms.aware, atomAtom); !aware || version != xdndVersion {
		t.Errorf("XdndAware of the edge window = %d, %v, want version %d", version, aware, xdndVersion)
	}
	if top := topWindowAt(t, drags, inTheStripX, inTheStripY); top != catch.window {
		t.Errorf("the window on top in the strip is 0x%x, want the edge window 0x%x", top, catch.window)
	}

	mappedLater := startXdndTarget(t, display, targetBehaviour{})
	deadline := time.Now().Add(3 * time.Second)
	for topWindowAt(t, drags, inTheStripX, inTheStripY) != catch.window {
		if time.Now().After(deadline) {
			t.Fatalf("the window 0x%x mapped later stays above the edge window", mappedLater.window)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTheEdgeTakesAWholeDropWithCopyAndLeavesTheFilesInPlace(t *testing.T) {
	display := startXvfb(t)
	drags := openDragsOn(t, display)
	catch := catchOn(t, drags)
	paths := filesWithAwkwardNames(t)
	source := startXdndSource(t, display, filesOffer(paths...))
	source.deletesOnMove(paths[0])

	target := enterTheEdge(t, drags, source, catch)
	source.position(target, inTheStripX, inTheStripY, source.serverTime(t), "XdndActionCopy")
	if status := source.status(t, target); !status.accepted || status.action != "XdndActionCopy" {
		t.Errorf("XdndStatus = %+v, want an accepted XdndActionCopy", status)
	}
	waitForEdgeAccepted(t, catch)
	dropped := source.serverTime(t)
	source.drop(target, dropped)

	if finished := source.finished(t, target); !finished.accepted || finished.action != "XdndActionCopy" {
		t.Errorf("XdndFinished = %+v, want an accepted XdndActionCopy", finished)
	}
	drop, delivered := nextDrop(t, catch)
	if !delivered || drop.Err != nil || !slices.Equal(drop.Paths, paths) {
		t.Errorf("Dropped gave %+v, %v, want the paths %q", drop, delivered, paths)
	}
	if asked := source.wasAsked(t, "text/uri-list"); asked.time != dropped {
		t.Errorf("text/uri-list was asked for at time %d, want the time of the drop %d", asked.time, dropped)
	}
	for _, path := range paths {
		if info, err := os.Stat(path); err != nil || (!info.IsDir() && info.Size() != int64(len("content"))) {
			t.Errorf("%q after the drop: %v, %v, want it untouched", path, info, err)
		}
	}
	checkGone(t, source.c, catch)
}

func TestASourceAskingForAnotherActionThanCopyGetsCopyAndKeepsItsFile(t *testing.T) {
	for _, action := range []string{"XdndActionMove", "XdndActionLink", "XdndActionAsk"} {
		t.Run(action, func(t *testing.T) {
			display := startXvfb(t)
			drags := openDragsOn(t, display)
			catch := catchOn(t, drags)
			path, content := fileToDrag(t)
			source := startXdndSource(t, display, filesOffer(path))
			source.deletesOnMove(path)

			target := enterTheEdge(t, drags, source, catch)
			source.position(target, inTheStripX, inTheStripY, source.serverTime(t), action)
			if status := source.status(t, target); !status.accepted || status.action != "XdndActionCopy" {
				t.Errorf("XdndStatus = %+v, want an accepted XdndActionCopy", status)
			}
			source.drop(target, source.serverTime(t))

			if finished := source.finished(t, target); !finished.accepted || finished.action != "XdndActionCopy" {
				t.Errorf("XdndFinished = %+v, want an accepted XdndActionCopy", finished)
			}
			if drop, delivered := nextDrop(t, catch); !delivered || drop.Err != nil || !slices.Equal(drop.Paths, []string{path}) {
				t.Errorf("Dropped gave %+v, %v, want %q", drop, delivered, path)
			}
			checkUntouched(t, path, content)
		})
	}
}

func TestTheEdgeRefusesADragThatOffersNoAddressList(t *testing.T) {
	display := startXvfb(t)
	drags := openDragsOn(t, display)
	catch := catchOn(t, drags)
	source := startXdndSource(t, display, sourceOffer{types: []string{"UTF8_STRING", "text/plain;charset=utf-8"}})

	target := enterTheEdge(t, drags, source, catch)
	source.position(target, inTheStripX, inTheStripY, source.serverTime(t), "XdndActionCopy")

	if status := source.status(t, target); status.accepted || status.action != "" {
		t.Errorf("XdndStatus = %+v, want a refusal with no action", status)
	}
	if edgeAccepted(catch) {
		t.Error("Accepted was closed for a drag the edge refused")
	}
}

func TestADropWhoseListIsNotOnlyLocalFilesIsFinishedAsNotTaken(t *testing.T) {
	display := startXvfb(t)
	drags := openDragsOn(t, display)
	catch := catchOn(t, drags)
	source := startXdndSource(t, display, sourceOffer{types: nautilusTypes, list: listOf("/home/someone/a.txt") + "https://example.com/b.txt\r\n"})

	target := enterTheEdge(t, drags, source, catch)
	source.position(target, inTheStripX, inTheStripY, source.serverTime(t), "XdndActionCopy")
	source.status(t, target)
	source.drop(target, source.serverTime(t))

	if finished := source.finished(t, target); finished.accepted || finished.action != "" {
		t.Errorf("XdndFinished = %+v, want a drop not taken, with no action", finished)
	}
	if drop, delivered := nextDrop(t, catch); !delivered || !errors.Is(drop.Err, clipboard.ErrNotFiles) || drop.Paths != nil {
		t.Errorf("Dropped gave %+v, %v, want %v and no paths", drop, delivered, clipboard.ErrNotFiles)
	}
	checkGone(t, source.c, catch)
}

func TestTheEdgeIgnoresASourceThatNeverEnteredIt(t *testing.T) {
	display := startXvfb(t)
	catch := catchOn(t, openDragsOn(t, display))
	stranger := startXdndSource(t, display, filesOffer(pathsDraggedAfter...))
	stranger.own(t)

	stranger.position(catch.window, inTheStripX, inTheStripY, stranger.serverTime(t), "XdndActionCopy")
	stranger.drop(catch.window, stranger.serverTime(t))

	if reply, answered := stranger.replyFrom(catch.window, "XdndStatus", 300*time.Millisecond); answered {
		t.Errorf("a source that never entered got XdndStatus %+v", reply)
	}
	if reply, answered := stranger.replyFrom(catch.window, "XdndFinished", 100*time.Millisecond); answered {
		t.Errorf("a source that never entered got XdndFinished %+v", reply)
	}
	select {
	case drop := <-catch.Dropped():
		t.Errorf("the edge ended with %+v", drop)
	default:
	}
}

func TestTheEdgeTakesADropFromAPointerDrivenSourceReleasedOverIt(t *testing.T) {
	display := startXvfb(t)
	catch := catchOn(t, openDragsOn(t, display))
	source := openDragsOn(t, display)
	input := startFakeInput(t, display)
	input.moveTo(t, 20, 20)
	drag, err := source.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(drag.Cancel)

	input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	for _, at := range [][2]int{{100, 60}, {200, 100}, {255, 140}, {inTheStripX, inTheStripY}} {
		input.moveTo(t, at[0], at[1])
		time.Sleep(10 * time.Millisecond)
	}
	waitForEdgeAccepted(t, catch)
	input.release(t, 1)

	if err := waitForDrag(t, drag); err != nil {
		t.Errorf("the source drag ended with %v, want a drop the edge took", err)
	}
	if drop, delivered := nextDrop(t, catch); !delivered || drop.Err != nil || !slices.Equal(drop.Paths, arrivedPaths) {
		t.Errorf("Dropped gave %+v, %v, want %q", drop, delivered, arrivedPaths)
	}
}

func TestTheEdgeTakesAListLongerThanOneProperty(t *testing.T) {
	display := startXvfb(t)
	drags := openDragsOn(t, display)
	catch := catchOn(t, drags)
	paths := make([]string, 1500)
	for i := range paths {
		paths[i] = fmt.Sprintf("/home/someone/%s%04d.txt", strings.Repeat("a-long-name-", 6), i)
	}
	source := startXdndSource(t, display, filesOffer(paths...))

	target := enterTheEdge(t, drags, source, catch)
	source.position(target, inTheStripX, inTheStripY, source.serverTime(t), "XdndActionCopy")
	source.status(t, target)
	source.drop(target, source.serverTime(t))

	if finished := source.finished(t, target); !finished.accepted {
		t.Errorf("XdndFinished = %+v, want an accepted drop", finished)
	}
	if drop, delivered := nextDrop(t, catch); !delivered || drop.Err != nil || !slices.Equal(drop.Paths, paths) {
		t.Errorf("Dropped gave %d paths and %v, want the %d paths", len(drop.Paths), drop.Err, len(paths))
	}
}

func TestTheEdgeWindowIsGoneOnceClosed(t *testing.T) {
	display := startXvfb(t)
	catch := catchOn(t, openDragsOn(t, display))
	observer := dialForTest(t, display, func([]byte) {})
	if !stateOf(t, observer, catch.window).viewable {
		t.Fatal("the edge window is not viewable before Close")
	}

	if err := catch.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	checkGone(t, observer, catch)
	if err := catch.Close(); err != nil {
		t.Errorf("a second Close: %v", err)
	}
}

func TestAnEdgeWithoutADropIsGoneAfterItsTimeLimit(t *testing.T) {
	display := startXvfb(t)
	drags := openDragsOn(t, display)
	drags.limits.catch = 300 * time.Millisecond
	observer := dialForTest(t, display, func([]byte) {})
	mapped := time.Now()
	catch := catchOn(t, drags)

	drop, delivered := nextDrop(t, catch)
	waited := time.Since(mapped)

	if !delivered || !errors.Is(drop.Err, ErrNoDropAtEdge) || drop.Paths != nil {
		t.Errorf("Dropped gave %+v, %v, want %v", drop, delivered, ErrNoDropAtEdge)
	}
	if limit := drags.limits.catch; waited < limit || waited > limit+2*time.Second {
		t.Errorf("the edge gave up %v after it was mapped, want about %v", waited, limit)
	}
	checkGone(t, observer, catch)
}

func TestLosingTheXServerEndsTheEdge(t *testing.T) {
	display, stop := startXvfbThatCanStop(t)
	catch := catchOn(t, openDragsOn(t, display))

	stop()

	if drop, delivered := nextDrop(t, catch); !delivered || !errors.Is(drop.Err, ErrClosed) {
		t.Errorf("Dropped gave %+v, %v, want %v", drop, delivered, ErrClosed)
	}
}

func TestAnEdgeStripTheProtocolCannotCarryIsRefused(t *testing.T) {
	for _, strip := range []Rect{
		{X: 0, Y: 0, Width: 1, Height: 100},
		{X: 0, Y: 0, Width: 40, Height: 1},
		{X: 0, Y: 0, Width: 0, Height: 100},
		{X: 0, Y: 0, Width: 40, Height: -1},
		{X: 0, Y: 0, Width: 65536, Height: 100},
		{X: 40000, Y: 0, Width: 40, Height: 100},
		{X: 0, Y: -40000, Width: 40, Height: 100},
	} {
		if catch, err := (&Drags{}).CatchAtEdge(strip); !errors.Is(err, ErrEdgeStrip) || catch != nil {
			t.Errorf("CatchAtEdge(%+v) = %v, %v, want %v", strip, catch, err, ErrEdgeStrip)
		}
	}
}

func grabTheServer(t *testing.T, display string) (*conn, func()) {
	t.Helper()
	grabber := dialForTest(t, display, func([]byte) {})
	grabber.send(newRequest(opGrabServer, 0).done())
	syncWith(t, grabber)
	return grabber, func() {
		grabber.send(newRequest(opUngrabServer, 0).done())
		syncWith(t, grabber)
	}
}

func waitUntilOverIs(t *testing.T, catch *EdgeCatch, want bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for catch.Over() != want {
		if time.Now().After(deadline) {
			t.Fatalf("Over stayed %v, want %v", !want, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTheEdgeIsAcceptedOnlyOnceTheXServerHasItsStatus(t *testing.T) {
	display := startXvfb(t)
	drags := openDragsOn(t, display)
	catch := catchOn(t, drags)
	source := startXdndSource(t, display, sourceOffer{types: []string{"text/uri-list"}, list: listOf(pathsDraggedAfter...)})
	target := enterTheEdge(t, drags, source, catch)
	syncWith(t, source.c)

	grabber, ungrab := grabTheServer(t, display)
	place := uint32(inTheStripX)<<16 | uint32(inTheStripY)
	sendClientMessage(grabber, target, target, source.atoms["XdndPosition"], [5]uint32{source.window, 0, place, currentTime, source.atoms["XdndActionCopy"]})
	syncWith(t, grabber)
	select {
	case <-catch.Accepted():
		t.Error("Accepted closed while the X server could not have taken the XdndStatus yet")
	case <-time.After(300 * time.Millisecond):
	}
	if catch.Over() {
		t.Error("Over was true while the X server could not have taken the XdndStatus yet")
	}
	ungrab()

	waitForEdgeAccepted(t, catch)
	if !catch.Over() {
		t.Error("Over is false after the edge accepted the drag")
	}
	if status := source.status(t, target); !status.accepted || status.action != "XdndActionCopy" {
		t.Errorf("XdndStatus = %+v, want an accepted XdndActionCopy", status)
	}
}

func TestTheEdgeMappedUnderAStillCursorTakesTheDropOnlyAfterANudge(t *testing.T) {
	display := startXvfb(t)
	drags := openDragsOn(t, display)
	standIn := startXdndTarget(t, display, targetBehaviour{})
	source := openDragsOn(t, display)
	input := startFakeInput(t, display)
	input.moveTo(t, 20, 20)
	drag, err := source.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(drag.Cancel)
	input.press(t, 1)
	if err := drag.FilesArrived(arrivedPaths); err != nil {
		t.Fatalf("FilesArrived: %v", err)
	}
	for _, at := range [][2]int{{100, 60}, {150, 150}, {inTheStripX, inTheStripY}} {
		input.moveTo(t, at[0], at[1])
		time.Sleep(10 * time.Millisecond)
	}
	deadline := time.Now().Add(3 * time.Second)
	for standIn.lastPosition() != [2]int{inTheStripX, inTheStripY} {
		if time.Now().After(deadline) {
			t.Fatalf("the drag never reached %d,%d over the window below the strip", inTheStripX, inTheStripY)
		}
		time.Sleep(5 * time.Millisecond)
	}

	catch := catchOn(t, drags)
	select {
	case <-catch.Accepted():
		t.Error("Accepted closed although the cursor did not move over the edge window")
	case <-time.After(300 * time.Millisecond):
	}
	if catch.Over() {
		t.Error("Over was true although the cursor did not move over the edge window")
	}
	input.moveTo(t, inTheStripX+1, inTheStripY)
	input.moveTo(t, inTheStripX, inTheStripY)
	waitForEdgeAccepted(t, catch)
	if !catch.Over() {
		t.Error("Over is false right after the edge accepted the drag")
	}
	input.release(t, 1)

	if err := waitForDrag(t, drag); err != nil {
		t.Errorf("the source drag ended with %v, want a drop the edge took", err)
	}
	if drop, delivered := nextDrop(t, catch); !delivered || drop.Err != nil || !slices.Equal(drop.Paths, arrivedPaths) {
		t.Errorf("Dropped gave %+v, %v, want %q", drop, delivered, arrivedPaths)
	}
	if seen := standIn.messages(); slices.Contains(seen, "XdndDrop") || !slices.Contains(seen, "XdndLeave") {
		t.Errorf("the window below the strip saw %v, want an XdndLeave and no XdndDrop", seen)
	}
}

func TestXdndLeaveTakesTheDragOffTheEdgeAndALaterDropIsIgnored(t *testing.T) {
	display := startXvfb(t)
	drags := openDragsOn(t, display)
	catch := catchOn(t, drags)
	source := startXdndSource(t, display, filesOffer(pathsDraggedAfter...))
	target := enterTheEdge(t, drags, source, catch)
	source.position(target, inTheStripX, inTheStripY, source.serverTime(t), "XdndActionCopy")
	source.status(t, target)
	waitForEdgeAccepted(t, catch)
	if !catch.Over() {
		t.Error("Over is false after the edge accepted the drag")
	}

	source.leave(target)

	waitUntilOverIs(t, catch, false)
	source.drop(target, source.serverTime(t))
	if reply, answered := source.replyFrom(target, "XdndFinished", 500*time.Millisecond); answered {
		t.Errorf("a drop after XdndLeave got XdndFinished %+v", reply)
	}
	select {
	case drop := <-catch.Dropped():
		t.Errorf("a drop after XdndLeave ended the edge with %+v", drop)
	default:
	}
}

func TestTheEdgeFindsTheAddressListOnlyInXdndTypeList(t *testing.T) {
	display := startXvfb(t)
	drags := openDragsOn(t, display)
	catch := catchOn(t, drags)
	path, content := fileToDrag(t)
	types := []string{"application/vnd.portal.filetransfer", "application/vnd.portal.files", "x-special/gnome-icon-list", "text/uri-list"}
	source := startXdndSource(t, display, sourceOffer{types: types, list: listOf(path)})
	source.deletesOnMove(path)

	target := enterTheEdge(t, drags, source, catch)
	source.position(target, inTheStripX, inTheStripY, source.serverTime(t), "XdndActionMove")
	if status := source.status(t, target); !status.accepted || status.action != "XdndActionCopy" {
		t.Errorf("XdndStatus = %+v, want an accepted XdndActionCopy", status)
	}
	source.drop(target, source.serverTime(t))

	if finished := source.finished(t, target); !finished.accepted || finished.action != "XdndActionCopy" {
		t.Errorf("XdndFinished = %+v, want an accepted XdndActionCopy", finished)
	}
	if drop, delivered := nextDrop(t, catch); !delivered || drop.Err != nil || !slices.Equal(drop.Paths, []string{path}) {
		t.Errorf("Dropped gave %+v, %v, want %q", drop, delivered, path)
	}
	checkUntouched(t, path, content)
}

func TestClosingTheEdgeWhileItReadsADropFinishesTheDropAsNotTaken(t *testing.T) {
	display := startXvfb(t)
	drags := openDragsOn(t, display)
	catch := catchOn(t, drags)
	observer := dialForTest(t, display, func([]byte) {})
	source := startXdndSource(t, display, sourceOffer{types: nautilusTypes, silent: true})
	target := enterTheEdge(t, drags, source, catch)
	source.position(target, inTheStripX, inTheStripY, source.serverTime(t), "XdndActionCopy")
	source.status(t, target)
	source.drop(target, source.serverTime(t))
	source.wasAsked(t, "text/uri-list")

	started := time.Now()
	if err := catch.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	if took := time.Since(started); took > time.Second {
		t.Errorf("Close took %v while the edge read the drop, want it at once", took)
	}
	if finished := source.finished(t, target); finished.accepted || finished.action != "" {
		t.Errorf("XdndFinished = %+v, want a drop not taken, with no action", finished)
	}
	if reply, answered := source.replyFrom(target, "XdndFinished", 300*time.Millisecond); answered {
		t.Errorf("the source got a second XdndFinished %+v", reply)
	}
	checkGone(t, observer, catch)
}
