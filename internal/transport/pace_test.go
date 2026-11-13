package transport

import (
	"fmt"
	"testing"
	"time"
)

type clockThatWakesLate struct {
	now    time.Time
	lateBy time.Duration
	sleeps int
}

func (c *clockThatWakesLate) read() time.Time {
	return c.now
}

func (c *clockThatWakesLate) sleep(asked time.Duration) {
	c.sleeps++
	c.now = c.now.Add(asked + c.lateBy)
}

func paceOn(clock *clockThatWakesLate, bytesPerSecond int) *Pace {
	pace := NewPace(bytesPerSecond)
	pace.now = clock.read
	pace.sleep = clock.sleep
	return pace
}

func writeThroughThePace(pace *Pace, total int) int {
	written := 0
	for written < total {
		pace.wait(pacedSlice)
		written += pacedSlice
	}
	return written
}

func TestThePaceKeepsItsRateWhenTimersWakeUpTo10MillisecondsLate(t *testing.T) {
	const bytesPerSecond = 10_000_000
	for _, lateBy := range []time.Duration{0, time.Millisecond, 5 * time.Millisecond, 10 * time.Millisecond} {
		t.Run(fmt.Sprintf("%s late", lateBy), func(t *testing.T) {
			clock := &clockThatWakesLate{now: time.Unix(1_000_000, 0), lateBy: lateBy}
			pace := paceOn(clock, bytesPerSecond)
			started := clock.now

			written := writeThroughThePace(pace, 100_000_000)
			took := clock.now.Sub(started)

			atThePace := time.Duration(float64(written) / bytesPerSecond * float64(time.Second))
			least := time.Duration(float64(written-paceBurst) / bytesPerSecond * float64(time.Second))
			if took < least-time.Microsecond {
				t.Errorf("%d bytes went in %s, faster than the pace allows (%s)", written, took, least)
			}
			if took > atThePace+atThePace/100 {
				t.Errorf("%d bytes took %s with timers %s late, want at most 1%% over %s", written, took, lateBy, atThePace)
			}
		})
	}
}

func TestThePaceLets128KiBGoAtOnceAndThenWaits(t *testing.T) {
	clock := &clockThatWakesLate{now: time.Unix(1_000_000, 0)}
	pace := paceOn(clock, 10_000_000)

	writeThroughThePace(pace, paceBurst)
	if clock.sleeps != 0 {
		t.Errorf("the first 128 KiB waited %d times, want none", clock.sleeps)
	}
	pace.wait(pacedSlice)
	if clock.sleeps != 1 {
		t.Errorf("the slice after 128 KiB waited %d times, want once", clock.sleeps)
	}
}

func TestAnUnpacedPaceNeverWaits(t *testing.T) {
	clock := &clockThatWakesLate{now: time.Unix(1_000_000, 0)}
	pace := paceOn(clock, Unpaced)

	writeThroughThePace(pace, 100_000_000)

	if clock.sleeps != 0 {
		t.Errorf("an unpaced pace waited %d times", clock.sleeps)
	}
}

func TestAPaceThatWasNeverSetUpActsAsTheDefaultPace(t *testing.T) {
	var zero Pace
	const beyondTheBurst = defaultDataBytesPerSecond / 10

	start := time.Now()
	zero.wait(paceBurst)
	zero.wait(beyondTheBurst)
	took := time.Since(start)

	if least := 90 * time.Millisecond; took < least {
		t.Errorf("a pace that was never set up let %d bytes past the burst go in %v, want at least %v, as the default pace", beyondTheBurst, took, least)
	}
	if most := time.Second; took > most {
		t.Errorf("a pace that was never set up took %v, want under %v", took, most)
	}
}

type steppingClock struct {
	at time.Time
}

func (c *steppingClock) now() time.Time { return c.at }

func (c *steppingClock) after(step time.Duration) { c.at = c.at.Add(step) }

func paceThatFollowsTheDelayOn(clock *steppingClock) *Pace {
	pace := newPaceThatFollowsTheDelay(defaultDataBytesPerSecond)
	pace.now = clock.now
	return pace
}

func megabitsOf(pace *Pace) float64 {
	return pace.currentBytesPerSecond() * 8 / 1_000_000
}

func TestThePaceStartsAt40MegabitsAndDoublesEach100MillisecondsWhileTheQueueIsEmpty(t *testing.T) {
	clock := &steppingClock{at: time.Unix(1000, 0)}
	pace := paceThatFollowsTheDelayOn(clock)
	connection := new(int)
	if got := megabitsOf(pace); got != 40 {
		t.Fatalf("the pace starts at %.1f Mbit/s, want 40", got)
	}
	pace.observe(connection, 2*time.Millisecond)
	for range 5 {
		clock.after(10 * time.Millisecond)
		pace.observe(connection, 2*time.Millisecond)
	}
	if got := megabitsOf(pace); got < 55 || got > 58 {
		t.Errorf("after 50 ms with an empty queue the pace is %.1f Mbit/s, want about 56.6", got)
	}
	for range 10 {
		clock.after(10 * time.Millisecond)
		pace.observe(connection, 2*time.Millisecond)
	}
	if got := megabitsOf(pace); got != 80 {
		t.Errorf("after 150 ms with an empty queue the pace is %.1f Mbit/s, want the ceiling of 80", got)
	}
}

func TestAQueueAboveTheTargetLowersThePaceAtMostEvery50Milliseconds(t *testing.T) {
	clock := &steppingClock{at: time.Unix(1000, 0)}
	pace := paceThatFollowsTheDelayOn(clock)
	connection := new(int)
	pace.observe(connection, 2*time.Millisecond)
	for range 3 {
		clock.after(time.Millisecond)
		pace.observe(connection, 32*time.Millisecond)
	}
	before := megabitsOf(pace)
	if before > 41 {
		t.Fatalf("the pace is %.1f Mbit/s before the queue built up, want about 40", before)
	}
	clock.after(time.Millisecond)
	pace.observe(connection, 32*time.Millisecond)
	first := megabitsOf(pace)
	if first > 0.55*before || first < 0.45*before {
		t.Errorf("a queue twice the target took the pace from %.1f to %.1f Mbit/s, want about half", before, first)
	}
	clock.after(20 * time.Millisecond)
	pace.observe(connection, 32*time.Millisecond)
	if got := megabitsOf(pace); got != first {
		t.Errorf("a second sample 20 ms later lowered the pace again, to %.1f Mbit/s", got)
	}
	clock.after(40 * time.Millisecond)
	pace.observe(connection, 32*time.Millisecond)
	if got := megabitsOf(pace); got >= first {
		t.Errorf("60 ms after the first decrease the pace is still %.1f Mbit/s, want lower", got)
	}
}

func TestAfterADecreaseThePaceRisesSlowly(t *testing.T) {
	clock := &steppingClock{at: time.Unix(1000, 0)}
	pace := paceThatFollowsTheDelayOn(clock)
	connection := new(int)
	pace.observe(connection, 2*time.Millisecond)
	for range 4 {
		clock.after(time.Millisecond)
		pace.observe(connection, 32*time.Millisecond)
	}
	for range 4 {
		clock.after(time.Millisecond)
		pace.observe(connection, 2*time.Millisecond)
	}
	low := megabitsOf(pace)
	for range 10 {
		clock.after(100 * time.Millisecond)
		pace.observe(connection, 2*time.Millisecond)
	}
	if got := megabitsOf(pace); got > low+20.5 {
		t.Errorf("one second after a decrease the pace rose from %.1f to %.1f Mbit/s, want at most 20 Mbit/s more", low, got)
	}
}

func TestThePaceNeverGoesBelow1Megabit(t *testing.T) {
	clock := &steppingClock{at: time.Unix(1000, 0)}
	pace := paceThatFollowsTheDelayOn(clock)
	connection := new(int)
	pace.observe(connection, time.Millisecond)
	for range 200 {
		clock.after(60 * time.Millisecond)
		pace.observe(connection, time.Second)
	}
	if got := megabitsOf(pace); got != 1 {
		t.Errorf("behind a queue that never drains the pace is %.2f Mbit/s, want the floor of 1", got)
	}
}

func TestTheWorstFreshConnectionSetsThePace(t *testing.T) {
	clock := &steppingClock{at: time.Unix(1000, 0)}
	pace := paceThatFollowsTheDelayOn(clock)
	calm, crowded := new(int), new(int)
	pace.observe(calm, 2*time.Millisecond)
	pace.observe(crowded, 2*time.Millisecond)
	for range 4 {
		clock.after(time.Millisecond)
		pace.observe(crowded, 40*time.Millisecond)
	}
	clock.after(time.Millisecond)
	pace.observe(calm, 2*time.Millisecond)
	lowered := megabitsOf(pace)
	if lowered >= 40 {
		t.Fatalf("a crowded connection next to a calm one left the pace at %.1f Mbit/s, want lower", lowered)
	}
	clock.after(300 * time.Millisecond)
	pace.observe(calm, 2*time.Millisecond)
	if got := megabitsOf(pace); got <= lowered {
		t.Errorf("300 ms after the crowded connection last spoke, the pace stayed at %.1f Mbit/s, want it to rise again", got)
	}
}

func TestThePaceStartsAgainAfterASecondWithoutTransfers(t *testing.T) {
	clock := &steppingClock{at: time.Unix(1000, 0)}
	pace := paceThatFollowsTheDelayOn(clock)
	connection := new(int)
	pace.observe(connection, 2*time.Millisecond)
	for range 4 {
		clock.after(time.Millisecond)
		pace.observe(connection, 40*time.Millisecond)
	}
	clock.after(2 * time.Second)
	pace.observe(new(int), 2*time.Millisecond)
	if got := megabitsOf(pace); got != 40 {
		t.Errorf("after two quiet seconds the pace is %.1f Mbit/s, want the start of 40", got)
	}
}

func TestAFixedPaceIgnoresTheDelay(t *testing.T) {
	pace := NewPace(2_000_000)
	connection := new(int)
	pace.observe(connection, time.Millisecond)
	pace.observe(connection, time.Second)
	if got := pace.currentBytesPerSecond(); got != 2_000_000 {
		t.Errorf("a fixed pace moved to %.0f bytes per second, want 2000000", got)
	}
}
