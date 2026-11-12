package macos

import (
	"slices"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/keymap"
)

const (
	linuxKeyA       uint16 = 30
	linuxLeftCtrl   uint16 = 29
	linuxShiftLeft  uint16 = 42
	linuxShiftRight uint16 = 54
	linuxLeftAlt    uint16 = 56
	linuxLeftSuper  uint16 = 125
	linuxUnknown    uint16 = 240
	linuxButtonSide uint16 = 0x113

	macOSKeyA        uint16 = 0x00
	macOSShiftLeft   uint16 = 0x38
	macOSShiftRight  uint16 = 0x3C
	macOSCommandLeft uint16 = 0x37
)

type fakeScreen struct {
	cursor   Point
	displays []Rect
	posted   []any
}

func (s *fakeScreen) PostKey(key KeyPress) {
	s.posted = append(s.posted, key)
}

func (s *fakeScreen) PostMouse(mouse MouseEvent) {
	s.posted = append(s.posted, mouse)
	s.cursor = mouse.At
}

func (s *fakeScreen) PostScroll(scroll Scroll) {
	s.posted = append(s.posted, scroll)
}

func (s *fakeScreen) Cursor() Point {
	return s.cursor
}

func (s *fakeScreen) Displays() []Rect {
	return s.displays
}

type manualClock struct {
	now time.Time
}

func (c *manualClock) advance(by time.Duration) {
	c.now = c.now.Add(by)
}

func newReplayOn(screen *fakeScreen) (*Replay, *manualClock) {
	clock := &manualClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	replay := NewReplay(screen, keymap.Translator{})
	replay.now = func() time.Time { return clock.now }
	return replay, clock
}

func oneDisplay() *fakeScreen {
	return &fakeScreen{cursor: Point{X: 50, Y: 50}, displays: []Rect{{Width: 100, Height: 100}}}
}

func press(code uint16) event.Event   { return event.Key(code, event.KeyPressed) }
func release(code uint16) event.Event { return event.Key(code, event.KeyReleased) }

func expectPosted(t *testing.T, screen *fakeScreen, want ...any) {
	t.Helper()
	if !slices.Equal(screen.posted, want) {
		t.Errorf("posted %+v, want %+v", screen.posted, want)
	}
}

func TestKeyIsTranslatedToItsMacOSCode(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(press(linuxKeyA), event.Sync(), release(linuxKeyA), event.Sync())

	expectPosted(t, screen,
		KeyPress{Code: macOSKeyA, Down: true},
		KeyPress{Code: macOSKeyA, Down: false},
	)
}

func TestHeldModifierIsCarriedByEveryLaterEvent(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(
		press(linuxShiftLeft), event.Sync(),
		press(linuxKeyA), event.Sync(),
		press(event.ButtonLeft), event.Sync(),
		event.Relative(event.RelWheel, 1), event.Sync(),
		release(linuxShiftLeft), event.Sync(),
		release(linuxKeyA), event.Sync(),
	)

	expectPosted(t, screen,
		KeyPress{Code: macOSShiftLeft, Down: true, Modifier: true, Flags: FlagShift},
		KeyPress{Code: macOSKeyA, Down: true, Flags: FlagShift},
		MouseEvent{Action: MouseDown, At: Point{X: 50, Y: 50}, Clicks: 1, Flags: FlagShift},
		Scroll{Vertical: 1, Flags: FlagShift},
		KeyPress{Code: macOSShiftLeft, Down: false, Modifier: true},
		KeyPress{Code: macOSKeyA, Down: false},
	)
}

func TestModifierStaysWhileEitherSideIsHeld(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(press(linuxShiftLeft), press(linuxShiftRight), release(linuxShiftLeft), release(linuxShiftRight))

	expectPosted(t, screen,
		KeyPress{Code: macOSShiftLeft, Down: true, Modifier: true, Flags: FlagShift},
		KeyPress{Code: macOSShiftRight, Down: true, Modifier: true, Flags: FlagShift},
		KeyPress{Code: macOSShiftLeft, Down: false, Modifier: true, Flags: FlagShift},
		KeyPress{Code: macOSShiftRight, Down: false, Modifier: true},
	)
}

func TestModifiersCombineByMeaning(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(press(linuxLeftCtrl), press(linuxLeftAlt), press(linuxLeftSuper), press(linuxKeyA))

	last := screen.posted[len(screen.posted)-1]
	if want := (KeyPress{Code: macOSKeyA, Down: true, Flags: FlagControl | FlagOption | FlagCommand}); last != want {
		t.Errorf("letter posted as %+v, want %+v", last, want)
	}
}

func TestSwappingAltAndSuperTurnsAltIntoCommand(t *testing.T) {
	screen := oneDisplay()
	replay := NewReplay(screen, keymap.Translator{SwapAltAndSuper: true})

	replay.Emit(press(linuxLeftAlt))

	expectPosted(t, screen, KeyPress{Code: macOSCommandLeft, Down: true, Modifier: true, Flags: FlagCommand})
}

func TestKeyRepeatIsPostedAsAnotherKeyDown(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(press(linuxKeyA), event.Key(linuxKeyA, event.KeyRepeated))

	expectPosted(t, screen, KeyPress{Code: macOSKeyA, Down: true}, KeyPress{Code: macOSKeyA, Down: true})
}

func TestKeyWithoutACounterpartIsNotPosted(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(press(linuxUnknown), event.Sync(), release(linuxUnknown), event.Sync())

	expectPosted(t, screen)
}

func TestMovementOfOneReportIsPostedOnceWhenTheReportEnds(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(event.Relative(event.RelX, 3), event.Relative(event.RelX, 2), event.Relative(event.RelY, -1))
	expectPosted(t, screen)

	replay.Emit(event.Sync())
	expectPosted(t, screen, MouseEvent{Action: MouseMoved, At: Point{X: 55, Y: 49}, DX: 5, DY: -1})

	replay.Emit(event.Sync())
	if len(screen.posted) != 1 {
		t.Errorf("an empty report posted something: %+v", screen.posted)
	}
}

func TestCursorStaysOnTheDisplays(t *testing.T) {
	sideBySide := []Rect{{Width: 100, Height: 100}, {X: 100, Width: 100, Height: 100}}
	cases := []struct {
		name     string
		displays []Rect
		from     Point
		dx, dy   int32
		want     Point
	}{
		{"stops at the right edge of a single display", sideBySide[:1], Point{X: 98, Y: 50}, 10, 0, Point{X: 99, Y: 50}},
		{"stops at the top left corner", sideBySide[:1], Point{X: 2, Y: 3}, -10, -10, Point{X: 0, Y: 0}},
		{"crosses to the display next to it", sideBySide, Point{X: 98, Y: 50}, 10, 0, Point{X: 108, Y: 50}},
		{"crosses back to the first display", sideBySide, Point{X: 102, Y: 50}, -10, 0, Point{X: 92, Y: 50}},
		{"stops where no display continues", sideBySide, Point{X: 150, Y: 98}, 0, 10, Point{X: 150, Y: 99}},
		{"moves freely when no display is known", nil, Point{X: 98, Y: 50}, 10, 0, Point{X: 108, Y: 50}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			screen := &fakeScreen{cursor: c.from, displays: c.displays}
			replay, _ := newReplayOn(screen)

			replay.Emit(event.Relative(event.RelX, c.dx), event.Relative(event.RelY, c.dy), event.Sync())

			if screen.cursor != c.want {
				t.Errorf("cursor at %+v, want %+v", screen.cursor, c.want)
			}
		})
	}
}

func TestMovementWithAButtonHeldIsPostedAsADragOfTheLowestButton(t *testing.T) {
	cases := []struct {
		name   string
		held   []uint16
		action MouseAction
		button int
	}{
		{"no button", nil, MouseMoved, 0},
		{"left button", []uint16{event.ButtonLeft}, MouseDragged, 0},
		{"right button", []uint16{event.ButtonRight}, MouseDragged, 1},
		{"right and left buttons", []uint16{event.ButtonRight, event.ButtonLeft}, MouseDragged, 0},
		{"side button", []uint16{linuxButtonSide}, MouseDragged, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			screen := oneDisplay()
			replay, _ := newReplayOn(screen)
			for _, button := range c.held {
				replay.Emit(press(button), event.Sync())
			}

			replay.Emit(event.Relative(event.RelX, 4), event.Sync())

			moved := screen.posted[len(screen.posted)-1].(MouseEvent)
			if moved.Action != c.action || moved.Button != c.button {
				t.Errorf("movement posted as %+v, want action %d with button %d", moved, c.action, c.button)
			}
		})
	}
}

func clicksOf(screen *fakeScreen) []int {
	var clicks []int
	for _, posted := range screen.posted {
		if mouse, isMouse := posted.(MouseEvent); isMouse && (mouse.Action == MouseDown || mouse.Action == MouseUp) {
			clicks = append(clicks, mouse.Clicks)
		}
	}
	return clicks
}

func TestClickCountGrowsOnlyForQuickClicksOfTheSameButtonOnTheSameSpot(t *testing.T) {
	click := func(replay *Replay, button uint16) {
		replay.Emit(press(button), event.Sync(), release(button), event.Sync())
	}
	cases := []struct {
		name    string
		between func(replay *Replay, clock *manualClock)
		second  uint16
		want    []int
	}{
		{"at once", func(*Replay, *manualClock) {}, event.ButtonLeft, []int{1, 1, 2, 2}},
		{"just inside the interval", func(_ *Replay, clock *manualClock) { clock.advance(doubleClickInterval) }, event.ButtonLeft, []int{1, 1, 2, 2}},
		{"after the interval", func(_ *Replay, clock *manualClock) { clock.advance(doubleClickInterval + time.Millisecond) }, event.ButtonLeft, []int{1, 1, 1, 1}},
		{"with another button", func(*Replay, *manualClock) {}, event.ButtonRight, []int{1, 1, 1, 1}},
		{"after moving a little", func(replay *Replay, _ *manualClock) { replay.Emit(event.Relative(event.RelX, 4), event.Sync()) }, event.ButtonLeft, []int{1, 1, 2, 2}},
		{"after moving away", func(replay *Replay, _ *manualClock) { replay.Emit(event.Relative(event.RelX, 5), event.Sync()) }, event.ButtonLeft, []int{1, 1, 1, 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			screen := oneDisplay()
			replay, clock := newReplayOn(screen)

			click(replay, event.ButtonLeft)
			c.between(replay, clock)
			click(replay, c.second)

			if got := clicksOf(screen); !slices.Equal(got, c.want) {
				t.Errorf("click counts = %v, want %v", got, c.want)
			}
		})
	}
}

func TestThirdQuickClickIsATripleClick(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	for range 3 {
		replay.Emit(press(event.ButtonLeft), event.Sync(), release(event.ButtonLeft), event.Sync())
	}

	if got, want := clicksOf(screen), []int{1, 1, 2, 2, 3, 3}; !slices.Equal(got, want) {
		t.Errorf("click counts = %v, want %v", got, want)
	}
}

func TestButtonsKeepTheirNumbers(t *testing.T) {
	for linuxButton, want := range map[uint16]int{event.ButtonLeft: 0, event.ButtonRight: 1, event.ButtonMiddle: 2, linuxButtonSide: 3} {
		screen := oneDisplay()
		replay, _ := newReplayOn(screen)

		replay.Emit(press(linuxButton), event.Sync())

		if got := screen.posted[0].(MouseEvent).Button; got != want {
			t.Errorf("Linux button %#x posted as button %d, want %d", linuxButton, got, want)
		}
	}
}

func TestButtonIsPressedWhereTheMovementOfTheSameReportEnds(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(event.Relative(event.RelX, 5), press(event.ButtonLeft), event.Sync())

	expectPosted(t, screen,
		MouseEvent{Action: MouseMoved, At: Point{X: 55, Y: 50}, DX: 5},
		MouseEvent{Action: MouseDown, At: Point{X: 55, Y: 50}, Clicks: 1},
	)
}

func TestReleaseOfAButtonThatWasNotPressedIsNotPosted(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(release(event.ButtonLeft), event.Sync())

	expectPosted(t, screen)
}

func TestWheelScrollsUpForPositiveAndHorizontalIsMirrored(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(event.Relative(event.RelWheel, 2), event.Relative(event.RelHWheel, 1), event.Sync())

	expectPosted(t, screen, Scroll{Vertical: 2, Horizontal: -1})
}

func TestMovementAndScrollOfOneReportAreBothPosted(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(event.Relative(event.RelX, 1), event.Relative(event.RelWheel, -1), event.Sync())

	expectPosted(t, screen,
		MouseEvent{Action: MouseMoved, At: Point{X: 51, Y: 50}, DX: 1},
		Scroll{Vertical: -1},
	)
}

const (
	linuxRightCtrl    uint16 = 97
	linuxRightAlt     uint16 = 100
	linuxRightSuper   uint16 = 126
	linuxButtonTask   uint16 = 0x117
	linuxWheelHighRes uint16 = 0x0b
)

type laggingScreen struct {
	fakeScreen
	reported Point
}

func (s *laggingScreen) Cursor() Point {
	return s.reported
}

func TestFlagValuesAreTheOnesMacOSUses(t *testing.T) {
	for name, check := range map[string]struct{ got, want Flags }{
		"Shift":   {FlagShift, 0x20000},
		"Control": {FlagControl, 0x40000},
		"Option":  {FlagOption, 0x80000},
		"Command": {FlagCommand, 0x100000},
	} {
		if check.got != check.want {
			t.Errorf("%s flag = %#x, want %#x", name, uint64(check.got), uint64(check.want))
		}
	}
}

func TestModifiersOfBothSidesSetTheirFlag(t *testing.T) {
	for linuxKey, want := range map[uint16]KeyPress{
		linuxLeftCtrl:   {Code: 0x3B, Down: true, Modifier: true, Flags: FlagControl},
		linuxRightCtrl:  {Code: 0x3E, Down: true, Modifier: true, Flags: FlagControl},
		linuxLeftAlt:    {Code: 0x3A, Down: true, Modifier: true, Flags: FlagOption},
		linuxRightAlt:   {Code: 0x3D, Down: true, Modifier: true, Flags: FlagOption},
		linuxLeftSuper:  {Code: 0x37, Down: true, Modifier: true, Flags: FlagCommand},
		linuxRightSuper: {Code: 0x36, Down: true, Modifier: true, Flags: FlagCommand},
		linuxShiftRight: {Code: macOSShiftRight, Down: true, Modifier: true, Flags: FlagShift},
	} {
		screen := oneDisplay()
		replay, _ := newReplayOn(screen)

		replay.Emit(press(linuxKey))

		expectPosted(t, screen, want)
	}
}

func TestRepeatOfAModifierIsNotPosted(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(press(linuxShiftLeft), event.Key(linuxShiftLeft, event.KeyRepeated), press(linuxKeyA))

	expectPosted(t, screen,
		KeyPress{Code: macOSShiftLeft, Down: true, Modifier: true, Flags: FlagShift},
		KeyPress{Code: macOSKeyA, Down: true, Flags: FlagShift},
	)
}

func TestHeldModifierIsCarriedByMovementDragAndRelease(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(
		press(linuxShiftLeft),
		event.Relative(event.RelX, 1), event.Sync(),
		press(event.ButtonLeft), event.Sync(),
		event.Relative(event.RelX, 1), event.Sync(),
		release(event.ButtonLeft), event.Sync(),
	)

	expectPosted(t, screen,
		KeyPress{Code: macOSShiftLeft, Down: true, Modifier: true, Flags: FlagShift},
		MouseEvent{Action: MouseMoved, At: Point{X: 51, Y: 50}, DX: 1, Flags: FlagShift},
		MouseEvent{Action: MouseDown, At: Point{X: 51, Y: 50}, Clicks: 1, Flags: FlagShift},
		MouseEvent{Action: MouseDragged, At: Point{X: 52, Y: 50}, Clicks: 1, DX: 1, Flags: FlagShift},
		MouseEvent{Action: MouseUp, At: Point{X: 52, Y: 50}, Clicks: 1, Flags: FlagShift},
	)
}

func TestReleaseCarriesTheNumberOfItsButton(t *testing.T) {
	for linuxButton, want := range map[uint16]int{event.ButtonLeft: 0, event.ButtonRight: 1, event.ButtonMiddle: 2, linuxButtonSide: 3, linuxButtonTask: 7} {
		screen := oneDisplay()
		replay, _ := newReplayOn(screen)

		replay.Emit(press(linuxButton), event.Sync(), release(linuxButton), event.Sync())

		down, up := screen.posted[0].(MouseEvent), screen.posted[1].(MouseEvent)
		if down.Action != MouseDown || down.Button != want || up.Action != MouseUp || up.Button != want {
			t.Errorf("Linux button %#x posted as %+v then %+v, want down and up of button %d", linuxButton, down, up, want)
		}
	}
}

func TestMovementAfterTheButtonIsReleasedIsNotADrag(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(press(event.ButtonLeft), event.Sync(), release(event.ButtonLeft), event.Sync(), event.Relative(event.RelX, 2), event.Sync())

	if last := screen.posted[len(screen.posted)-1].(MouseEvent); last.Action != MouseMoved {
		t.Errorf("movement after the release posted as %+v, want a plain move", last)
	}
}

func TestDragCarriesTheClickCountOfItsPress(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(
		press(event.ButtonLeft), event.Sync(), release(event.ButtonLeft), event.Sync(),
		press(event.ButtonLeft), event.Sync(),
		event.Relative(event.RelX, 6), event.Sync(),
	)

	if dragged := screen.posted[len(screen.posted)-1].(MouseEvent); dragged.Action != MouseDragged || dragged.Clicks != 2 {
		t.Errorf("drag after a double click posted as %+v, want a drag with click count 2", dragged)
	}
}

func TestRepeatOfAButtonIsNotPosted(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(event.Key(event.ButtonLeft, event.KeyRepeated), event.Sync())

	expectPosted(t, screen)
}

func TestReleaseOfAButtonHappensWhereThePendingMovementEnds(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(press(event.ButtonLeft), event.Sync(), event.Relative(event.RelX, 5), release(event.ButtonLeft), event.Sync())

	expectPosted(t, screen,
		MouseEvent{Action: MouseDown, At: Point{X: 50, Y: 50}, Clicks: 1},
		MouseEvent{Action: MouseDragged, At: Point{X: 55, Y: 50}, Clicks: 1, DX: 5},
		MouseEvent{Action: MouseUp, At: Point{X: 55, Y: 50}, Clicks: 1},
	)
}

func TestEveryAxisAddsUpWithinAReport(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(
		event.Relative(event.RelY, 1), event.Relative(event.RelY, 2),
		event.Relative(event.RelWheel, 1), event.Relative(event.RelWheel, 1),
		event.Relative(event.RelHWheel, 2), event.Relative(event.RelHWheel, 1),
		event.Sync(),
	)

	expectPosted(t, screen,
		MouseEvent{Action: MouseMoved, At: Point{X: 50, Y: 53}, DY: 3},
		Scroll{Vertical: 2, Horizontal: -3},
	)
}

func TestHorizontalWheelAloneScrolls(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(event.Relative(event.RelHWheel, 1), event.Sync())

	expectPosted(t, screen, Scroll{Horizontal: -1})
}

func TestHighResolutionWheelIsIgnored(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(event.Relative(linuxWheelHighRes, 120), event.Sync())

	expectPosted(t, screen)
}

func TestDoubleClickNeedsTheSameSpotOnBothAxes(t *testing.T) {
	for name, check := range map[string]struct {
		axis  uint16
		delta int32
		want  []int
	}{
		"a little down": {event.RelY, 4, []int{1, 1, 2, 2}},
		"far down":      {event.RelY, 5, []int{1, 1, 1, 1}},
		"far up":        {event.RelY, -5, []int{1, 1, 1, 1}},
		"far left":      {event.RelX, -5, []int{1, 1, 1, 1}},
	} {
		screen := oneDisplay()
		replay, _ := newReplayOn(screen)
		click := []event.Event{press(event.ButtonLeft), event.Sync(), release(event.ButtonLeft), event.Sync()}

		replay.Emit(click...)
		replay.Emit(event.Relative(check.axis, check.delta), event.Sync())
		replay.Emit(click...)

		if got := clicksOf(screen); !slices.Equal(got, check.want) {
			t.Errorf("%s: click counts = %v, want %v", name, got, check.want)
		}
	}
}

func TestCursorStaysOnDisplaysThatDoNotStartAtZero(t *testing.T) {
	leftOfMain := []Rect{{Width: 100, Height: 100}, {X: -100, Width: 100, Height: 100}}
	aboveMain := []Rect{{Width: 100, Height: 100}, {Y: -100, Width: 100, Height: 100}}
	away := []Rect{{X: 100, Y: 200, Width: 100, Height: 100}}
	cases := []struct {
		name     string
		displays []Rect
		from     Point
		dx, dy   int32
		want     Point
	}{
		{"crosses to a display on the left of the main one", leftOfMain, Point{X: 2, Y: 50}, -10, 0, Point{X: -8, Y: 50}},
		{"stops at the outer edge of a display on the left", leftOfMain, Point{X: -98, Y: 50}, -10, 0, Point{X: -100, Y: 50}},
		{"crosses to a display above the main one", aboveMain, Point{X: 50, Y: 2}, 0, -10, Point{X: 50, Y: -8}},
		{"stops at the outer edge of a display above", aboveMain, Point{X: 50, Y: -98}, 0, -10, Point{X: 50, Y: -100}},
		{"stops at the near edges of a display away from zero", away, Point{X: 105, Y: 205}, -10, -10, Point{X: 100, Y: 200}},
		{"stops at the far edges of a display away from zero", away, Point{X: 195, Y: 295}, 10, 10, Point{X: 199, Y: 299}},
		{"the far edge itself is outside", away[:1], Point{X: 198, Y: 250}, 2, 0, Point{X: 199, Y: 250}},
		{"the bottom edge itself is outside", away[:1], Point{X: 150, Y: 298}, 0, 2, Point{X: 150, Y: 299}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			screen := &fakeScreen{cursor: c.from, displays: c.displays}
			replay, _ := newReplayOn(screen)

			replay.Emit(event.Relative(event.RelX, c.dx), event.Relative(event.RelY, c.dy), event.Sync())

			if screen.cursor != c.want {
				t.Errorf("cursor at %+v, want %+v", screen.cursor, c.want)
			}
		})
	}
}

func TestQuickReportsBuildOnWhatWasPostedEvenIfTheSystemLags(t *testing.T) {
	screen := &laggingScreen{fakeScreen: fakeScreen{displays: []Rect{{Width: 1000, Height: 1000}}}, reported: Point{X: 500, Y: 500}}
	replay, _ := newReplayOn(&screen.fakeScreen)
	replay.screen = screen

	replay.Emit(
		event.Relative(event.RelX, 5), event.Sync(),
		event.Relative(event.RelX, 5), event.Sync(),
		press(event.ButtonLeft), event.Sync(),
	)

	expectPosted(t, &screen.fakeScreen,
		MouseEvent{Action: MouseMoved, At: Point{X: 505, Y: 500}, DX: 5},
		MouseEvent{Action: MouseMoved, At: Point{X: 510, Y: 500}, DX: 5},
		MouseEvent{Action: MouseDown, At: Point{X: 510, Y: 500}, Clicks: 1},
	)
}

func TestCursorMovedLocallyIsPickedUpAfterAPause(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)
	replay.Emit(event.Relative(event.RelX, 5), event.Sync())

	screen.cursor = Point{X: 10, Y: 10}
	clock.advance(cursorMemory)
	replay.Emit(event.Relative(event.RelX, 1), event.Sync())
	if want := (Point{X: 56, Y: 50}); screen.cursor != want {
		t.Errorf("within the memory the cursor went to %+v, want %+v", screen.cursor, want)
	}

	screen.cursor = Point{X: 10, Y: 10}
	clock.advance(cursorMemory + time.Millisecond)
	replay.Emit(event.Relative(event.RelX, 1), event.Sync())
	if want := (Point{X: 11, Y: 10}); screen.cursor != want {
		t.Errorf("after a pause the cursor went to %+v, want %+v", screen.cursor, want)
	}
}
