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
