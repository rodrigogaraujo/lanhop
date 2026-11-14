package clipshare

import (
	"slices"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
)

func TestAnnouncingKindsTurnsTheirBitsAndKeepsEveryOtherFeatureOfThisProcess(t *testing.T) {
	shared := new(features.Set)
	shared.Turn(8, true)
	config := Config{Features: shared}.withDefaults()
	filesBit := int32(1) << features.Files

	for _, announced := range []struct {
		kinds clipboard.Kinds
		want  int32
	}{
		{clipboard.Text, event.FeatureClipboard | 1<<8},
		{clipboard.Text | clipboard.Files, event.FeatureClipboard | filesBit | 1<<8},
		{clipboard.Files, filesBit | 1<<8},
		{0, 1 << 8},
	} {
		if got := config.announcing(announced.kinds).Value(); got != announced.want {
			t.Errorf("announcing %#x left %#x, want %#x", announced.kinds, got, announced.want)
		}
	}
}

func TestOnlyTheBitsOfTextAndFilesSayWhatTheOtherEndTakes(t *testing.T) {
	if got := clipboard.KindsIn(event.Features(1 << 8).Value); got != 0 {
		t.Errorf("a machine that announced only another feature takes %#x, want nothing", got)
	}
	both := event.Features(event.FeatureClipboard | 1<<features.Files | 1<<8)
	if got := clipboard.KindsIn(both.Value); got != clipboard.Text|clipboard.Files {
		t.Errorf("a machine that announced text, files and another feature takes %#x, want text and files", got)
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
