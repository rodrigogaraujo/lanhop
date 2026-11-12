package macos

import (
	"slices"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/event"
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
	got := translated(newTranslator(keymap.Translator{}),
		Observation{Type: TypeKeyDown, KeyCode: macOSKeyA},
		Observation{Type: TypeKeyUp, KeyCode: macOSKeyA},
	)

	expectTranslated(t, got, press(linuxKeyA), event.Sync(), release(linuxKeyA), event.Sync())
}

func TestAutomaticRepeatsAndKeysWithoutACounterpartAreDropped(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{}),
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
		got := translated(newTranslator(keymap.Translator{}),
			Observation{Type: TypeFlagsChanged, KeyCode: macOSKey, Flags: check.flag},
			Observation{Type: TypeFlagsChanged, KeyCode: macOSKey},
		)

		expectTranslated(t, got, press(check.linuxKey), event.Sync(), release(check.linuxKey), event.Sync())
	}
}

func TestModifiersOfBothSidesAreToldApartByTheirSideBits(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{}),
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
	got := translated(newTranslator(keymap.Translator{}),
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
	got := translated(newTranslator(keymap.Translator{}),
		Observation{Type: TypeFlagsChanged, KeyCode: macOSShiftLeft, Flags: FlagShift | sideOfLeftShift},
		Observation{Type: TypeFlagsChanged, KeyCode: macOSShiftLeft, Flags: FlagShift | sideOfLeftShift},
		Observation{Type: TypeFlagsChanged, KeyCode: macOSShiftRight},
	)

	expectTranslated(t, got, press(linuxShiftLeft), event.Sync())
}

func TestCapsLockIsTappedOnEveryChange(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{}), Observation{Type: TypeFlagsChanged, KeyCode: macOSCapsLock, Flags: 0x10000})

	expectTranslated(t, got, press(linuxCapsLock), event.Sync(), release(linuxCapsLock), event.Sync())
}

func TestSwappedAltAndSuperAreSwappedWhenCapturingToo(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{SwapAltAndSuper: true}),
		Observation{Type: TypeFlagsChanged, KeyCode: macOSCommandLeft, Flags: FlagCommand},
	)

	expectTranslated(t, got, press(linuxLeftAlt), event.Sync())
}

func TestMovementAndDragsBecomeRelativeMovement(t *testing.T) {
	for _, kind := range []uint32{TypeMouseMoved, TypeLeftMouseDragged, TypeRightMouseDragged, TypeOtherMouseDragged} {
		got := translated(newTranslator(keymap.Translator{}),
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
		got := translated(newTranslator(keymap.Translator{}),
			Observation{Type: c.down, Button: c.number},
			Observation{Type: c.down, Button: c.number},
			Observation{Type: c.up, Button: c.number},
			Observation{Type: c.up, Button: c.number},
		)

		expectTranslated(t, got, press(c.code), event.Sync(), release(c.code), event.Sync())
	}
}

func TestButtonsBeyondTheLinuxRangeAreDropped(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{}),
		Observation{Type: TypeOtherMouseDown, Button: 8},
		Observation{Type: TypeOtherMouseDown, Button: -1},
	)

	expectTranslated(t, got)
}

func TestWheelLinesKeepTheirDirectionAndHorizontalIsMirrored(t *testing.T) {
	got := translated(newTranslator(keymap.Translator{}),
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
	translator := newTranslator(keymap.Translator{})

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

		translator := newTranslator(keymap.Translator{})
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
