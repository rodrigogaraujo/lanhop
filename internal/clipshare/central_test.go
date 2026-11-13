package clipshare

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

type connToASide struct {
	*recordingConn
	name string
}

func (c *connToASide) Receive() (event.Event, error) {
	select {}
}

func (c *connToASide) Peer() trust.Peer {
	return trust.Peer{Name: c.name}
}

func copiesIn(frames []event.Event) []string {
	var assembler clipboard.Assembler
	var copies []string
	for _, frame := range frames {
		if text, progress, _ := assembler.Take(frame); progress == clipboard.Arrived {
			copies = append(copies, text)
		}
	}
	return copies
}

func TestAnAnswerThatOvertakesTheNewsOfWhereFocusWentStillCountsAsTheAnswerToThatLeave(t *testing.T) {
	board := holding("what the central held before")
	central := NewCentral(Config{Open: opening(board), SilenceLimit: testPatience})
	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		central.Run(ctx)
	}()
	t.Cleanup(func() {
		stop()
		<-stopped
	})
	left := &connToASide{recordingConn: &recordingConn{}, name: "left"}
	right := &connToASide{recordingConn: &recordingConn{}, name: "right"}
	fromLeft, fromRight := central.Connected(left), central.Connected(right)
	fromLeft(event.Features(event.FeatureClipboard))
	fromRight(event.Features(event.FeatureClipboard))
	eventually(t, "both side machines taking part", func() bool { return len(namesTakingPart(t, central)) == 2 })
	eventually(t, "the central taking a fresh mark", func() bool { return board.timesMarked() >= 2 })
	board.personCopies("a copy on the central")
	central.FocusArrives(left)
	eventually(t, "left getting the copy", func() bool { return len(copiesIn(left.frames())) == 1 })

	central.FocusLeaves(left)
	fromLeft(event.ClipboardUnchanged())
	central.FocusArrives(right)

	deadline := time.Now().Add(time.Second)
	for len(copiesIn(right.frames())) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("right never got the copy, so the answer of left was taken for something else than the answer to its leave")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := copiesIn(right.frames()); got[0] != "a copy on the central" {
		t.Errorf("right got %q", got[0])
	}
}

func centralIsOn(c *Central) bool {
	answer := make(chan bool, 1)
	c.tell(func() { answer <- c.on })
	return <-answer
}

func TestATimerFromBeforeTheCentralWasSwitchedOffNeverGivesUpOnALaterLeave(t *testing.T) {
	board := holding("what the central held before")
	var switchedOn atomic.Bool
	switchedOn.Store(true)
	central := NewCentral(Config{Open: opening(board), SwitchedOn: switchedOn.Load, SilenceLimit: testSilenceLimit})
	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		central.Run(ctx)
	}()
	t.Cleanup(func() {
		stop()
		<-stopped
	})
	left := &connToASide{recordingConn: &recordingConn{}, name: "left"}
	right := &connToASide{recordingConn: &recordingConn{}, name: "right"}
	fromLeft, fromRight := central.Connected(left), central.Connected(right)
	fromLeft(event.Features(event.FeatureClipboard))
	fromRight(event.Features(event.FeatureClipboard))
	eventually(t, "both side machines taking part", func() bool { return len(namesTakingPart(t, central)) == 2 })
	eventually(t, "the central taking a fresh mark", func() bool { return board.timesMarked() >= 2 })

	central.FocusArrives(left)
	central.FocusLeaves(left)
	firstLeave := time.Now()
	central.FocusArrives(right)
	time.Sleep(testSilenceLimit / 2)
	marks := board.timesMarked()
	switchedOn.Store(false)
	central.FocusLeaves(right)
	central.FocusArrives(nil)
	eventually(t, "the central switching off", func() bool { return !centralIsOn(central) })
	switchedOn.Store(true)
	central.FocusArrives(nil)
	eventually(t, "the central taking its arrival mark and a fresh mark once it is on again", func() bool { return board.timesMarked() >= marks+2 })
	board.personCopies("copied on the central after it came back on")
	central.FocusArrives(left)
	eventually(t, "left getting the copy", func() bool { return len(copiesIn(left.frames())) == 1 })
	central.FocusLeaves(left)
	central.FocusArrives(right)
	time.Sleep(time.Until(firstLeave.Add(testSilenceLimit * 6 / 5)))
	fromLeft(event.ClipboardUnchanged())
	fromLeft(event.ClipboardUnchanged())

	eventually(t, "right getting the copy once left answered its second leave", func() bool {
		got := copiesIn(right.frames())
		return len(got) == 1 && got[0] == "copied on the central after it came back on"
	})
}
