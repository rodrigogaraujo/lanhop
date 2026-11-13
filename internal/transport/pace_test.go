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
