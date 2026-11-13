package clipshare

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/event"
)

var errClosedByTheTest = errors.New("the connection is closed")

type recordingConn struct {
	mutex  sync.Mutex
	sent   []event.Event
	closed bool
}

func (c *recordingConn) Send(events ...event.Event) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return errClosedByTheTest
	}
	c.sent = append(c.sent, events...)
	return nil
}

func (c *recordingConn) Close() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.closed = true
	return nil
}

func (c *recordingConn) frames() []event.Event {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return slices.Clone(c.sent)
}

func (c *recordingConn) isClosed() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.closed
}

func linkOver(t *testing.T, conn *recordingConn, silenceLimit time.Duration) (*link, *logBook) {
	t.Helper()
	log := &logBook{}
	l := startLink(conn, "the other machine", silenceLimit, log)
	t.Cleanup(l.close)
	return l, log
}

func framesOf(t *testing.T, text string) []event.Event {
	t.Helper()
	parts, err := clipboard.Slices(text)
	if err != nil {
		t.Fatalf("Slices: %v", err)
	}
	return slices.Concat(parts...)
}

func TestALinkSendsAtMostTheWindowAndGoesOnAsAcknowledgementsComeBack(t *testing.T) {
	conn := &recordingConn{}
	l, _ := linkOver(t, conn, testPatience)
	text := strings.Repeat("x", clipboard.Limit)
	want := framesOf(t, text)

	l.sendCopy(text)
	acknowledged := 0
	for acknowledged < len(want) {
		onTheWay := min(acknowledged+clipboard.WindowFrames, len(want))
		eventually(t, "the next frames of the copy", func() bool { return len(conn.frames()) >= onTheWay })
		time.Sleep(time.Millisecond)
		if sent := len(conn.frames()); sent != onTheWay {
			t.Fatalf("%d copy frames were sent with %d acknowledged, want %d", sent, acknowledged, onTheWay)
		}
		acknowledged = onTheWay
		l.acknowledged(int32(acknowledged))
	}

	if !slices.Equal(conn.frames(), want) {
		t.Error("the frames sent are not the frames of the copy")
	}
}

func TestALinkSendsFeaturesAndAnswersWithoutWaitingForTheWindow(t *testing.T) {
	conn := &recordingConn{}
	l, _ := linkOver(t, conn, testPatience)
	full := strings.Repeat("x", 4*clipboard.WindowFrames)

	l.sendCopy(full)
	l.sendFrame(event.Features(event.FeatureClipboard))
	eventually(t, "the window filling", func() bool { return len(conn.frames()) == clipboard.WindowFrames })
	l.acknowledged(clipboard.WindowFrames)

	eventually(t, "the features after the copy", func() bool {
		return slices.Contains(conn.frames(), event.Features(event.FeatureClipboard))
	})
}

func TestALinkClosesTheConnectionOnAnAcknowledgementOfFramesNeverSent(t *testing.T) {
	conn := &recordingConn{}
	l, log := linkOver(t, conn, testPatience)

	l.acknowledged(1)

	if !conn.isClosed() {
		t.Error("the connection stayed open")
	}
	if !strings.Contains(log.String(), clipboard.ErrAck.Error()) {
		t.Errorf("the log says %q, want the reason", log.String())
	}
}

func TestALinkClosesTheConnectionWhenItWaitsForRoomLongerThanTheSilenceLimit(t *testing.T) {
	conn := &recordingConn{}
	silenceLimit := 50 * time.Millisecond
	l, _ := linkOver(t, conn, silenceLimit)
	started := time.Now()

	l.sendCopy(strings.Repeat("x", 2*clipboard.WindowFrames*4))

	eventually(t, "the connection closing", conn.isClosed)
	if waited := time.Since(started); waited < silenceLimit {
		t.Errorf("the connection closed after %s, before the silence limit of %s", waited, silenceLimit)
	}
}

func TestALeaveCutsShortACopyOnItsWayAndRefusesNewCopiesUntilItIsHandled(t *testing.T) {
	conn := &recordingConn{}
	l, _ := linkOver(t, conn, testPatience)
	l.sendCopy(strings.Repeat("x", 2*clipboard.WindowFrames*4))
	eventually(t, "the window filling", func() bool { return len(conn.frames()) == clipboard.WindowFrames })

	if !l.leave() {
		t.Error("a copy on its way was not cut short by the leave")
	}
	if l.sendCopy("sent before the leave was handled") {
		t.Error("a copy was queued while the leave was not handled yet")
	}
	l.acknowledged(clipboard.WindowFrames)
	l.leaveHandled()
	if !l.sendCopy("after the leave") {
		t.Fatal("a copy was refused after the leave was handled")
	}

	after := framesOf(t, "after the leave")
	eventually(t, "the copy after the leave", func() bool {
		frames := conn.frames()
		return len(frames) >= len(after) && slices.Equal(frames[len(frames)-len(after):], after)
	})
	if sent := len(conn.frames()); sent != clipboard.WindowFrames+len(after) {
		t.Errorf("%d frames were sent, want the first window and the copy after the leave", sent)
	}
}

func TestALeaveAfterACopyFinishedDoesNotCutItShort(t *testing.T) {
	conn := &recordingConn{}
	l, _ := linkOver(t, conn, testPatience)
	small := framesOf(t, "small")

	l.sendCopy("small")
	eventually(t, "the small copy sent whole", func() bool { return len(conn.frames()) == len(small) && l.sentWhole() })

	if l.leave() {
		t.Error("a copy that was sent whole was taken as cut short")
	}
}

func (l *link) sentWhole() bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.current == nil || l.current.done
}

func (l *link) copiesStillToSend() int {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	copies := 0
	if l.current != nil && l.current.isACopy && !l.current.stopped && !l.current.done {
		copies++
	}
	for _, d := range l.waiting {
		if d.isACopy {
			copies++
		}
	}
	return copies
}

func TestALinkKeepsOnlyTheNewestCopyAndStopsTheOneOnItsWay(t *testing.T) {
	conn := &recordingConn{}
	l, _ := linkOver(t, conn, testPatience)
	first := strings.Repeat("1", 2*clipboard.WindowFrames*4)
	l.sendNewestCopy(first)
	eventually(t, "the window filling", func() bool { return len(conn.frames()) == clipboard.WindowFrames })

	for i := range 28 {
		l.sendNewestCopy(strings.Repeat(string(rune('a'+i%26)), 2*clipboard.WindowFrames*4))
	}
	newest := "the newest copy"
	l.sendNewestCopy(newest)

	if waiting := l.copiesStillToSend(); waiting != 1 {
		t.Errorf("%d copies are still to send on the link, want only the newest", waiting)
	}
	l.acknowledged(clipboard.WindowFrames)
	after := framesOf(t, newest)
	eventually(t, "the newest copy", func() bool {
		frames := conn.frames()
		return len(frames) >= len(after) && slices.Equal(frames[len(frames)-len(after):], after)
	})
	if sent := len(conn.frames()); sent != clipboard.WindowFrames+len(after) {
		t.Errorf("%d frames were sent, want the start of the first copy and the newest copy, nothing in between", sent)
	}
}

func TestTheReadingSideOfALinkAcknowledgesWhatItTakes(t *testing.T) {
	conn := &recordingConn{}
	l, _ := linkOver(t, conn, testPatience)

	var arrived string
	for _, frame := range framesOf(t, "hello") {
		text, progress, err := l.take(frame)
		if err != nil {
			t.Fatalf("take: %v", err)
		}
		if progress == clipboard.Arrived {
			arrived = text
		}
	}

	if arrived != "hello" {
		t.Errorf("the link assembled %q, want hello", arrived)
	}
	if got := conn.frames(); !slices.Equal(got, []event.Event{event.ClipboardAck(3)}) {
		t.Errorf("the link sent %+v, want one acknowledgement of the 3 frames", got)
	}
}
