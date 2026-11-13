package clipshare

import (
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
)

func TestTurningTheClipboardKeepsEveryOtherFeatureOfThisProcess(t *testing.T) {
	shared := new(features.Set)
	shared.Turn(8, true)
	config := Config{Features: shared}.withDefaults()

	if got := config.turnTheClipboard(true).Value(); got != event.FeatureClipboard|1<<8 {
		t.Errorf("turning the clipboard on left %#x, want %#x", got, event.FeatureClipboard|1<<8)
	}
	if got := config.turnTheClipboard(false).Value(); got != 1<<8 {
		t.Errorf("turning the clipboard off left %#x, want %#x", got, 1<<8)
	}
}

type connThatHoldsCopies struct {
	recordingConn
	release   chan struct{}
	announced []int32
}

func (c *connThatHoldsCopies) Send(events ...event.Event) error {
	if clipboard.IsCopyFrame(events[0]) {
		<-c.release
	}
	return c.recordingConn.Send(events...)
}

func (c *connThatHoldsCopies) SendFeatures(announced *features.Set) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.announced = append(c.announced, announced.Value())
	return nil
}

func TestAnAnnouncementCarriesTheFeaturesAsTheyAreWhenItIsWritten(t *testing.T) {
	conn := &connThatHoldsCopies{release: make(chan struct{})}
	l := startLink(conn, "the other machine", time.Second, &logBook{})
	t.Cleanup(l.close)
	shared := new(features.Set)

	l.sendCopy("a copy the link is still writing")
	shared.Turn(features.Clipboard, true)
	l.announce(shared)
	shared.Turn(8, true)
	close(conn.release)

	eventually(t, "the announcement is written", func() bool {
		conn.mutex.Lock()
		defer conn.mutex.Unlock()
		return len(conn.announced) == 1
	})
	if got := conn.announced[0]; got != event.FeatureClipboard|1<<8 {
		t.Errorf("the announcement carried %#x, want the features as they were when it was written, %#x", got, event.FeatureClipboard|1<<8)
	}
}

func TestOnlyTheClipboardBitSaysWhetherTheOtherEndTakesCopies(t *testing.T) {
	if takesCopies(event.Features(1 << 8)) {
		t.Error("a machine that announced only another feature counts as taking copies")
	}
	if !takesCopies(event.Features(event.FeatureClipboard | 1<<8)) {
		t.Error("a machine that announced the clipboard and another feature does not count as taking copies")
	}
}
