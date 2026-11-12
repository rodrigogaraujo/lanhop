package macos

import (
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/hotkey"
	"github.com/rodrigogaraujo/lanhop/internal/keymap"
)

const (
	sideOfLeftShift  Flags  = 0x0002
	sideOfRightShift Flags  = 0x0004
	macOSFunction    uint16 = 0x3F
	linuxCapsLock    uint16 = 58
)

func translated(translator *translator, observations ...Observation) []event.Event {
	var events []event.Event
	for _, seen := range observations {
		events = append(events, translator.translate(seen)...)
	}
	return events
}

func expectTranslated(t *testing.T, got []event.Event, want ...event.Event) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("translated to %+v, want %+v", got, want)
	}
}

func TestKeyDownAndUpBecomeAPressAndAReleaseOfTheLinuxKey(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{}, nil),
		Observation{Type: TypeKeyDown, KeyCode: macOSKeyA},
		Observation{Type: TypeKeyUp, KeyCode: macOSKeyA},
	)

	expectTranslated(t, got, press(linuxKeyA), event.Sync(), release(linuxKeyA), event.Sync())
}

func TestAutomaticRepeatsAndKeysWithoutACounterpartAreDropped(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{}, nil),
		Observation{Type: TypeKeyDown, KeyCode: macOSKeyA, Autorepeat: true},
		Observation{Type: TypeKeyDown, KeyCode: macOSFunction},
		Observation{Type: TypeFlagsChanged, KeyCode: macOSFunction, Flags: 0x800000},
	)

	expectTranslated(t, got)
}

func TestEachModifierBecomesItsLinuxKey(t *testing.T) {
	for macOSKey, check := range map[uint16]struct {
		flag     Flags
		linuxKey uint16
	}{
		0x3B: {FlagControl, linuxLeftCtrl},
		0x3E: {FlagControl, linuxRightCtrl},
		0x3A: {FlagOption, linuxLeftAlt},
		0x3D: {FlagOption, linuxRightAlt},
		0x37: {FlagCommand, linuxLeftSuper},
		0x36: {FlagCommand, linuxRightSuper},
		0x38: {FlagShift, linuxShiftLeft},
		0x3C: {FlagShift, linuxShiftRight},
	} {
		got := translated(newTranslator(keymap.Translator{}, nil),
			Observation{Type: TypeFlagsChanged, KeyCode: macOSKey, Flags: check.flag},
			Observation{Type: TypeFlagsChanged, KeyCode: macOSKey},
		)

		expectTranslated(t, got, press(check.linuxKey), event.Sync(), release(check.linuxKey), event.Sync())
	}
}

func TestModifiersOfBothSidesAreToldApartByTheirSideBits(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{}, nil),
		Observation{Type: TypeFlagsChanged, KeyCode: macOSShiftLeft, Flags: FlagShift | sideOfLeftShift},
		Observation{Type: TypeFlagsChanged, KeyCode: macOSShiftRight, Flags: FlagShift | sideOfLeftShift | sideOfRightShift},
		Observation{Type: TypeFlagsChanged, KeyCode: macOSShiftLeft, Flags: FlagShift | sideOfRightShift},
		Observation{Type: TypeFlagsChanged, KeyCode: macOSShiftRight},
	)

	expectTranslated(t, got,
		press(linuxShiftLeft), event.Sync(),
		press(linuxShiftRight), event.Sync(),
		release(linuxShiftLeft), event.Sync(),
		release(linuxShiftRight), event.Sync(),
	)
}

func TestWithoutSideBitsAChangeOfAHeldModifierIsItsRelease(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{}, nil),
		Observation{Type: TypeFlagsChanged, KeyCode: macOSShiftLeft, Flags: FlagShift},
		Observation{Type: TypeFlagsChanged, KeyCode: macOSShiftRight, Flags: FlagShift},
		Observation{Type: TypeFlagsChanged, KeyCode: macOSShiftLeft, Flags: FlagShift},
		Observation{Type: TypeFlagsChanged, KeyCode: macOSShiftRight},
	)

	expectTranslated(t, got,
		press(linuxShiftLeft), event.Sync(),
		press(linuxShiftRight), event.Sync(),
		release(linuxShiftLeft), event.Sync(),
		release(linuxShiftRight), event.Sync(),
	)
}

func TestModifierChangeThatChangesNothingIsDropped(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{}, nil),
		Observation{Type: TypeFlagsChanged, KeyCode: macOSShiftLeft, Flags: FlagShift | sideOfLeftShift},
		Observation{Type: TypeFlagsChanged, KeyCode: macOSShiftLeft, Flags: FlagShift | sideOfLeftShift},
		Observation{Type: TypeFlagsChanged, KeyCode: macOSFunction, Flags: FlagShift | sideOfLeftShift},
	)

	expectTranslated(t, got, press(linuxShiftLeft), event.Sync())
}

func TestCapsLockIsTappedOnEveryChange(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{}, nil), Observation{Type: TypeFlagsChanged, KeyCode: macOSCapsLock, Flags: 0x10000})

	expectTranslated(t, got, press(linuxCapsLock), event.Sync(), release(linuxCapsLock), event.Sync())
}

func TestSwappedAltAndSuperAreSwappedWhenCapturingToo(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{SwapAltAndSuper: true}, nil),
		Observation{Type: TypeFlagsChanged, KeyCode: macOSCommandLeft, Flags: FlagCommand},
	)

	expectTranslated(t, got, press(linuxLeftAlt), event.Sync())
}

func TestMovementAndDragsBecomeRelativeMovement(t *testing.T) {
	for _, kind := range []uint32{TypeMouseMoved, TypeLeftMouseDragged, TypeRightMouseDragged, TypeOtherMouseDragged} {
		got := translated(newTranslator(keymap.Translator{}, nil),
			Observation{Type: kind, DX: 3, DY: -2},
			Observation{Type: kind, DX: 4},
			Observation{Type: kind, DY: 5},
			Observation{Type: kind},
		)

		expectTranslated(t, got,
			event.Relative(event.RelX, 3), event.Relative(event.RelY, -2), event.Sync(),
			event.Relative(event.RelX, 4), event.Sync(),
			event.Relative(event.RelY, 5), event.Sync(),
		)
	}
}

func TestButtonsBecomeTheLinuxButtonOfTheSameNumber(t *testing.T) {
	cases := []struct {
		down, up uint32
		number   int64
		code     uint16
	}{
		{TypeLeftMouseDown, TypeLeftMouseUp, 0, event.ButtonLeft},
		{TypeRightMouseDown, TypeRightMouseUp, 1, event.ButtonRight},
		{TypeOtherMouseDown, TypeOtherMouseUp, 2, event.ButtonMiddle},
		{TypeOtherMouseDown, TypeOtherMouseUp, 3, linuxButtonSide},
		{TypeOtherMouseDown, TypeOtherMouseUp, 7, linuxButtonTask},
	}
	for _, c := range cases {
		got := translated(newTranslator(keymap.Translator{}, nil),
			Observation{Type: c.down, Button: c.number},
			Observation{Type: c.down, Button: c.number},
			Observation{Type: c.up, Button: c.number},
			Observation{Type: c.up, Button: c.number},
		)

		expectTranslated(t, got,
			press(c.code), event.Sync(),
			release(c.code), event.Sync(), press(c.code), event.Sync(),
			release(c.code), event.Sync(),
		)
	}
}

func TestButtonsBeyondTheLinuxRangeAreDropped(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{}, nil),
		Observation{Type: TypeOtherMouseDown, Button: 8},
		Observation{Type: TypeOtherMouseDown, Button: -1},
	)

	expectTranslated(t, got)
}

func TestWheelLinesKeepTheirDirectionAndHorizontalIsMirrored(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{}, nil),
		Observation{Type: TypeScrollWheel, ScrollVertical: 1},
		Observation{Type: TypeScrollWheel, ScrollVertical: -2, ScrollHorizontal: 1},
		Observation{Type: TypeScrollWheel, ScrollHorizontal: -1},
		Observation{Type: TypeScrollWheel},
	)

	expectTranslated(t, got,
		event.Relative(event.RelWheel, 1), event.Sync(),
		event.Relative(event.RelWheel, -2), event.Relative(event.RelHWheel, -1), event.Sync(),
		event.Relative(event.RelHWheel, 1), event.Sync(),
	)
}

func TestSmoothScrollingAddsUpToWholeLines(t *testing.T) {
	smooth := func(vertical, horizontal float64) Observation {
		return Observation{Type: TypeScrollWheel, Continuous: true, PointsVertical: vertical, PointsHorizontal: horizontal, ScrollVertical: 9, ScrollHorizontal: 9}
	}
	translator := newTranslator(keymap.Translator{}, nil)

	expectTranslated(t, translated(translator, smooth(4, 0), smooth(4, 0)))
	expectTranslated(t, translated(translator, smooth(4, 0)), event.Relative(event.RelWheel, 1), event.Sync())
	expectTranslated(t, translated(translator, smooth(8, 0)), event.Relative(event.RelWheel, 1), event.Sync())
	expectTranslated(t, translated(translator, smooth(-25, 0)), event.Relative(event.RelWheel, -2), event.Sync())
	expectTranslated(t, translated(translator, smooth(0, 30)), event.Relative(event.RelHWheel, -3), event.Sync())
}

func TestWhatTheReplayPostsTranslatesBackToTheSameKeys(t *testing.T) {
	for linuxKey := event.KeyFirst; linuxKey <= event.KeyLast; linuxKey++ {
		if _, mapped := (keymap.Translator{}).ToMacOS(linuxKey); !mapped || linuxKey == linuxCapsLock {
			continue
		}
		screen := oneDisplay()
		replay, _ := newReplayOn(screen)
		replay.Emit(press(linuxKey), release(linuxKey))

		translator := newTranslator(keymap.Translator{}, nil)
		var got []event.Event
		for _, posted := range screen.posted {
			key := posted.(KeyPress)
			seen := Observation{Type: TypeKeyUp, KeyCode: key.Code, Flags: key.Flags}
			if key.Modifier {
				seen.Type = TypeFlagsChanged
			} else if key.Down {
				seen.Type = TypeKeyDown
			}
			got = append(got, translator.translate(seen)...)
		}

		expectTranslated(t, got, press(linuxKey), event.Sync(), release(linuxKey), event.Sync())
	}
}

type fakeHardware struct {
	keysDown    map[uint16]bool
	buttonsDown map[int]bool
}

func (h fakeHardware) KeyDown(macOSKey uint16) bool {
	return h.keysDown[macOSKey]
}

func (h fakeHardware) ButtonDown(number int) bool {
	return h.buttonsDown[number]
}

func TestEveryModifierIsToldApartByItsOwnSideBit(t *testing.T) {
	for _, check := range map[uint16]struct {
		flag, side Flags
		linuxKey   uint16
	}{
		0x3B: {FlagControl, 0x0001, linuxLeftCtrl},
		0x38: {FlagShift, 0x0002, linuxShiftLeft},
		0x3C: {FlagShift, 0x0004, linuxShiftRight},
		0x37: {FlagCommand, 0x0008, linuxLeftSuper},
		0x36: {FlagCommand, 0x0010, linuxRightSuper},
		0x3A: {FlagOption, 0x0020, linuxLeftAlt},
		0x3D: {FlagOption, 0x0040, linuxRightAlt},
		0x3E: {FlagControl, 0x2000, linuxRightCtrl},
	} {
		got := translated(newTranslator(keymap.Translator{}, nil),
			Observation{Type: TypeFlagsChanged, KeyCode: macOSFunction, Flags: check.flag | check.side},
			Observation{Type: TypeFlagsChanged, KeyCode: macOSFunction, Flags: 0x800000},
		)

		expectTranslated(t, got, press(check.linuxKey), event.Sync(), release(check.linuxKey), event.Sync())
	}
}

func TestSideBitsOfOtherModifiersDoNotPressAModifier(t *testing.T) {
	everySideBitButLeftShift := sideBitsOfAnyKey &^ sideOfLeftShift
	got := translated(newTranslator(keymap.Translator{}, nil),
		Observation{Type: TypeKeyDown, KeyCode: macOSKeyA, Flags: FlagShift | everySideBitButLeftShift},
	)

	if slices.Contains(got, press(linuxShiftLeft)) {
		t.Errorf("left Shift was pressed without its side bit: %+v", got)
	}
	if !slices.Contains(got, press(linuxShiftRight)) {
		t.Errorf("right Shift was not pressed although its side bit was set: %+v", got)
	}
}

func TestModifierWhoseReleaseWasLostIsReleasedByTheNextEventOfAnyKind(t *testing.T) {
	for name, next := range map[string]Observation{
		"a key":        {Type: TypeKeyDown, KeyCode: macOSKeyA},
		"a movement":   {Type: TypeMouseMoved, DX: 1},
		"a button":     {Type: TypeLeftMouseDown},
		"a scroll":     {Type: TypeScrollWheel, ScrollVertical: 1},
		"another flag": {Type: TypeFlagsChanged, KeyCode: macOSFunction, Flags: 0x800000},
	} {
		translator := newTranslator(keymap.Translator{}, nil)
		translated(translator, Observation{Type: TypeFlagsChanged, KeyCode: macOSShiftLeft, Flags: FlagShift | sideOfLeftShift})

		got := translated(translator, next)

		if len(got) < 2 || got[0] != release(linuxShiftLeft) {
			t.Errorf("after %s the translation was %+v, want it to start by releasing left Shift", name, got)
		}
	}
}

func TestModifierWhosePressWasLostIsPressedWhenItsSideBitShowsUp(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{}, nil),
		Observation{Type: TypeKeyDown, KeyCode: macOSKeyA, Flags: FlagShift | sideOfLeftShift},
	)

	expectTranslated(t, got, press(linuxShiftLeft), event.Sync(), press(linuxKeyA), event.Sync())
}

func TestKeyWhoseReleaseWasLostIsReleasedBeforeItsNextPress(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{}, nil),
		Observation{Type: TypeKeyDown, KeyCode: macOSKeyA},
		Observation{Type: TypeKeyDown, KeyCode: macOSKeyA},
		Observation{Type: TypeKeyUp, KeyCode: macOSKeyA},
		Observation{Type: TypeKeyUp, KeyCode: macOSKeyA},
	)

	expectTranslated(t, got,
		press(linuxKeyA), event.Sync(),
		release(linuxKeyA), event.Sync(), press(linuxKeyA), event.Sync(),
		release(linuxKeyA), event.Sync(),
	)
}

func TestPressingAModifierReleasesWhatTheHardwareNoLongerHolds(t *testing.T) {
	const macOSKeyS uint16 = 0x01
	const linuxKeyS uint16 = 31
	hardware := fakeHardware{keysDown: map[uint16]bool{macOSKeyS: true}, buttonsDown: map[int]bool{1: true}}
	translator := newTranslator(keymap.Translator{}, hardware)
	translated(translator,
		Observation{Type: TypeKeyDown, KeyCode: macOSKeyA},
		Observation{Type: TypeKeyDown, KeyCode: macOSKeyS},
		Observation{Type: TypeLeftMouseDown, Button: 0},
		Observation{Type: TypeRightMouseDown, Button: 1},
	)

	got := translated(translator, Observation{Type: TypeFlagsChanged, KeyCode: 0x3B, Flags: FlagControl})

	expectTranslated(t, got,
		press(linuxLeftCtrl), event.Sync(),
		release(linuxKeyA), event.Sync(),
		release(event.ButtonLeft), event.Sync(),
	)
	if after := translated(translator, Observation{Type: TypeKeyUp, KeyCode: macOSKeyS, Flags: FlagControl}); !slices.Equal(after, []event.Event{release(linuxKeyS), event.Sync()}) {
		t.Errorf("the key the hardware still held was not kept: its release gave %+v", after)
	}
}

func TestReleasingAModifierDoesNotAskTheHardware(t *testing.T) {
	translator := newTranslator(keymap.Translator{}, fakeHardware{})
	translated(translator,
		Observation{Type: TypeFlagsChanged, KeyCode: 0x3B, Flags: FlagControl},
		Observation{Type: TypeKeyDown, KeyCode: macOSKeyA, Flags: FlagControl},
	)

	got := translated(translator, Observation{Type: TypeFlagsChanged, KeyCode: 0x3B})

	expectTranslated(t, got, release(linuxLeftCtrl), event.Sync())
}

func TestInputMissedByTheTapReleasesEverythingHeld(t *testing.T) {
	translator := newTranslator(keymap.Translator{}, nil)
	translated(translator,
		Observation{Type: TypeFlagsChanged, KeyCode: macOSShiftLeft, Flags: FlagShift | sideOfLeftShift},
		Observation{Type: TypeKeyDown, KeyCode: macOSKeyA, Flags: FlagShift | sideOfLeftShift},
		Observation{Type: TypeRightMouseDown, Button: 1, Flags: FlagShift | sideOfLeftShift},
	)

	got := translated(translator, Observation{Type: TypeTapDisabled})

	expectTranslated(t, got,
		release(linuxKeyA), event.Sync(),
		release(event.ButtonRight), event.Sync(),
		release(linuxShiftLeft), event.Sync(),
	)
	expectTranslated(t, translated(translator, Observation{Type: TypeTapDisabled}))
}

func TestOnlyInputThatLanhopDidNotPostIsCaptured(t *testing.T) {
	if notPostedByLanhop(Observation{Type: TypeKeyDown, Marker: Marker}) {
		t.Error("an event lanhop posted would be captured")
	}
	for _, marker := range []int64{0, 1, Marker + 1, -Marker} {
		if !notPostedByLanhop(Observation{Type: TypeKeyDown, Marker: marker}) {
			t.Errorf("an event with marker %d would be ignored", marker)
		}
	}
}

const (
	macOSLeftControl uint16 = 0x3B
	macOSLeftOption  uint16 = 0x3A
	macOSDigit1      uint16 = 0x12
	macOSDigit2      uint16 = 0x13
)

func hotkeyOnAMac(digit uint16) []Observation {
	both := FlagControl | FlagOption
	return []Observation{
		{Type: TypeFlagsChanged, KeyCode: macOSLeftControl, Flags: FlagControl},
		{Type: TypeFlagsChanged, KeyCode: macOSLeftOption, Flags: both},
		{Type: TypeKeyDown, KeyCode: digit, Flags: both},
		{Type: TypeKeyUp, KeyCode: digit, Flags: both},
		{Type: TypeFlagsChanged, KeyCode: macOSLeftOption, Flags: FlagControl},
		{Type: TypeFlagsChanged, KeyCode: macOSLeftControl},
	}
}

func machinesSwitchedTo(t *testing.T, translator *translator, observations ...Observation) []int {
	t.Helper()
	detector, err := hotkey.New(hotkey.DefaultModifiers)
	if err != nil {
		t.Fatalf("hotkey.New: %v", err)
	}
	var targets []int
	for _, captured := range translated(translator, observations...) {
		if result := detector.Feed(captured); result.Switch {
			targets = append(targets, result.Target)
		}
	}
	return targets
}

func TestHotkeyTypedOnAMacIsDetected(t *testing.T) {
	got := machinesSwitchedTo(t, newTranslator(keymap.Translator{}, nil), hotkeyOnAMac(macOSDigit2)...)

	if !slices.Equal(got, []int{2}) {
		t.Errorf("switched to %v, want machine 2", got)
	}
}

func TestHotkeyStillWorksAfterTheReleaseOfAKeyWasLost(t *testing.T) {
	cases := map[string]Observation{
		"a letter":       {Type: TypeKeyDown, KeyCode: macOSKeyA},
		"a mouse button": {Type: TypeLeftMouseDown},
		"left Shift":     {Type: TypeFlagsChanged, KeyCode: macOSShiftLeft, Flags: FlagShift | sideOfLeftShift},
	}
	for name, pressedAndNeverReleased := range cases {
		t.Run(name, func(t *testing.T) {
			nothingIsDown := fakeHardware{}
			observations := append([]Observation{pressedAndNeverReleased}, hotkeyOnAMac(macOSDigit1)...)

			got := machinesSwitchedTo(t, newTranslator(keymap.Translator{}, nothingIsDown), observations...)

			if !slices.Equal(got, []int{1}) {
				t.Errorf("switched to %v, want the hotkey to bring the input back to machine 1", got)
			}
		})
	}
}

type pumpUnderTest struct {
	pump      *pump
	observed  chan Observation
	lostInput chan struct{}
	stop      chan struct{}
	allowed   atomic.Bool
	gaveBack  atomic.Int32
}

func startPump(handOffLimit time.Duration) *pumpUnderTest {
	under := &pumpUnderTest{
		observed:  make(chan Observation, 16),
		lostInput: make(chan struct{}, 1),
		stop:      make(chan struct{}),
	}
	under.allowed.Store(true)
	under.pump = &pump{
		observed:     under.observed,
		lostInput:    under.lostInput,
		stop:         under.stop,
		accepts:      notPostedByLanhop,
		translator:   newTranslator(keymap.Translator{}, nil),
		events:       make(chan event.Event),
		handOffLimit: handOffLimit,
		checkEvery:   5 * time.Millisecond,
		stillAllowed: under.allowed.Load,
		giveBack:     func() { under.gaveBack.Add(1) },
	}
	go under.pump.run()
	return under
}

func (u *pumpUnderTest) next(t *testing.T) (event.Event, bool) {
	t.Helper()
	select {
	case delivered, open := <-u.pump.events:
		return delivered, open
	case <-time.After(5 * time.Second):
		t.Fatal("the pump neither delivered an event nor ended")
		return event.Event{}, false
	}
}

func (u *pumpUnderTest) expectEnded(t *testing.T, gaveBack int32) {
	t.Helper()
	if delivered, open := u.next(t); open {
		t.Fatalf("the pump delivered %+v, want it to end", delivered)
	}
	if got := u.gaveBack.Load(); got != gaveBack {
		t.Errorf("the input was given back %d times, want %d", got, gaveBack)
	}
}

func TestPumpDeliversWhatItAcceptsInOrder(t *testing.T) {
	under := startPump(time.Minute)
	defer close(under.stop)
	under.observed <- Observation{Type: TypeKeyDown, KeyCode: macOSKeyA, Marker: Marker}
	under.observed <- Observation{Type: TypeKeyDown, KeyCode: macOSKeyA}
	under.observed <- Observation{Type: TypeKeyUp, KeyCode: macOSKeyA, Marker: Marker}
	under.observed <- Observation{Type: TypeKeyUp, KeyCode: macOSKeyA}

	for i, want := range []event.Event{press(linuxKeyA), event.Sync(), release(linuxKeyA), event.Sync()} {
		if got, open := under.next(t); !open || got != want {
			t.Fatalf("event %d = %+v (open %v), want %+v", i, got, open, want)
		}
	}
	if got := under.gaveBack.Load(); got != 0 {
		t.Errorf("the input was given back %d times, want 0", got)
	}
}

func TestPumpReleasesEverythingWhenTheTapMissedInput(t *testing.T) {
	under := startPump(time.Minute)
	defer close(under.stop)
	under.observed <- Observation{Type: TypeKeyDown, KeyCode: macOSKeyA}
	under.next(t)
	under.next(t)

	under.observed <- Observation{Type: TypeTapDisabled, Marker: Marker}

	if got, open := under.next(t); !open || got != release(linuxKeyA) {
		t.Errorf("after missed input the pump delivered %+v (open %v), want the release of the held key", got, open)
	}
}

func TestPumpGivesTheInputBackAndEnds(t *testing.T) {
	cases := map[string]func(*pumpUnderTest){
		"when the queue of the tap overflowed": func(under *pumpUnderTest) { under.lostInput <- struct{}{} },
		"when the permission was taken away":   func(under *pumpUnderTest) { under.allowed.Store(false) },
	}
	for name, trouble := range cases {
		t.Run(name, func(t *testing.T) {
			under := startPump(time.Minute)
			defer close(under.stop)

			trouble(under)

			under.expectEnded(t, 1)
		})
	}
}

func TestPumpGivesTheInputBackWhenNobodyTakesItsEvents(t *testing.T) {
	under := startPump(20 * time.Millisecond)
	defer close(under.stop)
	under.observed <- Observation{Type: TypeKeyDown, KeyCode: macOSKeyA}

	deadline := time.Now().Add(5 * time.Second)
	for under.gaveBack.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	under.expectEnded(t, 1)
}

func TestPumpEndsWithoutGivingBackWhenItIsStoppedOrItsSourceEnds(t *testing.T) {
	cases := map[string]func(*pumpUnderTest){
		"stopped":      func(under *pumpUnderTest) { close(under.stop) },
		"source ended": func(under *pumpUnderTest) { close(under.observed) },
	}
	for name, end := range cases {
		t.Run(name, func(t *testing.T) {
			under := startPump(time.Minute)

			end(under)

			under.expectEnded(t, 0)
			if name != "stopped" {
				close(under.stop)
			}
		})
	}
}
