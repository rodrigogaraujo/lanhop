package clipshare

import (
	"slices"
	"testing"
	"time"
)

type clipboardThatComesAndGoes struct {
	start     time.Time
	clock     time.Time
	reachable bool
	attempts  []time.Duration
}

func (c *clipboardThatComesAndGoes) open() (Clipboard, error) {
	c.attempts = append(c.attempts, c.clock.Sub(c.start))
	if !c.reachable {
		return nil, ErrUnreachable
	}
	return holding("the clipboard"), nil
}

func (c *clipboardThatComesAndGoes) attemptsSince(from int, k *keeper, until int) []time.Duration {
	c.attempts = nil
	for second := from; second <= until; second++ {
		c.clock = c.start.Add(time.Duration(second) * time.Second)
		k.reachIfDue()
	}
	return c.attempts
}

func seconds(values ...int) []time.Duration {
	durations := make([]time.Duration, len(values))
	for i, value := range values {
		durations[i] = time.Duration(value) * time.Second
	}
	return durations
}

func TestAClipboardThatCannotBeReachedIsTriedAfter1SecondThenTwiceAsLongUpTo30Seconds(t *testing.T) {
	board := &clipboardThatComesAndGoes{start: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)}
	var reached []bool
	k := newKeeper(Config{Open: board.open}.withDefaults(), func(reachable bool) { reached = append(reached, reachable) })
	k.now = func() time.Time { return board.clock }

	if got, want := board.attemptsSince(0, k, 120), seconds(0, 1, 3, 7, 15, 31, 61, 91); !slices.Equal(got, want) {
		t.Errorf("attempts while the clipboard stays away came at %v, want %v", got, want)
	}

	board.reachable = true
	if got, want := board.attemptsSince(121, k, 125), seconds(121); !slices.Equal(got, want) || !slices.Equal(reached, []bool{true}) {
		t.Errorf("attempts once the clipboard is back came at %v and reached it %v, want %v and once", got, reached, want)
	}

	k.lose()
	board.reachable = false
	if got, want := board.attemptsSince(130, k, 134), seconds(130, 131, 133); !slices.Equal(got, want) {
		t.Errorf("attempts after the clipboard was lost again came at %v, want %v, at once and then from 1 second again", got, want)
	}
}
