package macos

import (
	"math"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/keymap"
	"github.com/rodrigogaraujo/lanhop/internal/keystate"
)

const (
	linuxKeyB     uint16 = 48
	linuxCapsLock uint16 = 58
	linuxNumLock  uint16 = 69

	macOSKeyB uint16 = 0x0B

	longHold = time.Minute
)

var keysThatNeverRepeat = map[string]uint16{
	"left Shift":    linuxShiftLeft,
	"right Shift":   linuxShiftRight,
	"left Control":  linuxLeftCtrl,
	"right Control": linuxRightCtrl,
	"left Option":   linuxLeftAlt,
	"right Option":  linuxRightAlt,
	"left Command":  linuxLeftSuper,
	"right Command": linuxRightSuper,
	"Caps Lock":     linuxCapsLock,
	"Num Lock":      linuxNumLock,
}

func repeatOf(code uint16) KeyPress {
	return KeyPress{Code: code, Down: true, Repeat: true}
}

func repeatsOf(screen *fakeScreen) []KeyPress {
	var repeats []KeyPress
	for _, posted := range screen.posted {
		if key, isKey := posted.(KeyPress); isKey && key.Repeat {
			repeats = append(repeats, key)
		}
	}
	return repeats
}

func expectRepeats(t *testing.T, screen *fakeScreen, want int, when string) {
	t.Helper()
	if got := len(repeatsOf(screen)); got != want {
		t.Errorf("%s: %d repeats were posted, want %d", when, got, want)
	}
}

func TestHeldKeyRepeatsAfterTheDelayAndThenAtTheInterval(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)
	replay.Emit(press(linuxKeyA), event.Sync())

	for _, step := range []struct {
		when    string
		wait    time.Duration
		repeats int
	}{
		{"just before the delay ends", heldDelay - time.Millisecond, 0},
		{"when the delay ends", time.Millisecond, 1},
		{"just before the interval ends", heldInterval - time.Millisecond, 1},
		{"when the interval ends", time.Millisecond, 2},
		{"three intervals later", 3 * heldInterval, 5},
	} {
		clock.advance(step.wait)
		expectRepeats(t, screen, step.repeats, step.when)
	}

	expectPosted(t, screen,
		KeyPress{Code: macOSKeyA, Down: true},
		repeatOf(macOSKeyA), repeatOf(macOSKeyA), repeatOf(macOSKeyA), repeatOf(macOSKeyA), repeatOf(macOSKeyA),
	)
}

func TestReleasedKeyStopsRepeating(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)

	replay.Emit(press(linuxKeyA), event.Sync())
	clock.advance(heldDelay + heldInterval)
	replay.Emit(release(linuxKeyA), event.Sync())
	clock.advance(longHold)

	expectPosted(t, screen,
		KeyPress{Code: macOSKeyA, Down: true},
		repeatOf(macOSKeyA), repeatOf(macOSKeyA),
		KeyPress{Code: macOSKeyA, Down: false},
	)
}

func TestOnlyTheKeyPressedLastRepeatsAndItsDelayStartsWhenItIsPressed(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)

	replay.Emit(press(linuxKeyA), event.Sync())
	clock.advance(heldDelay - time.Millisecond)
	replay.Emit(press(linuxKeyB), event.Sync())
	clock.advance(heldDelay - time.Millisecond)
	expectRepeats(t, screen, 0, "before the delay of the second key ends")
	clock.advance(time.Millisecond + heldInterval)

	expectPosted(t, screen,
		KeyPress{Code: macOSKeyA, Down: true},
		KeyPress{Code: macOSKeyB, Down: true},
		repeatOf(macOSKeyB), repeatOf(macOSKeyB),
	)
}

func TestReleasingAKeyThatIsNotRepeatingKeepsTheRepeat(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)

	replay.Emit(press(linuxKeyA), press(linuxKeyB), release(linuxKeyA), event.Sync())
	clock.advance(heldDelay + heldInterval)

	expectPosted(t, screen,
		KeyPress{Code: macOSKeyA, Down: true},
		KeyPress{Code: macOSKeyB, Down: true},
		KeyPress{Code: macOSKeyA, Down: false},
		repeatOf(macOSKeyB), repeatOf(macOSKeyB),
	)
}

func TestReleasingTheRepeatingKeyDoesNotMoveTheRepeatToAnotherHeldKey(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)

	replay.Emit(press(linuxKeyA), press(linuxKeyB), event.Sync())
	clock.advance(heldDelay)
	replay.Emit(release(linuxKeyB), event.Sync())
	clock.advance(longHold)

	expectPosted(t, screen,
		KeyPress{Code: macOSKeyA, Down: true},
		KeyPress{Code: macOSKeyB, Down: true},
		repeatOf(macOSKeyB),
		KeyPress{Code: macOSKeyB, Down: false},
	)
}

func TestModifiersCapsLockAndNumLockNeverRepeat(t *testing.T) {
	for name, key := range keysThatNeverRepeat {
		t.Run(name, func(t *testing.T) {
			screen := oneDisplay()
			replay, clock := newReplayOn(screen)

			replay.Emit(press(key), event.Sync())
			clock.advance(longHold)

			if len(screen.posted) != 1 {
				t.Errorf("holding the key posted %+v, want only its press", screen.posted)
			}
		})
	}
}

func TestKeyThatNeverRepeatsPressedAndReleasedWhileAKeyRepeatsLeavesTheRepeat(t *testing.T) {
	for name, key := range keysThatNeverRepeat {
		t.Run(name, func(t *testing.T) {
			screen := oneDisplay()
			replay, clock := newReplayOn(screen)

			replay.Emit(press(linuxKeyA), event.Sync())
			clock.advance(heldDelay)
			replay.Emit(press(key), event.Sync())
			clock.advance(heldInterval)
			expectRepeats(t, screen, 2, "after the other key was pressed")
			replay.Emit(release(key), event.Sync())
			clock.advance(heldInterval)
			expectRepeats(t, screen, 3, "after the other key was released")

			for _, repeated := range repeatsOf(screen) {
				if repeated.Code != macOSKeyA {
					t.Errorf("key %#x repeated, want only the letter", repeated.Code)
				}
			}
		})
	}
}

func TestRepeatsCarryTheModifiersHeldWhenTheyArePosted(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)

	replay.Emit(press(linuxKeyA), event.Sync())
	clock.advance(heldDelay)
	for _, change := range []event.Event{press(linuxShiftLeft), press(linuxLeftSuper), release(linuxShiftLeft), release(linuxLeftSuper)} {
		replay.Emit(change, event.Sync())
		clock.advance(heldInterval)
	}

	var carried []Flags
	for _, repeated := range repeatsOf(screen) {
		carried = append(carried, repeated.Flags)
	}
	want := []Flags{0, FlagShift, FlagShift | FlagCommand, FlagCommand, 0}
	if len(carried) != len(want) {
		t.Fatalf("repeats carried %v, want %v", carried, want)
	}
	for i := range want {
		if carried[i] != want[i] {
			t.Errorf("repeat %d carried %#x, want %#x", i, uint64(carried[i]), uint64(want[i]))
		}
	}
}

func TestMouseButtonsDoNotRepeat(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)

	replay.Emit(press(event.ButtonLeft), event.Sync())
	clock.advance(longHold)

	expectPosted(t, screen, MouseEvent{Action: MouseDown, At: Point{X: 50, Y: 50}, Clicks: 1})
}

func TestClickWhileAKeyRepeatsLeavesTheRepeat(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)

	replay.Emit(press(linuxKeyA), event.Sync())
	clock.advance(heldDelay)
	replay.Emit(press(event.ButtonLeft), event.Sync(), release(event.ButtonLeft), event.Sync())
	clock.advance(heldInterval)

	expectPosted(t, screen,
		KeyPress{Code: macOSKeyA, Down: true},
		repeatOf(macOSKeyA),
		MouseEvent{Action: MouseDown, At: Point{X: 50, Y: 50}, Clicks: 1},
		MouseEvent{Action: MouseUp, At: Point{X: 50, Y: 50}, Clicks: 1},
		repeatOf(macOSKeyA),
	)
}

func TestRepeatReceivedFromTheNetworkIsNotPosted(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(press(linuxKeyA), event.Sync(), event.Key(linuxKeyA, event.KeyRepeated), event.Sync())

	expectPosted(t, screen, KeyPress{Code: macOSKeyA, Down: true})
}

func TestKeyEventThatIsNeitherAPressNorAReleaseIsNotPostedAndStartsNoRepeat(t *testing.T) {
	for name, value := range map[string]int32{
		"a value above the repeat": 3,
		"a negative value":         -1,
		"the largest value":        math.MaxInt32,
	} {
		t.Run(name, func(t *testing.T) {
			screen := oneDisplay()
			replay, clock := newReplayOn(screen)

			replay.Emit(event.Key(linuxKeyA, value), event.Sync())
			clock.advance(longHold)

			expectPosted(t, screen)
		})
	}
}

func TestKeyEventThatIsNeitherAPressNorAReleaseLeavesTheRepeatOfAHeldKey(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)

	replay.Emit(press(linuxKeyA), event.Sync())
	clock.advance(heldDelay)
	replay.Emit(event.Key(linuxKeyB, 3), event.Sync())
	clock.advance(heldInterval)

	expectPosted(t, screen, KeyPress{Code: macOSKeyA, Down: true}, repeatOf(macOSKeyA), repeatOf(macOSKeyA))
}

func TestRepeatReceivedFromTheNetworkDoesNotPostponeTheRepeat(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)

	replay.Emit(press(linuxKeyA), event.Sync())
	clock.advance(heldDelay - time.Millisecond)
	replay.Emit(event.Key(linuxKeyA, event.KeyRepeated), event.Sync())
	clock.advance(time.Millisecond)

	expectPosted(t, screen, KeyPress{Code: macOSKeyA, Down: true}, repeatOf(macOSKeyA))
}

func TestRepeatTimingFollowsWhatTheMacReports(t *testing.T) {
	for _, c := range []struct {
		name                    string
		delay, interval         time.Duration
		known                   bool
		wantDelay, wantInterval time.Duration
		wantRepeats             bool
	}{
		{"uses the delay and the interval of the Mac", 250 * time.Millisecond, 33 * time.Millisecond, true, 250 * time.Millisecond, 33 * time.Millisecond, true},
		{"uses the defaults of macOS when the Mac reports nothing", 0, 0, false, 500 * time.Millisecond, time.Second / 12, true},
		{"does not repeat when the delay is zero", 0, 33 * time.Millisecond, true, 0, 0, false},
		{"does not repeat when the interval is zero", 250 * time.Millisecond, 0, true, 0, 0, false},
		{"does not repeat faster than macOS does", time.Millisecond, 2 * time.Millisecond, true, 16 * time.Millisecond, 16 * time.Millisecond, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			delay, interval, repeats := repeatTiming(c.delay, c.interval, c.known)

			if delay != c.wantDelay || interval != c.wantInterval || repeats != c.wantRepeats {
				t.Errorf("repeatTiming(%v, %v, %v) = %v, %v, %v, want %v, %v, %v",
					c.delay, c.interval, c.known, delay, interval, repeats, c.wantDelay, c.wantInterval, c.wantRepeats)
			}
		})
	}
}

func TestKeyDoesNotRepeatWhenKeyRepeatIsTurnedOff(t *testing.T) {
	for name, turnOff := range map[string]func(*fakeScreen){
		"no delay":    func(screen *fakeScreen) { screen.repeatDelay = 0 },
		"no interval": func(screen *fakeScreen) { screen.repeatInterval = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			screen := oneDisplay()
			turnOff(screen)
			replay, clock := newReplayOn(screen)

			replay.Emit(press(linuxKeyA), event.Sync())
			clock.advance(longHold)

			expectPosted(t, screen, KeyPress{Code: macOSKeyA, Down: true})
		})
	}
}

func TestKeyPressedAfterKeyRepeatWasTurnedOffEndsTheRepeatOfTheKeyBefore(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)

	replay.Emit(press(linuxKeyA), event.Sync())
	clock.advance(heldDelay)
	screen.repeatDelay = 0
	replay.Emit(press(linuxKeyB), event.Sync())
	clock.advance(longHold)

	expectPosted(t, screen,
		KeyPress{Code: macOSKeyA, Down: true},
		repeatOf(macOSKeyA),
		KeyPress{Code: macOSKeyB, Down: true},
	)
}

func TestKeyRepeatsWithTheDefaultsOfMacOSWhenTheMacReportsNoSettings(t *testing.T) {
	screen := oneDisplay()
	screen.repeatUnknown = true
	replay, clock := newReplayOn(screen)

	replay.Emit(press(linuxKeyA), event.Sync())
	clock.advance(500*time.Millisecond - time.Millisecond)
	expectRepeats(t, screen, 0, "just before half a second")
	clock.advance(time.Millisecond)
	expectRepeats(t, screen, 1, "after half a second")
	clock.advance(time.Second)
	expectRepeats(t, screen, 13, "one second later, at twelve repeats a second")
}

func TestSettingsChangedOnTheMacApplyToTheNextKey(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)

	replay.Emit(press(linuxKeyA), event.Sync(), release(linuxKeyA), event.Sync())
	screen.repeatDelay, screen.repeatInterval = heldDelay/4, heldInterval/2
	replay.Emit(press(linuxKeyB), event.Sync())
	clock.advance(heldDelay/4 + heldInterval/2)

	expectPosted(t, screen,
		KeyPress{Code: macOSKeyA, Down: true},
		KeyPress{Code: macOSKeyA, Down: false},
		KeyPress{Code: macOSKeyB, Down: true},
		repeatOf(macOSKeyB), repeatOf(macOSKeyB),
	)
}

func TestClosingTheReplayStopsTheRepeat(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)

	replay.Emit(press(linuxKeyA), event.Sync())
	clock.advance(heldDelay)
	if err := replay.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	clock.advance(longHold)

	expectPosted(t, screen, KeyPress{Code: macOSKeyA, Down: true}, repeatOf(macOSKeyA))
}

func TestClosedReplayRefusesEventsAndPostsNothing(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)
	replay.Close()

	err := replay.Emit(press(linuxKeyA), event.Relative(event.RelX, 1), press(event.ButtonLeft), event.Sync())
	clock.advance(longHold)

	if err == nil {
		t.Error("Emit on a closed replay returned no error")
	}
	expectPosted(t, screen)
}

func TestTimerThatFiresLateDoesNotRepeatAKeyThatStoppedRepeating(t *testing.T) {
	stops := map[string]func(*Replay){
		"the key was released":    func(replay *Replay) { replay.Emit(release(linuxKeyA), event.Sync()) },
		"another key was pressed": func(replay *Replay) { replay.Emit(press(linuxKeyB), event.Sync()) },
		"the replay was closed":   func(replay *Replay) { replay.Close() },
	}
	moments := map[string]time.Duration{
		"while waiting for the delay": 0,
		"between two repeats":         heldDelay,
	}
	for stopName, stop := range stops {
		for momentName, heldFor := range moments {
			t.Run(stopName+" "+momentName, func(t *testing.T) {
				screen := oneDisplay()
				replay, clock := newReplayOn(screen)
				replay.Emit(press(linuxKeyA), event.Sync())
				clock.advance(heldFor)
				waiting := clock.waiting()
				if len(waiting) != 1 {
					t.Fatalf("%d timers were waiting for the held key, want 1", len(waiting))
				}

				stop(replay)
				postedBefore := len(screen.posted)
				waiting[0].run()

				if late := screen.posted[postedBefore:]; len(late) != 0 {
					t.Errorf("the late timer posted %+v", late)
				}
				if rearmed := clock.waiting(); stopName != "another key was pressed" && len(rearmed) != 0 {
					t.Errorf("the late timer left %d timers waiting", len(rearmed))
				}
			})
		}
	}
}

func TestReleasesSentWhenFocusIsLostStopTheRepeat(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)
	var held keystate.Tracker
	for _, pressed := range []event.Event{press(linuxShiftLeft), press(linuxKeyA), press(linuxKeyB)} {
		held.Observe(pressed)
		replay.Emit(pressed, event.Sync())
	}
	clock.advance(heldDelay)
	expectRepeats(t, screen, 1, "while the keys are held")

	replay.Emit(held.ReleaseAll()...)
	clock.advance(longHold)

	expectRepeats(t, screen, 1, "after every held key was released")
}

type racingTimer struct {
	timers *racingTimers
	run    func()
}

func (t *racingTimer) Stop() bool {
	t.timers.mu.Lock()
	defer t.timers.mu.Unlock()
	for i, waiting := range t.timers.waiting {
		if waiting == t {
			t.timers.waiting = append(t.timers.waiting[:i], t.timers.waiting[i+1:]...)
			return true
		}
	}
	return false
}

type racingTimers struct {
	mu      sync.Mutex
	waiting []*racingTimer
}

func (r *racingTimers) after(_ time.Duration, run func()) stopper {
	r.mu.Lock()
	defer r.mu.Unlock()
	timer := &racingTimer{timers: r, run: run}
	r.waiting = append(r.waiting, timer)
	return timer
}

func (r *racingTimers) fireNext() {
	r.mu.Lock()
	if len(r.waiting) == 0 {
		r.mu.Unlock()
		runtime.Gosched()
		return
	}
	next := r.waiting[0]
	r.waiting = r.waiting[1:]
	r.mu.Unlock()
	next.run()
}

func TestRepeatsNeverFollowTheReleaseWhenKeysAndTheTimerRunAtTheSameTime(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)
	timers := &racingTimers{}
	replay.after = timers.after

	typingDone := make(chan struct{})
	var firing sync.WaitGroup
	firing.Add(1)
	go func() {
		defer firing.Done()
		for {
			select {
			case <-typingDone:
				return
			default:
				timers.fireNext()
			}
		}
	}()
	for range 500 {
		for _, typed := range []event.Event{press(linuxKeyA), press(linuxKeyB), release(linuxKeyB), release(linuxKeyA)} {
			replay.Emit(typed, event.Sync())
			runtime.Gosched()
		}
	}
	close(typingDone)
	firing.Wait()
	replay.Close()

	const none = -1
	repeating := none
	for i, posted := range screen.posted {
		key := posted.(KeyPress)
		switch {
		case key.Repeat:
			if int(key.Code) != repeating {
				t.Fatalf("event %d repeats key %#x while the repeating key is %#x", i, key.Code, repeating)
			}
		case key.Down:
			repeating = int(key.Code)
		case int(key.Code) == repeating:
			repeating = none
		}
	}
}

type signallingScreen struct {
	fakeScreen
	repeated chan KeyPress
}

func (s *signallingScreen) PostKey(key KeyPress) {
	if !key.Repeat {
		return
	}
	select {
	case s.repeated <- key:
	default:
	}
}

func TestReplayMadeForTheSystemRepeatsWithoutAClockFromTheTest(t *testing.T) {
	screen := &signallingScreen{repeated: make(chan KeyPress, 1)}
	screen.repeatDelay, screen.repeatInterval = shortestRepeat, shortestRepeat
	replay := NewReplay(screen, keymap.Translator{})
	defer replay.Close()

	replay.Emit(press(linuxKeyA), event.Sync())

	for range 2 {
		select {
		case repeated := <-screen.repeated:
			if repeated != repeatOf(macOSKeyA) {
				t.Errorf("repeat = %+v, want %+v", repeated, repeatOf(macOSKeyA))
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the held key did not repeat")
		}
	}
}
