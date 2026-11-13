package clipshare

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const (
	pasteReadyWithin     = 300 * time.Millisecond
	keyLateByAtMost      = 50 * time.Millisecond
	betweenKeys          = 10 * time.Millisecond
	keysWithoutACopy     = 20
	keysBehindTheCopy    = 20
	sixtyFourKiB         = 64 << 10
	roundsOfEachCrossing = 3
)

func measuredWithoutTheRaceDetector(t *testing.T) {
	t.Helper()
	if raceDetectorOn {
		t.Skip("the race detector slows every frame down, so the timings of SC-001 and SC-002 are measured without it")
	}
}

func textOfLength(length int, words string) string {
	return strings.Repeat(words+" ", length/(len(words)+1)+1)[:length]
}

func delaysOfKeys(t *testing.T, side *sideUnderTest, typed []time.Time, pressedBefore int) []time.Duration {
	t.Helper()
	var presses []time.Time
	eventually(t, "every key reaching "+side.name, func() bool {
		presses = side.replay.pressesOf(event.KeyA)
		return len(presses) >= pressedBefore+len(typed)
	})
	delays := make([]time.Duration, len(typed))
	for i, at := range typed {
		delays[i] = presses[pressedBefore+i].Sub(at)
	}
	return delays
}

func median(durations []time.Duration) time.Duration {
	sorted := slices.Clone(durations)
	slices.Sort(sorted)
	return sorted[len(sorted)/2]
}

func lastWriteOf(t *testing.T, board *fakeClipboard, text string) time.Time {
	t.Helper()
	writes := board.written()
	if len(writes) == 0 || writes[len(writes)-1].text != text {
		t.Fatalf("the last write is not the copy that was awaited")
	}
	return writes[len(writes)-1].at
}

func TestKeysArriveAtMost50MillisecondsLaterWhileA1MiBCopyTravelsOverA20MegabitLink(t *testing.T) {
	measuredWithoutTheRaceDetector(t)
	d := newDesk(t, "right")
	right := d.sideJoinsOverASlowLink("right")
	d.waitUntilTakingPart(right)

	d.focusOn(right.number)
	var typedWithoutACopy []time.Time
	for range keysWithoutACopy {
		typedWithoutACopy = append(typedWithoutACopy, d.types())
		time.Sleep(betweenKeys)
	}
	usual := median(delaysOfKeys(t, right, typedWithoutACopy, 0))
	d.focusOn(thisMachine)

	large := textOfLength(clipboard.Limit, "a copy of one mebibyte")
	d.clipboard.personCopies(large)
	crossed := d.focusOn(right.number)
	var typedDuringTheCopy []time.Time
	for right.clipboard.holds() != large {
		if time.Since(crossed) > testPatience {
			t.Fatal("the 1 MiB copy never reached the right")
		}
		typedDuringTheCopy = append(typedDuringTheCopy, d.types())
		time.Sleep(betweenKeys)
	}
	written := lastWriteOf(t, right.clipboard, large)
	delays := delaysOfKeys(t, right, typedDuringTheCopy, keysWithoutACopy)

	var whileTravelling []time.Duration
	for i, at := range typedDuringTheCopy {
		if at.Before(written) {
			whileTravelling = append(whileTravelling, delays[i])
		}
	}
	if len(whileTravelling) < keysBehindTheCopy {
		t.Fatalf("only %d keys were typed while the copy travelled, want at least %d", len(whileTravelling), keysBehindTheCopy)
	}
	worst := slices.Max(whileTravelling)
	t.Logf("SC-002: usual key delay %s; while the 1 MiB copy travelled for %s, %d keys, median %s, worst %s, %s later than usual",
		usual, written.Sub(crossed).Round(time.Millisecond), len(whileTravelling), median(whileTravelling), worst, worst-usual)
	if worst-usual > keyLateByAtMost {
		t.Errorf("a key arrived %s later than usual while the copy travelled, want at most %s", worst-usual, keyLateByAtMost)
	}
}

func TestA64KiBCopyIsReadyToPasteWithin300MillisecondsAfterFocusArrivesOverA20MegabitLink(t *testing.T) {
	measuredWithoutTheRaceDetector(t)
	d := newDesk(t, "left", "right")
	left, right := d.sideJoinsOverASlowLink("left"), d.sideJoinsOverASlowLink("right")
	d.waitUntilTakingPart(left, right)
	var tookFromTheCentral, tookFromASideMachine []time.Duration

	for round := range roundsOfEachCrossing {
		text := textOfLength(sixtyFourKiB, fmt.Sprintf("from the central, round %d", round))
		d.clipboard.personCopies(text)
		crossed := d.focusOn(right.number)
		waitUntilItHolds(t, "right", right.clipboard, text)
		tookFromTheCentral = append(tookFromTheCentral, lastWriteOf(t, right.clipboard, text).Sub(crossed))
		d.focusOn(thisMachine)
	}
	lastOnTheDesk := d.clipboard.holds()
	for round := range roundsOfEachCrossing {
		d.focusOn(left.number)
		waitUntilItHolds(t, "left, with the last copy of the desk", left.clipboard, lastOnTheDesk)
		text := textOfLength(sixtyFourKiB, fmt.Sprintf("from the left, round %d", round))
		left.clipboard.personCopies(text)
		crossed := d.focusOn(right.number)
		waitUntilItHolds(t, "right", right.clipboard, text)
		tookFromASideMachine = append(tookFromASideMachine, lastWriteOf(t, right.clipboard, text).Sub(crossed))
		lastOnTheDesk = text
	}

	t.Logf("SC-001: from the hotkey to the write of 64 KiB: central to side machine %v, side machine to side machine through the central %v",
		tookFromTheCentral, tookFromASideMachine)
	for _, took := range slices.Concat(tookFromTheCentral, tookFromASideMachine) {
		if took > pasteReadyWithin {
			t.Errorf("a 64 KiB copy was written %s after focus moved, want under %s", took, pasteReadyWithin)
		}
	}
}
