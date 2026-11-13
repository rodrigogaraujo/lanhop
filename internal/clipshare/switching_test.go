package clipshare

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

func switchedOffWhileFocusMovesToTheLeft(d *deskUnderTest, left, right *sideUnderTest) {
	d.t.Helper()
	d.waitUntilTakingPart(left, right)
	d.switchedOn.Store(false)
	d.focusOn(left.number)
	d.waitUntilTheCentralIs(false)
	d.switchedOn.Store(true)
}

func TestTheCentralSwitchedOnAsFocusMovesOnDoesNotWaitForTheMachineThatHeardItWasOff(t *testing.T) {
	d := newDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	switchedOffWhileFocusMovesToTheLeft(d, left, right)

	marksBefore := right.clipboard.timesMarked()
	d.focusOn(right.number)
	eventually(t, "right taking its arrival mark and a fresh mark", func() bool { return right.clipboard.timesMarked() >= marksBefore+2 })
	right.clipboard.personCopies("from the right, after the central came on")
	d.focusOn(thisMachine)

	waitUntilItHolds(t, "the central", d.clipboard, "from the right, after the central came on")
}

func TestTheCentralSwitchedOnAsFocusComesBackKeepsItsOwnNextCopy(t *testing.T) {
	d := newDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	switchedOffWhileFocusMovesToTheLeft(d, left, right)

	marksBefore := d.clipboard.timesMarked()
	d.focusOn(thisMachine)
	eventually(t, "the central taking its arrival mark and a fresh mark", func() bool { return d.clipboard.timesMarked() >= marksBefore+2 })
	d.clipboard.personCopies("the next copy of the central")
	d.focusOn(right.number)

	waitUntilItHolds(t, "right", right.clipboard, "the next copy of the central")
}

func TestTheCentralThatLosesItsClipboardStopsSharingAndSharesAgainWhenItIsBack(t *testing.T) {
	first := &endingClipboard{fakeClipboard: holding("the first clipboard of the central"), gone: make(chan struct{})}
	plugged := &pluggedClipboard{current: first}
	d := newDeskWithAClipboard(t, first.fakeClipboard, plugged.open, "right")
	right := d.sideJoins("right")
	d.waitUntilTakingPart(right)

	plugged.plug(nil)
	close(first.gone)
	d.log.waitFor(t, "lanhop lost the clipboard of this machine")
	d.waitUntilTheCentralIs(false)

	second := holding("the second clipboard of the central")
	plugged.plug(second)
	marksBefore := right.clipboard.timesMarked()
	d.focusOn(right.number)
	d.log.waitFor(t, "lanhop reaches the clipboard of this machine again")
	eventually(t, "right taking a fresh mark once the central takes copies again", func() bool { return right.clipboard.timesMarked() >= marksBefore+2 })
	right.clipboard.personCopies("copied after the central got its clipboard back")
	d.focusOn(thisMachine)

	waitUntilItHolds(t, "the second clipboard of the central", second, "copied after the central got its clipboard back")
	if writes := first.written(); len(writes) > 0 {
		t.Errorf("the first clipboard of the central was written %+v after it was gone", writes)
	}
}

func TestFeaturesThatFlapMakeTheCentralTakeOnlyAFewMarks(t *testing.T) {
	d := newDesk(t, "flapping")
	flapping := d.rawSideTakesCopies("flapping", true)
	d.waitUntilTheCentralSeesTakingPart("flapping")
	eventually(t, "the central taking its fresh mark", func() bool { return d.clipboard.timesMarked() >= 2 })
	marksBefore := d.clipboard.timesMarked()
	release := d.clipboard.holdMarksUntilReleased()
	released := false
	defer func() {
		if !released {
			release()
		}
	}()

	const pairs = 20000
	batch := make([]event.Event, 0, 2000)
	for range 1000 {
		batch = append(batch, event.Features(0), event.Features(event.FeatureClipboard))
	}
	for range pairs / 1000 {
		flapping.answers(t, batch...)
	}
	time.Sleep(time.Second)
	release()
	released = true
	time.Sleep(settling)

	if taken := d.clipboard.timesMarked() - marksBefore; taken > 10 {
		t.Errorf("the central took %d marks for %d flaps of the features, want only a few", taken, pairs)
	}
}

func TestASideMachineThatCannotReachItsClipboardSaysWhyOnOneLineAndWaitsBeforeTryingAgain(t *testing.T) {
	d := newDesk(t, "right")
	right := d.sides["right"]
	var attempts atomic.Int32
	d.startSide(right, d.address, func() (Clipboard, error) {
		attempts.Add(1)
		return nil, errors.Join(errors.New("no X server in DISPLAY"), errors.New("no socket in /tmp/.X11-unix"))
	})

	for range 5 {
		d.focusOn(right.number)
		d.focusOn(thisMachine)
	}
	time.Sleep(settling)

	if tried := attempts.Load(); tried < 1 || tried > 3 {
		t.Errorf("the side machine tried to reach its clipboard %d times over 10 focus changes, want 1 to 3", tried)
	}
	right.log.waitFor(t, "cannot reach its clipboard: no X server in DISPLAY; no socket in /tmp/.X11-unix\n")
	if lines := strings.Count(right.log.String(), "\n"); lines != 1 {
		t.Errorf("the log has %d lines, want the reason once on one line:\n%s", lines, right.log.String())
	}
}

func TestASideMachineReachesItsClipboardAgainWithinSecondsOfItsReturnAfterAFailedAttempt(t *testing.T) {
	d := newDesk(t, "right")
	right := d.sides["right"]
	first := &endingClipboard{fakeClipboard: right.clipboard, gone: make(chan struct{})}
	plugged := &pluggedClipboard{current: first}
	var attempts atomic.Int32
	d.startSide(right, d.address, func() (Clipboard, error) {
		attempts.Add(1)
		return plugged.open()
	})
	d.waitUntilTakingPart(right)

	plugged.plug(nil)
	close(first.gone)
	right.log.waitFor(t, "lanhop lost the clipboard of this machine")
	attemptsBefore := attempts.Load()
	d.focusOn(right.number)
	eventually(t, "a failed attempt to reach the clipboard", func() bool { return attempts.Load() > attemptsBefore })

	plugged.plug(holding("the clipboard after the person logged in again"))
	back := time.Now()
	for !strings.Contains(right.log.String(), "lanhop reaches the clipboard of this machine again") {
		if time.Since(back) > 5*time.Second {
			t.Fatalf("the side machine still could not reach its clipboard %s after it came back, with focus moving every 300 ms", time.Since(back).Round(time.Millisecond))
		}
		d.focusOn(thisMachine)
		d.focusOn(right.number)
		time.Sleep(300 * time.Millisecond)
	}
	d.waitUntilTheCentralSeesTakingPart("right")
}
