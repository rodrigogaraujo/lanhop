package clipshare

import (
	"slices"
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
	order     []string
}

func (c *connThatHoldsCopies) Send(events ...event.Event) error {
	if clipboard.IsCopyFrame(events[0]) {
		<-c.release
		c.mutex.Lock()
		c.order = append(c.order, "copy")
		c.mutex.Unlock()
	}
	return c.recordingConn.Send(events...)
}

func (c *connThatHoldsCopies) SendFeatures(announced *features.Set) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.announced = append(c.announced, announced.Value())
	c.order = append(c.order, "features")
	return nil
}

func (c *connThatHoldsCopies) written() (order []string, announced []int32) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return slices.Clone(c.order), slices.Clone(c.announced)
}

func TestAnAnnouncementWaitsForTheCopyAheadOfItAndCarriesTheFeaturesAsTheyAreWhenWritten(t *testing.T) {
	conn := &connThatHoldsCopies{release: make(chan struct{})}
	l := startLink(conn, "the other machine", time.Second, &logBook{})
	t.Cleanup(l.close)
	shared := new(features.Set)

	l.sendCopy("a copy the link is still writing")
	shared.Turn(features.Clipboard, true)
	l.announce(shared)
	shared.Turn(8, true)
	if _, announced := conn.written(); len(announced) != 0 {
		t.Fatalf("the announcement was written before the copy ahead of it: %#x", announced)
	}
	close(conn.release)

	eventually(t, "the announcement is written", func() bool {
		_, announced := conn.written()
		return len(announced) == 1
	})
	order, announced := conn.written()
	if !slices.Equal(order, []string{"copy", "features"}) {
		t.Errorf("written in the order %q, want the copy then the announcement", order)
	}
	if announced[0] != event.FeatureClipboard|1<<8 {
		t.Errorf("the announcement carried %#x, want the features as they were when it was written, %#x", announced[0], event.FeatureClipboard|1<<8)
	}
}
