package main

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
	"github.com/rodrigogaraujo/lanhop/internal/event"
)

type replayLog struct {
	mutex   sync.Mutex
	entries []string
	keeps   bool
}

func (r *replayLog) Emit(events ...event.Event) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for _, e := range events {
		r.entries = append(r.entries, nameOfEvent(e))
	}
	return nil
}

func (r *replayLog) Close() error {
	r.record("close")
	return nil
}

func (r *replayLog) record(entry string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.entries = append(r.entries, entry)
}

func (r *replayLog) seen() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return slices.Clone(r.entries)
}

func (r *replayLog) count(entry string) int {
	found := 0
	for _, each := range r.seen() {
		if each == entry {
			found++
		}
	}
	return found
}

type keepingReplayLog struct {
	*replayLog
}

func (r keepingReplayLog) HoldButton(button int) {
	r.record(fmt.Sprintf("hold %d", button))
}

func (r keepingReplayLog) ForgetButton(button int) {
	r.record(fmt.Sprintf("forget %d", button))
}

func nameOfEvent(e event.Event) string {
	switch {
	case e == event.Sync():
		return "sync"
	case e == event.Key(event.ButtonLeft, event.KeyPressed):
		return "left down"
	case e == event.Key(event.ButtonLeft, event.KeyReleased):
		return "left up"
	case e == event.Key(keyEscape, event.KeyPressed):
		return "escape down"
	case e == event.Key(keyEscape, event.KeyReleased):
		return "escape up"
	case e.Type == event.TypeRelative && e.Code == event.RelX:
		return fmt.Sprintf("x %+d", e.Value)
	case e.Type == event.TypeRelative && e.Code == event.RelY:
		return fmt.Sprintf("y %+d", e.Value)
	}
	return fmt.Sprintf("%+v", e)
}

func onlyEvents(entries []string) []string {
	return slices.DeleteFunc(slices.Clone(entries), func(entry string) bool { return entry == "sync" })
}

func expectEntries(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s: %q, want %q", what, got, want)
	}
}

func TestTheEscapeOfAPendingDropIsNotReplayedNorItsRelease(t *testing.T) {
	log := &replayLog{}
	hand := newDropReplay(log, nil)

	hand.dropStarts()
	hand.Emit(event.Key(keyEscape, event.KeyPressed), event.Sync())
	hand.Emit(event.Key(keyEscape, event.KeyRepeated), event.Sync())
	hand.dropSettled()
	hand.Emit(event.Key(keyEscape, event.KeyReleased), event.Sync())
	hand.Emit(event.Key(keyEscape, event.KeyPressed), event.Sync(), event.Key(keyEscape, event.KeyReleased), event.Sync())

	expectEntries(t, "replayed", onlyEvents(log.seen()), "escape down", "escape up")
}

func TestInputHeldBackDuringTheStartOfADropFollowsTheAdoptedButton(t *testing.T) {
	log := &replayLog{}
	hand := newDropReplay(keepingReplayLog{log}, nil)

	held := hand.holdBack()
	hand.Emit(event.Relative(event.RelX, 3), event.Sync())
	hand.Emit(event.Key(event.ButtonLeft, event.KeyReleased), event.Sync())
	if seen := log.seen(); len(seen) != 0 {
		t.Fatalf("the replay got %q while the drop started", seen)
	}
	if releasedEarlier := held.resumeHoldingTheLeftButton(false); releasedEarlier {
		t.Error("a release held back during the start counted as a release before the drop")
	}

	expectEntries(t, "replayed", onlyEvents(log.seen()), "hold 0", "x +3", "left up")
	if hand.holdsTheDropButton() {
		t.Error("the drop still holds the button after the person released it")
	}
}

func TestOnLinuxTheVirtualButtonIsPressedRightAfterTheStartAndBeforeTheInputHeldBack(t *testing.T) {
	log := &replayLog{}
	hand := newDropReplay(log, nil)

	held := hand.holdBack()
	hand.Emit(event.Relative(event.RelY, -2), event.Sync())
	held.resumeHoldingTheLeftButton(true)
	hand.Emit(event.Key(event.ButtonLeft, event.KeyReleased), event.Sync())

	expectEntries(t, "replayed", onlyEvents(log.seen()), "left down", "y -2", "left up")
}

func TestAReleaseJustBeforeTheDropBeganReleasesTheDropButtonAtOnce(t *testing.T) {
	cases := []struct {
		name     string
		before   time.Duration
		released bool
	}{
		{"a second before", time.Second, true},
		{"longer before than lanhop waits", releaseBeforeTheDrop + time.Second, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log := &replayLog{}
			hand := newDropReplay(log, nil)
			now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
			hand.now = func() time.Time { return now }
			hand.Emit(event.Key(event.ButtonLeft, event.KeyReleased), event.Sync())
			now = now.Add(c.before)

			released := hand.holdBack().resumeHoldingTheLeftButton(true)

			want := []string{"left up", "left down"}
			if c.released {
				want = append(want, "left up")
			}
			if released != c.released {
				t.Errorf("released at once = %v, want %v", released, c.released)
			}
			expectEntries(t, "replayed", onlyEvents(log.seen()), want...)
		})
	}
}

func TestAReleaseAfterAPressOfThePersonIsNotOneBeforeTheDrop(t *testing.T) {
	log := &replayLog{}
	hand := newDropReplay(log, nil)
	hand.Emit(event.Key(event.ButtonLeft, event.KeyPressed), event.Sync(), event.Key(event.ButtonLeft, event.KeyReleased), event.Sync())

	if hand.holdBack().resumeHoldingTheLeftButton(true) {
		t.Error("the release of a click counted as a release before the drop")
	}
}

func TestTheDropButtonIsLetGoOnlyWhileTheDropHoldsIt(t *testing.T) {
	t.Run("posted on Linux", func(t *testing.T) {
		log := &replayLog{}
		hand := newDropReplay(log, nil)
		hand.holdBack().resumeHoldingTheLeftButton(true)

		hand.letGoOfTheDropButton()
		hand.letGoOfTheDropButton()

		expectEntries(t, "replayed", onlyEvents(log.seen()), "left down", "left up")
	})
	t.Run("after the release of the person", func(t *testing.T) {
		log := &replayLog{}
		hand := newDropReplay(log, nil)
		hand.holdBack().resumeHoldingTheLeftButton(true)
		hand.Emit(event.Key(event.ButtonLeft, event.KeyReleased), event.Sync())

		hand.letGoOfTheDropButton()

		expectEntries(t, "replayed", onlyEvents(log.seen()), "left down", "left up")
	})
	t.Run("forgotten on a Mac", func(t *testing.T) {
		log := &replayLog{}
		hand := newDropReplay(keepingReplayLog{log}, nil)
		hand.holdBack().resumeHoldingTheLeftButton(false)

		hand.letGoOfTheDropButton()

		expectEntries(t, "replayed", onlyEvents(log.seen()), "hold 0", "forget 0")
	})
}

func TestAHoldBackThatIsNeverResumedEndsByItself(t *testing.T) {
	log := &replayLog{}
	hand := newDropReplay(log, nil)
	hand.holdLimit = 20 * time.Millisecond

	hand.holdBack()
	hand.Emit(event.Relative(event.RelX, 1), event.Sync())

	waitUntil(t, "the held back move to be replayed", func() bool { return log.count("x +1") == 1 })
}

func TestHoldBacksThatOverlapReplayOnlyOnceBothEnd(t *testing.T) {
	log := &replayLog{}
	hand := newDropReplay(log, nil)
	first, second := hand.holdBack(), hand.holdBack()
	hand.Emit(event.Relative(event.RelX, 1), event.Sync())

	first.resume()
	first.resume()
	if seen := log.seen(); len(seen) != 0 {
		t.Fatalf("the replay got %q while a hold back was still on", seen)
	}
	second.resume()

	expectEntries(t, "replayed", onlyEvents(log.seen()), "x +1")
}

func TestLocalDragsAreResetOnAReplayedLeftButtonOrEscape(t *testing.T) {
	var resets atomic.Int32
	hand := newDropReplay(&replayLog{}, func() { resets.Add(1) })

	hand.Emit(event.Relative(event.RelX, 1), event.Sync(), event.Key(event.KeyA, event.KeyPressed), event.Sync())
	if got := resets.Load(); got != 0 {
		t.Fatalf("a move and a key reset the local drags %d times", got)
	}
	hand.Emit(event.Key(event.ButtonLeft, event.KeyPressed), event.Sync())
	hand.Emit(event.Key(event.ButtonLeft, event.KeyReleased), event.Sync())
	hand.Emit(event.Key(keyEscape, event.KeyPressed), event.Sync())
	hand.emitForLanhop(event.Key(event.ButtonLeft, event.KeyReleased), event.Sync())

	if got := resets.Load(); got != 3 {
		t.Errorf("the local drags were reset %d times, want once for the press, the release and Escape", got)
	}
}

func TestForgettingTheLeftButtonPostsNothing(t *testing.T) {
	log := &replayLog{}
	hand := newDropReplay(keepingReplayLog{log}, nil)
	hand.Emit(event.Key(event.ButtonLeft, event.KeyPressed), event.Sync())

	hand.forgetTheLeftButton()

	expectEntries(t, "replayed", onlyEvents(log.seen()), "left down", "forget 0")
}

func TestAClosedDropReplayDropsWhatWasHeldBack(t *testing.T) {
	log := &replayLog{}
	hand := newDropReplay(log, nil)
	held := hand.holdBack()
	hand.Emit(event.Relative(event.RelX, 1), event.Sync())

	hand.Close()
	held.resumeHoldingTheLeftButton(true)

	expectEntries(t, "replayed", log.seen(), "close")
}

func TestForwardedDragsStopWithTheirWatch(t *testing.T) {
	events := make(chan int)
	var closed atomic.Bool
	drags, stop := forwardDrags(context.Background(), events, func(n int) localDrag {
		return localDrag{Started: n > 0, Paths: []string{fmt.Sprint(n)}}
	}, func() { closed.Store(true) })

	go func() { events <- 7 }()
	if drag := <-drags; !drag.Started || !slices.Equal(drag.Paths, []string{"7"}) {
		t.Errorf("forwarded %+v, want a started drag of 7", drag)
	}
	stop()
	stop()
	if !closed.Load() {
		t.Error("stopping did not close the watch")
	}
	if _, open := <-drags; open {
		t.Error("the drags are still open after stop")
	}
}

func TestForwardedDragsEndWhenTheWatchEnds(t *testing.T) {
	events := make(chan int)
	drags, stop := forwardDrags(context.Background(), events, func(int) localDrag { return localDrag{} }, func() {})
	defer stop()

	close(events)

	select {
	case _, open := <-drags:
		if open {
			t.Error("a drag came from a watch that ended")
		}
	case <-time.After(testPatience):
		t.Fatal("the drags did not end with the watch")
	}
}

func TestDragsAreAllowedOnlyWithFilesMovingAndADropSession(t *testing.T) {
	session := newXdndDrops(nil, nil, nil, &screen{})
	cases := []struct {
		name     string
		switches copySwitches
		drops    bool
		want     bool
	}{
		{"files moving with a drop session", copySwitches{clipboard: true, files: true}, true, true},
		{"files off", copySwitches{clipboard: true}, true, false},
		{"the clipboard off", copySwitches{files: true}, true, false},
		{"no drop session", copySwitches{clipboard: true, files: true}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var drops clipshare.Drops
			if c.drops {
				drops = session
			}
			if got := dragsAllowed(c.switches, drops); got != c.want {
				t.Errorf("dragsAllowed = %v, want %v", got, c.want)
			}
		})
	}
}
