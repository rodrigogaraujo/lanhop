package sharer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

type recordedClipboard struct {
	journal *journal
}

func clipboardHears(news string) string {
	return "clipboard hears: " + news
}

func (c recordedClipboard) Connected(conn Conn) func(event.Event) {
	c.journal.note(clipboardHears(conn.Peer().Name + " connected"))
	return func(frame event.Event) {
		c.journal.note(clipboardHears(fmt.Sprintf("%s says %+v", conn.Peer().Name, frame)))
	}
}

func (c recordedClipboard) Lost(conn Conn) {
	c.journal.note(clipboardHears(conn.Peer().Name + " lost"))
}

func (c recordedClipboard) FocusLeaves(conn Conn) {
	c.journal.note(clipboardHears("focus leaves " + conn.Peer().Name))
}

func (c recordedClipboard) FocusArrives(conn Conn) {
	if conn == nil {
		c.journal.note(clipboardHears("focus arrives on this machine"))
		return
	}
	c.journal.note(clipboardHears("focus arrives on " + conn.Peer().Name))
}

func startSharingWithAClipboard(t *testing.T, peers ...trust.Peer) *sharing {
	t.Helper()
	s := newSharing(t, peers...)
	s.clipboard = recordedClipboard{journal: s.journal}
	s.start(s.capture)
	return s
}

func (s *sharing) connectSaying(peer trust.Peer) *fakeReceiver {
	receiver := &fakeReceiver{journal: s.journal, peer: peer, says: make(chan event.Event), lost: make(chan struct{}), shut: make(chan struct{})}
	s.sharer.Connect(receiver)
	return receiver
}

func (s *sharing) expectInOrder(wanted ...string) {
	s.t.Helper()
	entries := s.journal.read()
	next := 0
	for _, entry := range entries {
		if next < len(wanted) && entry == wanted[next] {
			next++
		}
	}
	if next < len(wanted) {
		s.t.Errorf("the journal lacks %q after %q, it is:\n%s", wanted[next], wanted[:next], strings.Join(entries, "\n"))
	}
}

func TestTheClipboardHearsThatFocusLeavesBeforeTheLeaveIsSentAndThatItArrivesAfterTheEnter(t *testing.T) {
	s := startSharingWithAClipboard(t, desk, laptop)
	s.connect(desk)
	s.connect(laptop)

	s.focusOn(2)
	s.focusOn(3)
	s.focusOn(1)

	s.expectInOrder(
		clipboardHears("desk connected"),
		clipboardHears("laptop connected"),
		gets(desk, event.Enter())[0],
		clipboardHears("focus arrives on desk"),
		clipboardHears("focus leaves desk"),
		gets(desk, event.Leave())[0],
		gets(laptop, event.Enter())[0],
		clipboardHears("focus arrives on laptop"),
		clipboardHears("focus leaves laptop"),
		gets(laptop, event.Leave())[0],
		clipboardHears("focus arrives on this machine"),
		gaveInputBack,
	)
}

func TestTheClipboardHearsNothingWhenFocusStaysOnThisMachine(t *testing.T) {
	s := startSharingWithAClipboard(t, desk)
	s.connect(desk)

	s.focusOn(1)

	s.expectJournal(then(clipboardHears("desk connected")))
}

func TestWhatAReceiverSaysReachesTheClipboard(t *testing.T) {
	s := startSharingWithAClipboard(t, desk)
	receiver := s.connectSaying(desk)

	receiver.says <- event.Features(event.FeatureClipboard)

	said := clipboardHears(fmt.Sprintf("desk says %+v", event.Features(event.FeatureClipboard)))
	s.eventually("the clipboard hearing the features of desk", func() bool { return s.journal.has(said) })
}

func TestTheClipboardHearsOfAReceiverLostWithFocusAndThenThatFocusIsBackHere(t *testing.T) {
	s := startSharingWithAClipboard(t, desk)
	receiver := s.connect(desk)
	s.focusOn(2)

	receiver.loseConnection()

	s.eventually("the input coming back", func() bool { return s.journal.has(gaveInputBack) })
	s.expectInOrder(clipboardHears("focus arrives on desk"), clipboardHears("desk lost"), clipboardHears("focus arrives on this machine"), gaveInputBack)
}

func TestTheClipboardHearsOfAReceiverThatConnectsAgainAsANewOneAndOfTheOldOneLost(t *testing.T) {
	s := startSharingWithAClipboard(t, desk)
	s.connect(desk)

	s.connect(desk)

	s.eventually("the old connection closing", func() bool { return s.journal.has(closed(desk)) })
	s.expectInOrder(clipboardHears("desk connected"), clipboardHears("desk connected"), clipboardHears("desk lost"))
}

func TestTheClipboardHearsThatFocusArrivedEvenWhenTheEnterFailsAndThenThatItCameBack(t *testing.T) {
	s := startSharingWithAClipboard(t, desk)
	receiver := s.connect(desk)
	receiver.sendFails.Store(true)

	s.focusOn(2)

	s.eventually("the input coming back", func() bool { return s.journal.has(gaveInputBack) })
	s.expectInOrder(clipboardHears("focus arrives on desk"), clipboardHears("desk lost"), clipboardHears("focus arrives on this machine"))
}

func TestStoppingTheSharerTellsTheClipboardThatFocusLeftAndCameBack(t *testing.T) {
	s := startSharingWithAClipboard(t, desk)
	s.connect(desk)
	s.focusOn(2)

	s.stop()
	s.outcome()

	s.expectInOrder(clipboardHears("focus leaves desk"), gets(desk, event.Leave())[0], clipboardHears("focus arrives on this machine"))
}
