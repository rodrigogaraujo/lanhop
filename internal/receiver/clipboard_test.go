package receiver

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
)

type recordedClipboard struct {
	mutex sync.Mutex
	heard []string
	conns []Connection
}

func (c *recordedClipboard) note(news string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.heard = append(c.heard, news)
}

func (c *recordedClipboard) Connected(conn Connection) func(event.Event) {
	c.mutex.Lock()
	c.conns = append(c.conns, conn)
	c.mutex.Unlock()
	c.note("connected")
	return func(frame event.Event) { c.note(fmt.Sprintf("heard %+v", frame)) }
}

func (c *recordedClipboard) FocusArrives() { c.note("focus arrives") }
func (c *recordedClipboard) FocusLeaves()  { c.note("focus leaves") }
func (c *recordedClipboard) Lost()         { c.note("lost") }

func (c *recordedClipboard) news() []string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return slices.Clone(c.heard)
}

func (c *recordedClipboard) connection(t *testing.T) Connection {
	t.Helper()
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if len(c.conns) == 0 {
		t.Fatal("the clipboard never heard of a connection")
	}
	return c.conns[len(c.conns)-1]
}

func (c *recordedClipboard) expect(t *testing.T, want ...string) {
	t.Helper()
	for deadline := time.Now().Add(testPatience); ; time.Sleep(2 * time.Millisecond) {
		got := c.news()
		if slices.Equal(got, want) {
			return
		}
		if len(got) >= len(want) || time.Now().After(deadline) {
			t.Fatalf("the clipboard heard\n%q\nwant\n%q", got, want)
		}
	}
}

func heard(frame event.Event) string {
	return fmt.Sprintf("heard %+v", frame)
}

func connectedDeskWithAClipboard(t *testing.T) (*desk, *recordedClipboard, *heldAddress, *transport.Conn) {
	t.Helper()
	d := newDesk(t)
	board := &recordedClipboard{}
	d.clipboard = board
	address := holdAddress(t)
	listener := d.sharerListens(t, loopback)
	address.passTo(t, listener)
	d.receiverRuns(t, address.String())
	return d, board, address, accept(t, listener)
}

func TestTheClipboardHearsOfFocusAndOfControlFramesAndInputIsReplayedAsBefore(t *testing.T) {
	d, board, _, sharerSide := connectedDeskWithAClipboard(t)
	copyFrame := event.Clipboard(5)

	send(t, sharerSide,
		event.Features(event.FeatureClipboard),
		event.Enter(),
		event.Key(event.KeyA, pressed), event.Sync(),
		copyFrame,
		event.Key(event.KeyA, released), event.Sync(),
		event.Leave(),
	)

	board.expect(t, "connected", heard(event.Features(event.FeatureClipboard)), "focus arrives", heard(copyFrame), "focus leaves")
	d.replay.expect(t,
		event.Key(event.KeyA, pressed), event.Sync(),
		event.Key(event.KeyA, released), event.Sync(),
	)
}

func TestTheClipboardSendsOnTheConnectionItIsGivenAndHearsWhenItIsLost(t *testing.T) {
	_, board, address, sharerSide := connectedDeskWithAClipboard(t)
	board.expect(t, "connected")

	if err := board.connection(t).Send(event.Features(event.FeatureClipboard)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got, err := sharerSide.Receive(); err != nil || got != event.Features(event.FeatureClipboard) {
		t.Fatalf("the sharer received %+v, %v, want the features the clipboard sent", got, err)
	}
	address.refuse()
	sharerSide.Close()

	board.expect(t, "connected", "lost")
}
