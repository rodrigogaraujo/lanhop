package macos

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const (
	linuxAxisZ        uint16 = 0x02
	linuxAxisPressure uint16 = 0x18

	middleOfTheRange int32 = 32768
)

func position(x, y int32) []event.Event {
	return []event.Event{event.Absolute(event.AbsX, x), event.Absolute(event.AbsY, y), event.Sync()}
}

func TestPositionOfOneReportIsPostedOnceWhenTheReportEnds(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(event.Absolute(event.AbsX, 0), event.Absolute(event.AbsY, event.AbsoluteMax))
	expectPosted(t, screen)

	replay.Emit(event.Sync())
	expectPosted(t, screen, MouseEvent{Action: MouseMoved, At: Point{X: 0, Y: 99}, DX: -50, DY: 49})

	replay.Emit(event.Sync())
	if len(screen.posted) != 1 {
		t.Errorf("an empty report after a position posted something: %+v", screen.posted)
	}
}

func TestPositionMapsTheRangeOntoTheWholeDesktop(t *testing.T) {
	single := []Rect{{Width: 1001, Height: 501}}
	leftAndAbove := []Rect{{Width: 100, Height: 100}, {X: -200, Y: -50, Width: 200, Height: 150}}
	cases := []struct {
		name     string
		displays []Rect
		x, y     int32
		want     Point
	}{
		{"the start of both axes is the top left corner", single, 0, 0, Point{X: 0, Y: 0}},
		{"the end of both axes is the bottom right corner", single, event.AbsoluteMax, event.AbsoluteMax, Point{X: 1000, Y: 500}},
		{"the start of one axis and the end of the other is the bottom left corner", single, 0, event.AbsoluteMax, Point{X: 0, Y: 500}},
		{"the middle of the range is the middle of the display", single, middleOfTheRange, middleOfTheRange, Point{X: 500, Y: 250}},
		{"a quarter along and three quarters down", single, 16384, 49151, Point{X: 250, Y: 375}},
		{"the start is the corner of the box around two displays", leftAndAbove, 0, 0, Point{X: -200, Y: -50}},
		{"the end is the opposite corner of that box", leftAndAbove, event.AbsoluteMax, event.AbsoluteMax, Point{X: 99, Y: 99}},
		{"the middle of the range is the middle of that box", leftAndAbove, middleOfTheRange, middleOfTheRange, Point{X: -50, Y: 25}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			screen := &fakeScreen{cursor: Point{X: 7, Y: 7}, displays: c.displays}
			replay, _ := newReplayOn(screen)

			replay.Emit(position(c.x, c.y)...)

			if screen.cursor != c.want {
				t.Errorf("cursor at %+v, want %+v", screen.cursor, c.want)
			}
		})
	}
}

func TestPositionWhereNoDisplayIsGoesToTheNearestPointOnADisplay(t *testing.T) {
	tallThenShort := []Rect{{Width: 200, Height: 200}, {X: 200, Width: 100, Height: 150}}
	cases := []struct {
		name string
		x, y int32
		want MouseEvent
	}{
		{"under the short display", event.AbsoluteMax, event.AbsoluteMax, MouseEvent{Action: MouseMoved, At: Point{X: 299, Y: 149}, DX: 199, DY: 49}},
		{"beside the tall display", 46029, event.AbsoluteMax, MouseEvent{Action: MouseMoved, At: Point{X: 199, Y: 199}, DX: 99, DY: 99}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			screen := &fakeScreen{cursor: Point{X: 100, Y: 100}, displays: tallThenShort}
			replay, _ := newReplayOn(screen)

			replay.Emit(position(c.x, c.y)...)

			expectPosted(t, screen, c.want)
		})
	}
}

func TestAxisThatAReportDoesNotCarryKeepsItsPosition(t *testing.T) {
	cases := []struct {
		name    string
		carried event.Event
		want    MouseEvent
	}{
		{"only the horizontal axis", event.Absolute(event.AbsX, event.AbsoluteMax), MouseEvent{Action: MouseMoved, At: Point{X: 99, Y: 50}, DX: 49}},
		{"only the vertical axis", event.Absolute(event.AbsY, 0), MouseEvent{Action: MouseMoved, At: Point{X: 50, Y: 0}, DY: -50}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			screen := oneDisplay()
			replay, _ := newReplayOn(screen)

			replay.Emit(c.carried, event.Sync())

			expectPosted(t, screen, c.want)
		})
	}
}

func TestAxisThatAReportDoesNotCarryIsTheOneLastPlacedAndAfterAPauseTheOneRead(t *testing.T) {
	screen := &laggingScreen{fakeScreen: fakeScreen{displays: []Rect{{Width: 1000, Height: 1000}}}, reported: Point{X: 500, Y: 500}}
	replay, clock := newReplayOn(&screen.fakeScreen)
	replay.screen = screen

	replay.Emit(position(0, 0)...)
	replay.Emit(event.Absolute(event.AbsX, event.AbsoluteMax), event.Sync())
	clock.advance(cursorMemory + time.Millisecond)
	replay.Emit(event.Absolute(event.AbsY, 0), event.Sync())

	expectPosted(t, &screen.fakeScreen,
		MouseEvent{Action: MouseMoved, At: Point{X: 0, Y: 0}, DX: -500, DY: -500},
		MouseEvent{Action: MouseMoved, At: Point{X: 999, Y: 0}, DX: 999},
		MouseEvent{Action: MouseMoved, At: Point{X: 500, Y: 0}, DY: -500},
	)
}

func TestAxisCarriedByOneReportIsNotCarriedByTheNext(t *testing.T) {
	cases := []struct {
		name    string
		carried event.Event
		want    MouseEvent
	}{
		{"the horizontal axis alone", event.Absolute(event.AbsX, event.AbsoluteMax), MouseEvent{Action: MouseMoved, At: Point{X: 99, Y: 30}, DX: 79}},
		{"the vertical axis alone", event.Absolute(event.AbsY, event.AbsoluteMax), MouseEvent{Action: MouseMoved, At: Point{X: 20, Y: 99}, DY: 69}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			screen := oneDisplay()
			replay, clock := newReplayOn(screen)
			replay.Emit(position(0, 0)...)
			screen.cursor = Point{X: 20, Y: 30}
			clock.advance(cursorMemory + time.Millisecond)

			replay.Emit(c.carried, event.Sync())

			if last := screen.posted[len(screen.posted)-1]; last != c.want {
				t.Errorf("the next report was posted as %+v, want %+v", last, c.want)
			}
		})
	}
}

func TestAxisThatAReportDoesNotCarryStaysBetweenTwoPointsAtTheFarEdge(t *testing.T) {
	screen := &fakeScreen{cursor: Point{X: 10, Y: 99.5}, displays: []Rect{{Width: 100, Height: 100}}}
	replay, _ := newReplayOn(screen)

	replay.Emit(event.Absolute(event.AbsX, 0), event.Sync())

	expectPosted(t, screen, MouseEvent{Action: MouseMoved, At: Point{X: 0, Y: 99.5}, DX: -10})
}

func TestLastPositionOfAnAxisWithinAReportWins(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(event.Absolute(event.AbsX, 0), event.Absolute(event.AbsX, event.AbsoluteMax), event.Sync())

	expectPosted(t, screen, MouseEvent{Action: MouseMoved, At: Point{X: 99, Y: 50}, DX: 49})
}

func TestPositionCarriesModifiersAndIsADragWhileAButtonIsHeld(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(press(linuxShiftLeft))
	replay.Emit(position(0, 0)...)
	replay.Emit(press(event.ButtonLeft), event.Sync())
	replay.Emit(position(event.AbsoluteMax, event.AbsoluteMax)...)
	replay.Emit(release(event.ButtonLeft), event.Sync())
	replay.Emit(position(0, event.AbsoluteMax)...)

	expectPosted(t, screen,
		KeyPress{Code: macOSShiftLeft, Down: true, Modifier: true, Flags: FlagShift},
		MouseEvent{Action: MouseMoved, At: Point{X: 0, Y: 0}, DX: -50, DY: -50, Flags: FlagShift},
		MouseEvent{Action: MouseDown, At: Point{X: 0, Y: 0}, Clicks: 1, Flags: FlagShift},
		MouseEvent{Action: MouseDragged, At: Point{X: 99, Y: 99}, Clicks: 1, DX: 99, DY: 99, Flags: FlagShift},
		MouseEvent{Action: MouseUp, At: Point{X: 99, Y: 99}, Clicks: 1, Flags: FlagShift},
		MouseEvent{Action: MouseMoved, At: Point{X: 0, Y: 99}, DX: -99, Flags: FlagShift},
	)
}

func TestPositionWithButtonsHeldIsADragOfTheLowestOne(t *testing.T) {
	cases := []struct {
		name   string
		held   []uint16
		button int
	}{
		{"right button", []uint16{event.ButtonRight}, 1},
		{"right and left buttons", []uint16{event.ButtonRight, event.ButtonLeft}, 0},
		{"side and middle buttons", []uint16{linuxButtonSide, event.ButtonMiddle}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			screen := oneDisplay()
			replay, _ := newReplayOn(screen)
			for _, button := range c.held {
				replay.Emit(press(button), event.Sync())
			}

			replay.Emit(position(0, 0)...)

			placed := screen.posted[len(screen.posted)-1].(MouseEvent)
			if want := (MouseEvent{Action: MouseDragged, At: Point{X: 0, Y: 0}, Button: c.button, Clicks: 1, DX: -50, DY: -50}); placed != want {
				t.Errorf("position posted as %+v, want %+v", placed, want)
			}
		})
	}
}

func TestDragToAPositionCarriesTheClickCountOfItsPress(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(
		press(event.ButtonLeft), event.Sync(), release(event.ButtonLeft), event.Sync(),
		press(event.ButtonLeft), event.Sync(),
	)
	replay.Emit(position(event.AbsoluteMax, event.AbsoluteMax)...)

	placed := screen.posted[len(screen.posted)-1].(MouseEvent)
	if want := (MouseEvent{Action: MouseDragged, At: Point{X: 99, Y: 99}, Clicks: 2, DX: 49, DY: 49}); placed != want {
		t.Errorf("position after a double click posted as %+v, want %+v", placed, want)
	}
}

func TestDeltasOfAPositionAreTheWholePointsTheCursorTravelled(t *testing.T) {
	screen := &fakeScreen{cursor: Point{X: 10.25, Y: 20.75}, displays: []Rect{{Width: 100, Height: 100}}}
	replay, _ := newReplayOn(screen)

	replay.Emit(position(0, 0)...)
	replay.Emit(position(event.AbsoluteMax, event.AbsoluteMax)...)

	expectPosted(t, screen,
		MouseEvent{Action: MouseMoved, At: Point{X: 0, Y: 0}, DX: -10, DY: -21},
		MouseEvent{Action: MouseMoved, At: Point{X: 99, Y: 99}, DX: 99, DY: 99},
	)
}

func TestPositionWhereTheCursorAlreadyIsPostsNothing(t *testing.T) {
	screen := &fakeScreen{cursor: Point{X: 0, Y: 99}, displays: []Rect{{Width: 100, Height: 100}}}
	replay, _ := newReplayOn(screen)

	replay.Emit(position(0, event.AbsoluteMax)...)
	expectPosted(t, screen)

	replay.Emit(position(event.AbsoluteMax, 0)...)
	replay.Emit(position(event.AbsoluteMax, 0)...)
	replay.Emit(event.Absolute(event.AbsX, event.AbsoluteMax), event.Sync())
	expectPosted(t, screen, MouseEvent{Action: MouseMoved, At: Point{X: 99, Y: 0}, DX: 99, DY: -99})
}

func TestRelativeMovementOfTheSameReportIsAppliedAfterThePosition(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(
		event.Relative(event.RelX, 5), event.Relative(event.RelY, 2),
		event.Absolute(event.AbsX, 0), event.Absolute(event.AbsY, 0),
		event.Sync(),
	)

	expectPosted(t, screen,
		MouseEvent{Action: MouseMoved, At: Point{X: 0, Y: 0}, DX: -50, DY: -50},
		MouseEvent{Action: MouseMoved, At: Point{X: 5, Y: 2}, DX: 5, DY: 2},
	)
}

func TestLaterMovementAndClicksBuildOnThePositionEvenIfTheSystemLags(t *testing.T) {
	screen := &laggingScreen{fakeScreen: fakeScreen{displays: []Rect{{Width: 1000, Height: 1000}}}, reported: Point{X: 500, Y: 500}}
	replay, _ := newReplayOn(&screen.fakeScreen)
	replay.screen = screen

	replay.Emit(position(0, event.AbsoluteMax)...)
	replay.Emit(position(0, event.AbsoluteMax)...)
	replay.Emit(event.Relative(event.RelX, 5), event.Sync())
	replay.Emit(press(event.ButtonLeft), event.Sync())

	expectPosted(t, &screen.fakeScreen,
		MouseEvent{Action: MouseMoved, At: Point{X: 0, Y: 999}, DX: -500, DY: 499},
		MouseEvent{Action: MouseMoved, At: Point{X: 5, Y: 999}, DX: 5},
		MouseEvent{Action: MouseDown, At: Point{X: 5, Y: 999}, Clicks: 1},
	)
}

func TestPlacedPositionIsForgottenAfterAPause(t *testing.T) {
	screen := oneDisplay()
	replay, clock := newReplayOn(screen)
	replay.Emit(position(0, 0)...)

	screen.cursor = Point{X: 10, Y: 10}
	clock.advance(cursorMemory)
	replay.Emit(event.Relative(event.RelX, 1), event.Sync())
	if want := (Point{X: 1, Y: 0}); screen.cursor != want {
		t.Errorf("within the memory the cursor went to %+v, want %+v", screen.cursor, want)
	}

	screen.cursor = Point{X: 10, Y: 10}
	clock.advance(cursorMemory + time.Millisecond)
	replay.Emit(position(0, 0)...)
	if last, want := screen.posted[len(screen.posted)-1], (MouseEvent{Action: MouseMoved, At: Point{X: 0, Y: 0}, DX: -10, DY: -10}); last != want {
		t.Errorf("after a pause the position was posted as %+v, want %+v", last, want)
	}
}

func TestButtonsOfTheSameReportFollowTheirOrderWithThePosition(t *testing.T) {
	toTheCorner := []event.Event{event.Absolute(event.AbsX, event.AbsoluteMax), event.Absolute(event.AbsY, event.AbsoluteMax)}
	pressLeft, releaseLeft, endOfReport := []event.Event{press(event.ButtonLeft)}, []event.Event{release(event.ButtonLeft)}, []event.Event{event.Sync()}
	cases := []struct {
		name   string
		report []event.Event
		want   []any
	}{
		{
			"a press after the position happens where the position is",
			slices.Concat(toTheCorner, pressLeft, endOfReport),
			[]any{
				MouseEvent{Action: MouseMoved, At: Point{X: 99, Y: 99}, DX: 49, DY: 49},
				MouseEvent{Action: MouseDown, At: Point{X: 99, Y: 99}, Clicks: 1},
			},
		},
		{
			"a press before the position happens where the cursor was and the position drags",
			slices.Concat(pressLeft, toTheCorner, endOfReport),
			[]any{
				MouseEvent{Action: MouseDown, At: Point{X: 50, Y: 50}, Clicks: 1},
				MouseEvent{Action: MouseDragged, At: Point{X: 99, Y: 99}, Clicks: 1, DX: 49, DY: 49},
			},
		},
		{
			"a press, the position and the release drag to the position and release there",
			slices.Concat(pressLeft, toTheCorner, releaseLeft, endOfReport),
			[]any{
				MouseEvent{Action: MouseDown, At: Point{X: 50, Y: 50}, Clicks: 1},
				MouseEvent{Action: MouseDragged, At: Point{X: 99, Y: 99}, Clicks: 1, DX: 49, DY: 49},
				MouseEvent{Action: MouseUp, At: Point{X: 99, Y: 99}, Clicks: 1},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			screen := oneDisplay()
			replay, _ := newReplayOn(screen)

			replay.Emit(c.report...)

			expectPosted(t, screen, c.want...)
		})
	}
}

func TestClickAfterAPositionHappensThere(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(position(0, event.AbsoluteMax)...)
	replay.Emit(press(event.ButtonLeft), event.Sync(), release(event.ButtonLeft), event.Sync())

	expectPosted(t, screen,
		MouseEvent{Action: MouseMoved, At: Point{X: 0, Y: 99}, DX: -50, DY: 49},
		MouseEvent{Action: MouseDown, At: Point{X: 0, Y: 99}, Clicks: 1},
		MouseEvent{Action: MouseUp, At: Point{X: 0, Y: 99}, Clicks: 1},
	)
}

func TestPositionOutsideTheRangeIsClampedToItsNearestEnd(t *testing.T) {
	cases := []struct {
		name  string
		value int32
		want  Point
	}{
		{"just under the start", -1, Point{X: 0, Y: 0}},
		{"far under the start", math.MinInt32, Point{X: 0, Y: 0}},
		{"just over the end", event.AbsoluteMax + 1, Point{X: 99, Y: 99}},
		{"far over the end", math.MaxInt32, Point{X: 99, Y: 99}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			screen := oneDisplay()
			replay, _ := newReplayOn(screen)

			replay.Emit(position(c.value, c.value)...)

			if screen.cursor != c.want {
				t.Errorf("cursor at %+v, want %+v", screen.cursor, c.want)
			}
		})
	}
}

func TestPositionOutsideTheRangeIsClampedBeforeTheNearestDisplayIsChosen(t *testing.T) {
	wideAboveOneThatEndsSooner := []Rect{{Width: 300, Height: 100}, {Y: 150, Width: 298, Height: 100}}
	wideAboveOneThatStartsLater := []Rect{{Width: 300, Height: 100}, {X: 2, Y: 150, Width: 298, Height: 100}}
	cases := []struct {
		name     string
		displays []Rect
		x        int32
		want     Point
	}{
		{"far over the end", wideAboveOneThatEndsSooner, math.MaxInt32, Point{X: 297, Y: 240}},
		{"far under the start", wideAboveOneThatStartsLater, math.MinInt32, Point{X: 2, Y: 240}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			screen := &fakeScreen{cursor: Point{X: 7, Y: 7}, displays: c.displays}
			replay, _ := newReplayOn(screen)

			replay.Emit(position(c.x, 63166)...)

			if screen.cursor != c.want {
				t.Errorf("cursor at %+v, want %+v", screen.cursor, c.want)
			}
		})
	}
}

func TestOtherAbsoluteAxesAreIgnored(t *testing.T) {
	screen := oneDisplay()
	replay, _ := newReplayOn(screen)

	replay.Emit(event.Absolute(linuxAxisZ, 0), event.Absolute(linuxAxisPressure, event.AbsoluteMax), event.Sync())
	expectPosted(t, screen)

	replay.Emit(event.Absolute(event.AbsX, 0), event.Absolute(linuxAxisZ, event.AbsoluteMax), event.Sync())
	expectPosted(t, screen, MouseEvent{Action: MouseMoved, At: Point{X: 0, Y: 50}, DX: -50})
}

func TestPositionIsDroppedWhenNoDisplayIsKnown(t *testing.T) {
	screen := &fakeScreen{cursor: Point{X: 50, Y: 50}}
	replay, _ := newReplayOn(screen)

	replay.Emit(position(0, 0)...)
	replay.Emit(event.Relative(event.RelX, 1), event.Sync())

	expectPosted(t, screen, MouseEvent{Action: MouseMoved, At: Point{X: 51, Y: 50}, DX: 1})
}
