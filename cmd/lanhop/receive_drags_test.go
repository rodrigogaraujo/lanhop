package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
)

type closerFunc func() error

func (c closerFunc) Close() error { return c() }

func TestOnAMacTheDragThatLeftIsEndedUnderTheShieldAndThenItsButtonIsForgotten(t *testing.T) {
	log := &replayLog{}
	hand := newDropReplay(keepingReplayLog{log}, nil)
	shield := func(context.Context) (io.Closer, error) {
		log.record("shield")
		return closerFunc(func() error { log.record("shield closed"); return nil }), nil
	}
	end := func() error { log.record("source drag ended"); return nil }

	endTheSourceDragUnderAShield(context.Background(), shield, end, hand, &screen{})

	expectEntries(t, "the steps", log.seen(), "shield", "source drag ended", "shield closed", "forget 0")
}

func TestOnAMacTheDragThatLeftIsStillEndedWhenTheShieldFails(t *testing.T) {
	log := &replayLog{}
	hand := newDropReplay(keepingReplayLog{log}, nil)
	said := &screen{}
	shield := func(context.Context) (io.Closer, error) { return nil, errors.New("no window") }
	end := func() error { log.record("source drag ended"); return nil }

	endTheSourceDragUnderAShield(context.Background(), shield, end, hand, said)

	expectEntries(t, "the steps", log.seen(), "source drag ended", "forget 0")
	if !strings.Contains(said.String(), "no window") {
		t.Errorf("the log said %q, want the shield failure", said.String())
	}
}

func TestOnACentralMacTheDragThatLeftIsEndedWithoutAReplay(t *testing.T) {
	log := &replayLog{}
	shield := func(context.Context) (io.Closer, error) {
		log.record("shield")
		return closerFunc(func() error { log.record("shield closed"); return nil }), nil
	}

	endTheSourceDragUnderAShield(context.Background(), shield, func() error { log.record("source drag ended"); return nil }, nil, &screen{})

	expectEntries(t, "the steps", log.seen(), "shield", "source drag ended", "shield closed")
}

type edgeHandLog struct {
	*replayLog
}

func (h edgeHandLog) Nudge(dx, dy int) error {
	h.record(fmt.Sprintf("nudge %d,%d", dx, dy))
	return nil
}

func (h edgeHandLog) Release() error {
	h.record("release")
	return nil
}

func (h edgeHandLog) Escape() error {
	h.record("escape")
	return nil
}

func TestAtAnEdgeWithoutTheCatcherLanhopPressesEscapeReleasesAndSaysSo(t *testing.T) {
	log := &replayLog{}
	said := &screen{}

	fallBackAtTheEdge(edgeHandLog{log}, said, func() { log.record("told the central") }, errors.New("no X server"))

	expectEntries(t, "the steps", log.seen(), "escape", "release", "told the central")
	if !strings.Contains(said.String(), "no X server") {
		t.Errorf("the log said %q, want the reason", said.String())
	}
}

type describedLocally struct {
	mutex sync.Mutex
	said  []string
}

func (d *describedLocally) DragSeen(paths []string, dock edge.Side) {
	d.add(fmt.Sprintf("seen %s dock %v", strings.Join(paths, ","), dock))
}

func (d *describedLocally) DragGone() { d.add("gone") }

func (d *describedLocally) DockIs(dock edge.Side) { d.add(fmt.Sprintf("dock %v", dock)) }

func (d *describedLocally) add(entry string) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.said = append(d.said, entry)
}

func (d *describedLocally) entries() []string {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return slices.Clone(d.said)
}

type localWatches struct {
	mutex   sync.Mutex
	started int
	stopped int
	drags   chan localDrag
	failure error
}

func (w *localWatches) watch(context.Context) (<-chan localDrag, func(), error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.failure != nil {
		return nil, func() {}, w.failure
	}
	w.started++
	w.drags = make(chan localDrag)
	return w.drags, func() {
		w.mutex.Lock()
		defer w.mutex.Unlock()
		w.stopped++
	}, nil
}

func (w *localWatches) counts() (started, stopped int) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.started, w.stopped
}

func (w *localWatches) current() chan localDrag {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.drags
}

type dockReadings struct {
	mutex sync.Mutex
	side  edge.Side
}

func (d *dockReadings) read() edge.Side {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.side
}

func (d *dockReadings) moveTo(side edge.Side) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.side = side
}

func followerForTheTest(t *testing.T, every time.Duration, watches *localWatches, log io.Writer) (*localDragFollower, *describedLocally, *dockReadings) {
	t.Helper()
	side, dock := &describedLocally{}, &dockReadings{side: edge.Left}
	follower := &localDragFollower{side: side, watch: watches.watch, dock: dock.read, every: every, log: log, changes: make(chan bool, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		follower.run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	return follower, side, dock
}

func TestLocalDragsAreWatchedOnlyWhileTheyCanBeDescribed(t *testing.T) {
	watches := &localWatches{}
	follower, side, _ := followerForTheTest(t, time.Hour, watches, &screen{})

	follower.describing(true)
	waitUntil(t, "the watch to start", func() bool { started, _ := watches.counts(); return started == 1 })
	watches.current() <- localDrag{Started: true, Paths: []string{"/home/me/a.txt"}}
	watches.current() <- localDrag{}
	watches.current() <- localDrag{Started: true, Paths: []string{"/home/me/b.txt"}}
	follower.describing(false)
	waitUntil(t, "the watch to stop", func() bool { _, stopped := watches.counts(); return stopped == 1 })

	waitUntil(t, "the side to hear the drags", func() bool { return len(side.entries()) == 5 })
	expectEntries(t, "the side heard", side.entries(), "dock left", "seen /home/me/a.txt dock left", "gone", "seen /home/me/b.txt dock left", "gone")
}

func TestTheDockIsReadAgainWhileDragsCanBeDescribed(t *testing.T) {
	follower, side, dock := followerForTheTest(t, 10*time.Millisecond, &localWatches{}, &screen{})

	follower.describing(true)
	waitUntil(t, "the first reading", func() bool { return slices.Contains(side.entries(), "dock left") })
	dock.moveTo(edge.Bottom)

	waitUntil(t, "the Dock moved to the bottom", func() bool { return slices.Contains(side.entries(), "dock bottom") })
}

func TestAWatchThatCannotStartIsSaidOnceAndTriedAgain(t *testing.T) {
	watches := &localWatches{failure: errors.New("no X server")}
	said := &screen{}
	follower, _, _ := followerForTheTest(t, 10*time.Millisecond, watches, said)

	follower.describing(true)
	time.Sleep(60 * time.Millisecond)
	watches.mutex.Lock()
	watches.failure = nil
	watches.mutex.Unlock()

	waitUntil(t, "the watch to start once it can", func() bool { started, _ := watches.counts(); return started == 1 })
	if times := strings.Count(said.String(), "no X server"); times != 1 {
		t.Errorf("the failure was said %d times, want once: %q", times, said.String())
	}
}
