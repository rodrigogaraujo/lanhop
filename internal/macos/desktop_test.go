package macos

import (
	"slices"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
)

var (
	laptopPanel     = Rect{X: 0, Y: 0, Width: 1920, Height: 1080}
	monitorOnLeft   = Rect{X: -1280, Y: 100, Width: 1280, Height: 720}
	monitorOnTop    = Rect{X: 320, Y: -1440, Width: 2560, Height: 1440}
	scaledPanel     = Rect{X: 0, Y: 0, Width: 1512.5, Height: 982.5}
	smallerOnRight  = Rect{X: 1920, Y: 0, Width: 1280, Height: 720}
	belowTheSmaller = Point{X: 3000, Y: 900}
)

type fakeDesktop struct {
	cursor   Point
	displays []Rect
	placed   []Point
}

func (d *fakeDesktop) Cursor() Point {
	return d.cursor
}

func (d *fakeDesktop) Displays() []Rect {
	return d.displays
}

func (d *fakeDesktop) PlaceCursor(at Point) {
	d.placed = append(d.placed, at)
}

func TestCursorIsToldInWholePointsWithTheBoxAroundEveryDisplay(t *testing.T) {
	tests := []struct {
		name         string
		displays     []Rect
		cursor       Point
		wantX, wantY int
		wantDesktop  edge.Bounds
	}{
		{
			name:        "one display, with the cursor at its top left corner",
			displays:    []Rect{laptopPanel},
			cursor:      Point{X: 0, Y: 0},
			wantDesktop: edge.Bounds{MinX: 0, MinY: 0, MaxX: 1919, MaxY: 1079},
		},
		{
			name:        "one display, with the cursor as far right and down as macOS lets it go",
			displays:    []Rect{laptopPanel},
			cursor:      Point{X: 1919.996, Y: 1079.5},
			wantX:       1919,
			wantY:       1079,
			wantDesktop: edge.Bounds{MinX: 0, MinY: 0, MaxX: 1919, MaxY: 1079},
		},
		{
			name:        "a second display on the left, which has negative positions",
			displays:    []Rect{laptopPanel, monitorOnLeft},
			cursor:      Point{X: -0.5, Y: 100.25},
			wantX:       -1,
			wantY:       100,
			wantDesktop: edge.Bounds{MinX: -1280, MinY: 0, MaxX: 1919, MaxY: 1079},
		},
		{
			name:        "a larger display above",
			displays:    []Rect{monitorOnTop, laptopPanel},
			cursor:      Point{X: 2879.9, Y: -1440},
			wantX:       2879,
			wantY:       -1440,
			wantDesktop: edge.Bounds{MinX: 0, MinY: -1440, MaxX: 2879, MaxY: 1079},
		},
		{
			name:        "a display whose size is not a whole number of points",
			displays:    []Rect{scaledPanel},
			cursor:      Point{X: 1512.25, Y: 982.25},
			wantX:       1512,
			wantY:       982,
			wantDesktop: edge.Bounds{MinX: 0, MinY: 0, MaxX: 1512, MaxY: 982},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			x, y, desktop, known := cursorOn(&fakeDesktop{cursor: test.cursor, displays: test.displays})

			if !known {
				t.Fatal("the cursor cannot be told although a display is attached")
			}
			if x != test.wantX || y != test.wantY {
				t.Errorf("cursor at (%d, %d), want (%d, %d)", x, y, test.wantX, test.wantY)
			}
			if desktop != test.wantDesktop {
				t.Errorf("desktop = %+v, want %+v", desktop, test.wantDesktop)
			}
		})
	}
}

func TestCursorCannotBeToldWithoutADisplay(t *testing.T) {
	if x, y, desktop, known := cursorOn(&fakeDesktop{cursor: Point{X: 10, Y: 20}}); known {
		t.Errorf("the cursor is told at (%d, %d) on %+v although no display is attached", x, y, desktop)
	}
}

func TestCursorIsPlacedAtThePointOrOnTheNearestDisplayWhenThePointIsOnNone(t *testing.T) {
	tests := []struct {
		name     string
		displays []Rect
		x, y     int
		want     Point
	}{
		{"a point on the only display", []Rect{laptopPanel}, 1919, 540, Point{X: 1919, Y: 540}},
		{"a point on the second display", []Rect{laptopPanel, monitorOnLeft}, -1280, 400, Point{X: -1280, Y: 400}},
		{"a point of the box that is below the smaller display", []Rect{laptopPanel, smallerOnRight}, int(belowTheSmaller.X), int(belowTheSmaller.Y), Point{X: 3000, Y: 719}},
		{"a point of the box that is nearer to the other display", []Rect{laptopPanel, smallerOnRight}, 1925, 1000, Point{X: 1919, Y: 1000}},
		{"a point above the display on the left", []Rect{laptopPanel, monitorOnLeft}, -640, 0, Point{X: -640, Y: 100}},
		{"no display at all", nil, 5, 6, Point{X: 5, Y: 6}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			desktop := &fakeDesktop{displays: test.displays}

			placeCursorOn(desktop, test.x, test.y)

			if want := []Point{test.want}; !slices.Equal(desktop.placed, want) {
				t.Errorf("placed the cursor at %+v, want %+v", desktop.placed, want)
			}
		})
	}
}
