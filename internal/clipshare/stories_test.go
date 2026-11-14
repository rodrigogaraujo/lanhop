package clipshare

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/receiver"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

func TestCopiesFollowFocusBetweenTheCentralAndTwoSideMachinesInEveryDirection(t *testing.T) {
	d := newDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilTakingPart(left, right)

	inEveryLanguage := "olá, ação 🙂👍🏽\tcolumn\nsecond line\r\n日本語のテキスト"
	d.clipboard.personCopies(inEveryLanguage)
	d.focusOn(left.number)
	waitUntilItHolds(t, "left, after a copy on the central", left.clipboard, inEveryLanguage)

	left.clipboard.personCopies("from the left")
	d.focusOn(thisMachine)
	waitUntilItHolds(t, "the central, after a copy on the left", d.clipboard, "from the left")

	d.focusOn(left.number)
	left.clipboard.personCopies("from the left to the right")
	d.focusOn(right.number)
	waitUntilItHolds(t, "right, after a copy on the left", right.clipboard, "from the left to the right")
	if held := d.clipboard.holds(); held != "from the left" {
		t.Errorf("the central holds %s while focus is on the right, want it to keep the copy only in memory", describe(held))
	}
	d.focusOn(thisMachine)
	waitUntilItHolds(t, "the central, once focus comes back", d.clipboard, "from the left to the right")

	d.focusOn(left.number)
	left.clipboard.personCopies("visited the central")
	d.focusOn(thisMachine)
	waitUntilItHolds(t, "the central, visited on the way", d.clipboard, "visited the central")
	d.focusOn(right.number)
	waitUntilItHolds(t, "right, after the visit to the central", right.clipboard, "visited the central")

	d.focusOn(left.number)
	left.clipboard.personCopies("older, from the left")
	d.focusOn(thisMachine)
	waitUntilItHolds(t, "the central", d.clipboard, "older, from the left")
	d.clipboard.personCopies("newer, on the central")
	d.focusOn(right.number)
	waitUntilItHolds(t, "right, after a newer copy on the central", right.clipboard, "newer, on the central")
}

func TestACopyReachesOnlyTheMachinesThatFocusVisits(t *testing.T) {
	d := newDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilTakingPart(left, right)

	d.clipboard.personCopies("a password")
	d.focusOn(left.number)
	waitUntilItHolds(t, "left", left.clipboard, "a password")

	left.clipboard.personCopies("stays on the left while focus stays")
	time.Sleep(settling)
	if writes := d.clipboard.written(); len(writes) > 0 {
		t.Errorf("the central was written %+v while focus stayed on the left", writes)
	}

	d.focusOn(thisMachine)
	waitUntilItHolds(t, "the central, once focus left the left", d.clipboard, "stays on the left while focus stays")
	time.Sleep(settling)
	expectUnwritten(t, "right, which focus never visited", right.clipboard, "what right held before")
}

func TestWhatIsNotTextStaysWhereItIsAndTheCopyBeforeItIsNotHandedOut(t *testing.T) {
	d := newDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilTakingPart(left, right)
	d.clipboard.personCopies("text before the image")
	d.focusOn(left.number)
	waitUntilItHolds(t, "left", left.clipboard, "text before the image")
	d.focusOn(thisMachine)

	d.clipboard.personCopiesWhatLanhopDoesNotCarry(clipboard.ErrNotText)
	d.focusOn(right.number)
	d.log.waitFor(t, clipboard.ErrNotText.Error())

	right.clipboard.personCopiesWhatLanhopDoesNotCarry(clipboard.ErrPrivate)
	d.focusOn(left.number)
	right.log.waitFor(t, clipboard.ErrPrivate.Error())
	time.Sleep(settling)

	expectUnwritten(t, "right", right.clipboard, "")
	if writes := left.clipboard.written(); len(writes) != 1 {
		t.Errorf("left was written %d times, want only the text before the image", len(writes))
	}
}

func TestASideMachineOnTheOldVersionKeepsGettingInputAndNeverGetsACopy(t *testing.T) {
	d := newDesk(t, "left", "old")
	left := d.sideJoins("left")
	old := d.oldSideJoins("old")
	d.waitUntilTakingPart(left)

	d.clipboard.personCopies("not for the old version")
	d.focusOn(d.sides["old"].number)
	d.types()
	d.focusOn(left.number)
	waitUntilItHolds(t, "left, right after the old side machine that never answers", left.clipboard, "not for the old version")

	eventually(t, "the old side machine getting the key and the leave", func() bool {
		frames := old.frames()
		return slices.Contains(frames, press(event.KeyA)) && slices.Contains(frames, event.Leave())
	})
	for _, frame := range old.frames() {
		if clipboard.IsCopyFrame(frame) {
			t.Fatalf("the old side machine got the copy frame %+v", frame)
		}
	}
}

func TestASideMachineUnderACentralOnTheOldVersionReadsNothingAndSaysOnlyThatNothingChanged(t *testing.T) {
	central, side := newIdentity(t), newIdentity(t)
	listener, err := transport.Listen("127.0.0.1:0", transportFor(central), pairedMachines{{Name: "laptop", Fingerprint: side.Fingerprint}})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	board := holding("a copy on the side machine")
	runtime := NewSide(Config{Open: opening(board), SilenceLimit: testSilenceLimit})
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go runtime.Run(ctx)
	go receiver.Run(ctx, receiver.Config{
		Transport: transportFor(side),
		Sharer:    trust.Peer{Name: "desk", Fingerprint: central.Fingerprint, Address: listener.Addr().String()},
		Replay:    &timedReplay{},
		Clipboard: runtime,
	})
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer conn.Close()

	board.personCopies("copied before the leave")
	if err := conn.Send(event.Enter(), press(event.KeyA), event.Sync(), event.Leave()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	frames := make(chan event.Event, 16)
	go func() {
		defer close(frames)
		for {
			frame, err := conn.Receive()
			if err != nil {
				return
			}
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()
	nextWithin := func(limit time.Duration) (event.Event, bool) {
		select {
		case frame, open := <-frames:
			return frame, open
		case <-time.After(limit):
			return event.Event{}, false
		}
	}
	if first, sent := nextWithin(testPatience); !sent || first != event.Features(event.FeatureClipboard) {
		t.Fatalf("the side machine first sent %+v (sent: %v), want Features with bit 0", first, sent)
	}
	if answer, sent := nextWithin(testPatience); !sent || answer != event.ClipboardUnchanged() {
		t.Fatalf("the side machine answered the leave with %+v (sent: %v), want unchanged", answer, sent)
	}
	if next, sent := nextWithin(settling); sent {
		t.Errorf("the side machine sent %+v after its answer, want nothing more", next)
	}
	if reads := board.timesRead(); reads != 0 {
		t.Errorf("the side machine read its clipboard %d times, want never", reads)
	}
}

func (d *deskUnderTest) silentSideHoldsACopyAndFocusMovesOnTo(left *sideUnderTest) *transport.Conn {
	d.t.Helper()
	silent := d.dialAs("silent")
	if err := silent.Send(event.Features(event.FeatureClipboard)); err != nil {
		d.t.Fatalf("Send: %v", err)
	}
	d.waitUntilConnected("silent")
	d.startSide(left, d.address, opening(left.clipboard))
	d.waitUntilTakingPart(left)
	d.waitUntilTheCentralSeesTakingPart("left", "silent")
	d.clipboard.personCopies("before the silence")
	d.focusOn(d.sides["silent"].number)
	d.focusOn(left.number)
	time.Sleep(testSilenceLimit + settling)
	expectUnwritten(d.t, "left, while the silent side machine owes its answer", left.clipboard, "what left held before")
	return silent
}

func TestASideMachineThatDoesNotAnswerWithinTheSilenceLimitCountsAsAWithdrawal(t *testing.T) {
	d := newDesk(t, "silent", "left")
	left := d.sides["left"]
	silent := d.silentSideHoldsACopyAndFocusMovesOnTo(left)

	if err := silent.Send(event.ClipboardUnchanged()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	time.Sleep(settling)

	expectUnwritten(t, "left, after the late word that nothing changed", left.clipboard, "what left held before")
}

func TestAnAnswerThatComesAfterTheCentralGaveUpStillCounts(t *testing.T) {
	d := newDesk(t, "silent", "left")
	left := d.sides["left"]
	silent := d.silentSideHoldsACopyAndFocusMovesOnTo(left)

	sendCopy(t, silent, "the late answer")

	waitUntilItHolds(t, "left, after the late answer", left.clipboard, "the late answer")
}

func TestFocusThatLeavesInTheMiddleOfACopyLeavesTheNextCopyWorking(t *testing.T) {
	d := newDesk(t, "right")
	right := d.sideJoinsOverASlowLink("right")
	d.waitUntilTakingPart(right)
	large := strings.Repeat("a large copy ", clipboard.Limit/13)

	d.clipboard.personCopies(large)
	d.focusOn(right.number)
	time.Sleep(150 * time.Millisecond)
	d.focusOn(thisMachine)
	time.Sleep(settling)
	if writes := right.clipboard.written(); len(writes) > 0 {
		t.Fatalf("right wrote a copy that focus left in the middle")
	}

	d.focusOn(right.number)
	waitUntilItHolds(t, "right, after focus came back to it", right.clipboard, large)
	d.focusOn(thisMachine)
	d.clipboard.personCopies("the next copy")
	d.focusOn(right.number)
	waitUntilItHolds(t, "right, with the next copy", right.clipboard, "the next copy")
}

func TestASideMachineThatSwitchesOffInTheMiddleOfACopyLeavesTheNextCopyWorking(t *testing.T) {
	d := newDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoinsOverASlowLink("right")
	d.waitUntilTakingPart(left, right)
	large := strings.Repeat("a large copy ", clipboard.Limit/13)

	d.clipboard.personCopies(large)
	d.focusOn(right.number)
	time.Sleep(150 * time.Millisecond)
	right.switchedOn.Store(false)
	d.focusOn(left.number)
	d.waitUntilTheCentralSeesTakingPart("left")
	waitUntilItHolds(t, "left", left.clipboard, large)

	left.clipboard.personCopies("after the switch")
	right.switchedOn.Store(true)
	d.focusOn(right.number)
	waitUntilItHolds(t, "right, switched on again", right.clipboard, "after the switch")
	if writes := right.clipboard.written(); len(writes) != 1 {
		t.Errorf("right was written %d times, want only the copy after the switch", len(writes))
	}
}

func TestACopyThePersonMakesAfterFocusArrivedIsNotOverwritten(t *testing.T) {
	d := newDesk(t, "right")
	right := d.sideJoins("right")
	d.waitUntilTakingPart(right)
	d.clipboard.personCopies("from the central")
	releaseTheRead := d.clipboard.holdReadsUntilReleased()
	defer releaseTheRead()
	marksBefore := right.clipboard.timesMarked()

	d.focusOn(right.number)
	eventually(t, "right taking its mark when focus arrived", func() bool { return right.clipboard.timesMarked() > marksBefore })
	right.clipboard.personCopies("made on the right after focus arrived")
	releaseTheRead()
	right.log.waitFor(t, "the newer copy made on this machine after focus arrived was kept")

	if held := right.clipboard.holds(); held != "made on the right after focus arrived" {
		t.Errorf("right holds %s, want the copy the person made there", describe(held))
	}
	d.focusOn(thisMachine)
	waitUntilItHolds(t, "the central, once focus left the right", d.clipboard, "made on the right after focus arrived")
}

func TestACopyMadeOnASideMachineWinsOverTheOlderCopyOfTheCentralWhoseReadEndsLast(t *testing.T) {
	d := newDesk(t, "left", "right")
	left, right := d.sideJoins("left"), d.sideJoins("right")
	d.waitUntilTakingPart(left, right)
	d.clipboard.personCopies("from the central")
	releaseTheRead := d.clipboard.holdReadsUntilReleased()
	defer releaseTheRead()

	d.focusOn(right.number)
	right.clipboard.personCopies("from the right")
	d.focusOn(left.number)
	d.waitUntilTheCentralHasTheAnswerOf(right)
	releaseTheRead()

	eventually(t, "left getting a copy", func() bool { return len(left.clipboard.written()) > 0 })
	if first := left.clipboard.written()[0].text; first != "from the right" {
		t.Errorf("left got %s, want the newer copy from the right", describe(first))
	}
}

func TestTheCentralSwitchedOffMovesNoCopyUntilItIsSwitchedOnAgain(t *testing.T) {
	d := newDesk(t, "right")
	right := d.sideJoins("right")
	d.waitUntilTakingPart(right)

	d.switchedOn.Store(false)
	d.clipboard.personCopies("while the central is off")
	d.focusOn(right.number)
	right.clipboard.personCopies("from the right while the central is off")
	d.focusOn(thisMachine)
	time.Sleep(settling)
	expectUnwritten(t, "right", right.clipboard, "from the right while the central is off")
	if writes := d.clipboard.written(); len(writes) > 0 {
		t.Errorf("the central was written %+v while it was off", writes)
	}

	d.switchedOn.Store(true)
	marksBefore := right.clipboard.timesMarked()
	d.focusOn(right.number)
	eventually(t, "right taking a fresh mark and its arrival mark", func() bool { return right.clipboard.timesMarked() >= marksBefore+2 })
	right.clipboard.personCopies("after the central is on again")
	d.focusOn(thisMachine)
	waitUntilItHolds(t, "the central, on again", d.clipboard, "after the central is on again")
}

type endingClipboard struct {
	*fakeClipboard
	gone chan struct{}
}

func (e *endingClipboard) Done() <-chan struct{} {
	return e.gone
}

type pluggedClipboard struct {
	mutex   sync.Mutex
	current Clipboard
}

func (p *pluggedClipboard) open() (Clipboard, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.current == nil {
		return nil, ErrUnreachable
	}
	return p.current, nil
}

func (p *pluggedClipboard) plug(board Clipboard) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.current = board
}

func TestASideMachineWhoseClipboardGoesAwayStopsSharingAndTakesAFreshMarkWhenItComesBack(t *testing.T) {
	d := newDesk(t, "right")
	right := d.sides["right"]
	first := &endingClipboard{fakeClipboard: right.clipboard, gone: make(chan struct{})}
	plugged := &pluggedClipboard{current: first}
	d.startSide(right, d.address, plugged.open)
	d.waitUntilTakingPart(right)
	marksBefore := right.clipboard.timesMarked()
	d.focusOn(right.number)
	eventually(t, "right taking its arrival mark", func() bool { return right.clipboard.timesMarked() > marksBefore })

	plugged.plug(nil)
	close(first.gone)
	d.waitUntilTheCentralSeesTakingPart()
	right.log.waitFor(t, "lanhop lost the clipboard of this machine")

	second := holding("copied while lanhop could not reach the clipboard")
	second.mark = 7
	plugged.plug(second)
	d.focusOn(thisMachine)
	d.waitUntilTheCentralSeesTakingPart("right")
	right.log.waitFor(t, "lanhop reaches the clipboard of this machine again")

	d.focusOn(right.number)
	d.focusOn(thisMachine)
	time.Sleep(settling)
	for _, write := range d.clipboard.written() {
		if write.text == "copied while lanhop could not reach the clipboard" {
			t.Fatal("the central got a copy made while lanhop could not reach the clipboard of the right")
		}
	}

	d.focusOn(right.number)
	second.personCopies("copied after the clipboard came back")
	d.focusOn(thisMachine)
	waitUntilItHolds(t, "the central", d.clipboard, "copied after the clipboard came back")
}
