package clipshare

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/event"
)

func TestAPairedSideMachineCannotWriteTheClipboardOfTheCentralUnasked(t *testing.T) {
	d := newDesk(t, "rogue")
	rogue := d.rawSideTakesCopies("rogue", true)
	d.waitUntilTheCentralSeesTakingPart("rogue")

	sendCopy(t, rogue.conn, "pushed without being asked")
	rogue.answers(t, clipboard.Withdrawal(), event.ClipboardUnchanged())
	time.Sleep(settling)

	expectUnwritten(t, "the central, which focus never left", d.clipboard, "what the central held before")
}

func TestAPairedSideMachineCannotPushACopyToTheMachineThatHasFocus(t *testing.T) {
	d := newDesk(t, "rogue", "left")
	rogue := d.rawSideTakesCopies("rogue", true)
	left := d.sideJoins("left")
	d.waitUntilTakingPart(left)
	d.waitUntilTheCentralSeesTakingPart("left", "rogue")

	d.focusOn(left.number)
	sendCopy(t, rogue.conn, "pushed to the machine with focus")
	time.Sleep(settling)
	expectUnwritten(t, "left", left.clipboard, "what left held before")

	d.focusOn(d.sides["rogue"].number)
	sendCopy(t, rogue.conn, "pushed while it had focus")
	time.Sleep(settling)
	d.focusOn(thisMachine)
	time.Sleep(testSilenceLimit + settling)
	expectUnwritten(t, "the central", d.clipboard, "what the central held before")
}

func TestAnAnswerAfterOneThatWasGivenUpStillCountsWhenASideMachineOwesSeveral(t *testing.T) {
	d := newDesk(t, "slow", "left")
	slow := d.rawSideTakesCopies("slow", true)
	left := d.sideJoins("left")
	d.waitUntilTakingPart(left)
	d.waitUntilTheCentralSeesTakingPart("left", "slow")
	slowNumber := d.sides["slow"].number

	d.focusOn(slowNumber)
	d.focusOn(left.number)
	slow.answers(t, event.ClipboardUnchanged())
	d.focusOn(slowNumber)
	d.focusOn(left.number)
	time.Sleep(testSilenceLimit + settling)
	d.focusOn(slowNumber)
	d.focusOn(left.number)

	slow.answers(t, event.ClipboardUnchanged())
	sendCopy(t, slow.conn, "the answer to the third leave")
	waitUntilItHolds(t, "left", left.clipboard, "the answer to the third leave")
}

func TestAnAnswerOnItsWayWhileTheCentralWasSwitchedOffIsDroppedAfterItIsOnAgain(t *testing.T) {
	d := newDesk(t, "slow", "left")
	slow := d.rawSideTakesCopies("slow", true)
	left := d.sideJoins("left")
	d.waitUntilTakingPart(left)
	d.waitUntilTheCentralSeesTakingPart("left", "slow")

	d.focusOn(d.sides["slow"].number)
	d.focusOn(left.number)
	d.switchedOn.Store(false)
	d.focusOn(thisMachine)
	d.waitUntilTheCentralIs(false)
	d.switchedOn.Store(true)
	d.focusOn(left.number)
	d.waitUntilTheCentralIs(true)
	sendCopy(t, slow.conn, "an answer from before the switch")
	time.Sleep(settling)

	if slices.ContainsFunc(left.clipboard.written(), func(w timedWrite) bool { return w.text == "an answer from before the switch" }) {
		t.Error("left got an answer that was on its way while the central was off")
	}
}

func TestASideMachineThatConnectsAgainWhileItOwesAnAnswerCannotAnswerOnItsNewConnection(t *testing.T) {
	d := newDesk(t, "slow", "left")
	d.rawSideTakesCopies("slow", true)
	left := d.sideJoins("left")
	d.waitUntilTakingPart(left)
	d.waitUntilTheCentralSeesTakingPart("left", "slow")
	d.clipboard.personCopies("before the new connection")

	d.focusOn(d.sides["slow"].number)
	d.focusOn(left.number)
	again := d.rawSideTakesCopies("slow", true)
	sendCopy(t, again.conn, "pushed on the new connection")
	time.Sleep(testSilenceLimit + settling)

	expectUnwritten(t, "left", left.clipboard, "what left held before")
}

func TestASideMachineThatConnectsAgainWhileACopyIsOnItsWayGetsTheWholeCopyWhenFocusComesBack(t *testing.T) {
	d := newDesk(t, "big")
	first := d.rawSideTakesCopies("big", false)
	d.waitUntilTheCentralSeesTakingPart("big")
	large := strings.Repeat("a copy larger than the window ", clipboard.Limit/30)
	d.clipboard.personCopies(large)
	d.focusOn(d.sides["big"].number)
	time.Sleep(settling)

	again := d.rawSideTakesCopies("big", true)
	eventually(t, "the central closing the first connection", first.ended)
	d.waitUntilTheCentralSeesTakingPart("big")
	d.focusOn(d.sides["big"].number)

	eventually(t, "the new connection getting the whole copy", func() bool {
		got := again.received()
		return len(got) == 1 && got[0] == large
	})
}

func TestASideMachineWhoseReadOutlastedTheCentralBeingSwitchedOffKeepsMovingItsCopies(t *testing.T) {
	d := newDesk(t, "slow", "right")
	slow, right := d.sideJoins("slow"), d.sideJoins("right")
	d.waitUntilTakingPart(slow, right)
	release := slow.clipboard.holdReadsUntilReleased()
	t.Cleanup(release)
	readWhileOff := "copied on slow before the central was switched off"

	d.focusOn(slow.number)
	slow.clipboard.personCopies(readWhileOff)
	d.focusOn(right.number)
	time.Sleep(testSilenceLimit + settling)
	d.switchedOn.Store(false)
	d.focusOn(thisMachine)
	d.waitUntilTheCentralIs(false)
	time.Sleep(settling)
	release()
	eventually(t, "slow finishing its read", func() bool { return slow.clipboard.timesRead() >= 1 })
	time.Sleep(settling)
	d.switchedOn.Store(true)
	marksBefore := slow.clipboard.timesMarked()
	d.focusOn(right.number)
	d.waitUntilTheCentralIs(true)
	eventually(t, "slow taking a fresh mark once the central is on again", func() bool { return slow.clipboard.timesMarked() > marksBefore })

	for _, text := range []string{"the first copy on slow after the switch", "the second copy on slow after the switch"} {
		d.focusOn(slow.number)
		slow.clipboard.personCopies(text)
		d.focusOn(right.number)
		waitUntilItHolds(t, "right", right.clipboard, text)
	}
	if slices.ContainsFunc(right.clipboard.written(), func(w timedWrite) bool { return w.text == readWhileOff }) {
		t.Error("right got the copy that slow read while the central was off")
	}
}

func TestASideMachineSwitchedOffAndOnWhileItOwedAnAnswerKeepsMovingItsCopies(t *testing.T) {
	d := newDesk(t, "slow", "right")
	slow, right := d.sideJoins("slow"), d.sideJoins("right")
	d.waitUntilTakingPart(slow, right)
	release := slow.clipboard.holdReadsUntilReleased()
	t.Cleanup(release)

	d.focusOn(slow.number)
	slow.clipboard.personCopies("copied on slow before it was switched off")
	d.focusOn(right.number)
	time.Sleep(settling)
	slow.switchedOn.Store(false)
	d.focusOn(slow.number)
	eventually(t, "the central seeing slow switch off", func() bool { return !slices.Contains(namesTakingPart(t, d.runtime), "slow") })
	release()
	eventually(t, "slow finishing its read", func() bool { return slow.clipboard.timesRead() >= 1 })
	slow.switchedOn.Store(true)
	d.focusOn(right.number)
	d.waitUntilTheCentralSeesTakingPart("right", "slow")

	for _, text := range []string{"the first copy on slow after it was switched on", "the second copy on slow after it was switched on"} {
		d.focusOn(slow.number)
		slow.clipboard.personCopies(text)
		d.focusOn(right.number)
		waitUntilItHolds(t, "right", right.clipboard, text)
	}
}
