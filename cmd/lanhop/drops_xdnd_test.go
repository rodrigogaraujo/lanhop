package main

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
	"github.com/rodrigogaraujo/lanhop/internal/event"
)

var (
	errDroppedForTheTest    = errors.New("the window took the drop and said nothing")
	errNotDroppedForTheTest = errors.New("released over nothing")
)

type fakeXdndDrag struct {
	log        *replayLog
	accepted   chan struct{}
	done       chan struct{}
	accepting  sync.Once
	ending     sync.Once
	state      sync.Mutex
	err        error
	given      [][]string
	cancels    int
	refuseThem error
}

func newFakeXdndDrag(log *replayLog) *fakeXdndDrag {
	return &fakeXdndDrag{log: log, accepted: make(chan struct{}), done: make(chan struct{})}
}

func (g *fakeXdndDrag) FilesArrived(paths []string) error {
	g.state.Lock()
	defer g.state.Unlock()
	if g.refuseThem != nil {
		return g.refuseThem
	}
	g.given = append(g.given, slices.Clone(paths))
	return nil
}

func (g *fakeXdndDrag) Cancel() {
	g.state.Lock()
	g.cancels++
	g.state.Unlock()
	g.log.record("drag cancelled")
	g.end(errors.New("cancelled"))
}

func (g *fakeXdndDrag) Accepted() <-chan struct{} { return g.accepted }

func (g *fakeXdndDrag) Done() <-chan struct{} { return g.done }

func (g *fakeXdndDrag) Wait() error {
	<-g.done
	g.state.Lock()
	defer g.state.Unlock()
	return g.err
}

func (g *fakeXdndDrag) accept() {
	g.accepting.Do(func() { close(g.accepted) })
}

func (g *fakeXdndDrag) end(err error) {
	g.ending.Do(func() {
		g.state.Lock()
		g.err = err
		g.state.Unlock()
		close(g.done)
	})
}

func (g *fakeXdndDrag) timesCancelled() int {
	g.state.Lock()
	defer g.state.Unlock()
	return g.cancels
}

type xdndDesk struct {
	log   *replayLog
	hand  *dropReplay
	drops *xdndDrops
	mutex sync.Mutex
	made  []*fakeXdndDrag
}

func newXdndDesk(t *testing.T) *xdndDesk {
	t.Helper()
	d := &xdndDesk{log: &replayLog{}}
	d.hand = newDropReplay(d.log, nil)
	d.drops = newXdndDrops(d.start, d.hand, func(err error, accepted, filesGiven bool) bool {
		return err == nil || errors.Is(err, errDroppedForTheTest)
	}, &screen{})
	t.Cleanup(d.drops.close)
	return d
}

func (d *xdndDesk) start() (xdndDrag, error) {
	d.log.record("drag starts")
	drag := newFakeXdndDrag(d.log)
	d.mutex.Lock()
	d.made = append(d.made, drag)
	d.mutex.Unlock()
	return drag, nil
}

func (d *xdndDesk) startADrop(t *testing.T) (clipshare.DropSession, *fakeXdndDrag) {
	t.Helper()
	session, err := d.drops.StartDrop(context.Background(), []clipshare.DroppedFile{{Name: "report.txt"}})
	if err != nil {
		t.Fatalf("StartDrop: %v", err)
	}
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return session, d.made[len(d.made)-1]
}

func TestTheVirtualButtonIsPressedRightAfterTheLinuxDropStarts(t *testing.T) {
	d := newXdndDesk(t)

	d.startADrop(t)

	expectEntries(t, "the replay", onlyEvents(d.log.seen()), "drag starts", "left down")
}

func TestAcceptedIsTakenAndTheReleaseOfThePersonEndsTheHold(t *testing.T) {
	d := newXdndDesk(t)
	session, drag := d.startADrop(t)

	d.hand.Emit(event.Key(event.ButtonLeft, event.KeyReleased), event.Sync())
	drag.accept()
	closedWithin(t, "Taken", session.Taken())
	drag.end(nil)

	waitUntil(t, "the session to end", func() bool { return len(d.drops.sessions()) == 0 })
	stillOpen(t, "EndedWithoutDrop", session.EndedWithoutDrop())
	if got := d.log.count("left up"); got != 1 {
		t.Errorf("the left button was released %d times, want once by the person", got)
	}
}

func TestADragThatEndsBeforeTheReleaseOfThePersonLetsGoOfTheVirtualButtonAtOnce(t *testing.T) {
	d := newXdndDesk(t)
	session, drag := d.startADrop(t)

	drag.end(errNotDroppedForTheTest)

	closedWithin(t, "EndedWithoutDrop", session.EndedWithoutDrop())
	if got := d.log.count("left up"); got != 1 {
		t.Errorf("the left button was released %d times, want once by lanhop", got)
	}
	d.hand.Emit(event.Key(event.ButtonLeft, event.KeyReleased), event.Sync())
	if got := d.log.count("left up"); got != 2 {
		t.Errorf("the late release of the person was not passed on: %d releases", got)
	}
}

func TestCancelEndsTheDragFirstAndThenLetsGoOfTheVirtualButton(t *testing.T) {
	d := newXdndDesk(t)
	session, drag := d.startADrop(t)

	session.Cancel()

	if drag.timesCancelled() != 1 {
		t.Errorf("the drag was cancelled %d times, want once", drag.timesCancelled())
	}
	entries := onlyEvents(d.log.seen())
	if cancelledAt, releasedAt := slices.Index(entries, "drag cancelled"), slices.Index(entries, "left up"); cancelledAt < 0 || releasedAt < cancelledAt {
		t.Errorf("the replay saw %q, want the drag cancelled, then the button released", entries)
	}
	waitUntil(t, "the session to end", func() bool { return len(d.drops.sessions()) == 0 })
	if got := d.log.count("left up"); got != 1 {
		t.Errorf("the left button was released %d times, want once", got)
	}
}

func TestADropThatMayHaveHappenedIsNotRefused(t *testing.T) {
	d := newXdndDesk(t)
	session, drag := d.startADrop(t)
	d.hand.Emit(event.Key(event.ButtonLeft, event.KeyReleased), event.Sync())

	drag.accept()
	drag.end(errDroppedForTheTest)

	closedWithin(t, "Taken", session.Taken())
	waitUntil(t, "the session to end", func() bool { return len(d.drops.sessions()) == 0 })
	stillOpen(t, "EndedWithoutDrop", session.EndedWithoutDrop())
}

func TestADragAcceptedAsItEndsIsStillTaken(t *testing.T) {
	d := newXdndDesk(t)
	session, drag := d.startADrop(t)

	drag.accept()
	drag.end(nil)

	closedWithin(t, "Taken", session.Taken())
}

func TestTheFilesThatArriveGoToTheDrag(t *testing.T) {
	d := newXdndDesk(t)
	session, drag := d.startADrop(t)

	session.Arrived([]string{"/arrived/report.txt"})

	drag.state.Lock()
	defer drag.state.Unlock()
	if len(drag.given) != 1 || !slices.Equal(drag.given[0], []string{"/arrived/report.txt"}) {
		t.Errorf("the drag got %q, want the arrived path", drag.given)
	}
}

func TestThePreviousLinuxDropIsCancelledBeforeTheNextUnlessItWasAccepted(t *testing.T) {
	d := newXdndDesk(t)
	_, first := d.startADrop(t)
	secondSession, second := d.startADrop(t)
	if first.timesCancelled() != 1 {
		t.Errorf("the first drag was cancelled %d times before the second started, want once", first.timesCancelled())
	}

	second.accept()
	closedWithin(t, "Taken", secondSession.Taken())
	d.startADrop(t)
	if second.timesCancelled() != 0 {
		t.Error("an accepted drag that waits for its files was cancelled when the next started")
	}
}

func TestTheEscapeOfAPendingLinuxDropIsNotReplayed(t *testing.T) {
	d := newXdndDesk(t)
	d.startADrop(t)

	d.hand.Emit(event.Key(event.KeyEsc, event.KeyPressed), event.Sync(), event.Key(event.KeyEsc, event.KeyReleased), event.Sync())

	expectEntries(t, "the replay", onlyEvents(d.log.seen()), "drag starts", "left down")
}
