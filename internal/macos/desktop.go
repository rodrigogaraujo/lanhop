package macos

import (
	"math"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
)

type cursorScreen interface {
	Cursor() Point
	Displays() []Rect
	PlaceCursor(at Point)
}

func cursorOn(screen cursorScreen) (x, y int, desktop edge.Bounds, known bool) {
	desktop, known = desktopBounds(screen.Displays())
	if !known {
		return 0, 0, edge.Bounds{}, false
	}
	at := screen.Cursor()
	return int(math.Floor(at.X)), int(math.Floor(at.Y)), desktop, true
}

func placeCursorOn(screen cursorScreen, x, y int) {
	screen.PlaceCursor(ontoNearestDisplay(Point{X: float64(x), Y: float64(y)}, screen.Displays()))
}

func desktopBounds(displays []Rect) (edge.Bounds, bool) {
	if len(displays) == 0 {
		return edge.Bounds{}, false
	}
	left, top := math.Inf(1), math.Inf(1)
	right, bottom := math.Inf(-1), math.Inf(-1)
	for _, display := range displays {
		left, top = min(left, display.X), min(top, display.Y)
		right, bottom = max(right, display.X+display.Width), max(bottom, display.Y+display.Height)
	}
	return edge.Bounds{
		MinX: int(math.Floor(left)),
		MinY: int(math.Floor(top)),
		MaxX: int(math.Ceil(right)) - 1,
		MaxY: int(math.Ceil(bottom)) - 1,
	}, true
}

func ontoNearestDisplay(at Point, displays []Rect) Point {
	nearest, shortest := at, math.Inf(1)
	for _, display := range displays {
		if display.contains(at) {
			return at
		}
		inside := display.nearestInside(at)
		if distance := math.Hypot(inside.X-at.X, inside.Y-at.Y); distance < shortest {
			nearest, shortest = inside, distance
		}
	}
	return nearest
}
