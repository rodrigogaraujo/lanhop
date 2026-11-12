//go:build darwin && cgo

package macos

import (
	"os"
	"testing"
	"time"
)

const cursorNudge = 3

func TestCaptureTellsItsCursorInsideTheDesktop(t *testing.T) {
	x, y, desktop, known := (&Capture{}).Cursor()
	if !known {
		t.Skip("no display is attached")
	}
	if x < desktop.MinX || x > desktop.MaxX || y < desktop.MinY || y > desktop.MaxY {
		t.Errorf("the cursor at (%d, %d) is outside the desktop %+v", x, y, desktop)
	}
	if desktop.MaxX <= desktop.MinX || desktop.MaxY <= desktop.MinY {
		t.Errorf("the desktop %+v has no size", desktop)
	}
}

func towards(centre, from float64) int {
	if from < centre {
		return int(from) + cursorNudge
	}
	return int(from) - cursorNudge
}

func TestPlacedCursorIsToldWhereItWasPut(t *testing.T) {
	if os.Getenv(realInputVariable) == "" {
		t.Skipf("set %s=1 to move the cursor of this Mac", realInputVariable)
	}
	capture := &Capture{}
	before := Quartz{}.Cursor()
	var home *Rect
	for _, display := range (Quartz{}).Displays() {
		if display.contains(before) {
			home = &display
		}
	}
	if home == nil {
		t.Skip("the cursor is on no display")
	}
	t.Cleanup(func() { Quartz{}.PlaceCursor(before) })
	wantX := towards(home.X+home.Width/2, before.X)
	wantY := towards(home.Y+home.Height/2, before.Y)

	capture.PlaceCursor(wantX, wantY)

	var x, y int
	for deadline := time.Now().Add(observePatience); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if x, y, _, _ = capture.Cursor(); x == wantX && y == wantY {
			return
		}
	}
	t.Errorf("the cursor was placed at (%d, %d) and is told at (%d, %d)", wantX, wantY, x, y)
}
