package edge

import (
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

type Side int

const (
	Left Side = iota + 1
	Right
	Top
	Bottom
)

var sides = [...]Side{Left, Right, Top, Bottom}

func (s Side) String() string {
	switch s {
	case Left:
		return "left"
	case Right:
		return "right"
	case Top:
		return "top"
	case Bottom:
		return "bottom"
	}
	return "unknown"
}

type Bounds struct {
	MinX, MinY, MaxX, MaxY int
}

func (b Bounds) empty() bool {
	return b.MaxX <= b.MinX || b.MaxY <= b.MinY
}

type Settings struct {
	PushTime   time.Duration
	PushGap    time.Duration
	PushPause  time.Duration
	CornerSize int
}

const (
	shortestDragPush       = 600 * time.Millisecond
	dragPushBeyondPushTime = 300 * time.Millisecond
)

func DefaultSettings() Settings {
	return Settings{
		PushTime:   150 * time.Millisecond,
		PushGap:    50 * time.Millisecond,
		PushPause:  700 * time.Millisecond,
		CornerSize: 32,
	}
}

func (s Settings) dragPushTime() time.Duration {
	return max(shortestDragPush, s.PushTime+dragPushBeyondPushTime)
}

type Crossing struct {
	Side       Side
	Proportion float64
	Dragging   bool
}

type push struct {
	pushedFor time.Duration
	latest    time.Time
}

type drag struct {
	on   bool
	dock Side
}

type Watcher struct {
	settings Settings
	bounds   Bounds
	buttons  map[uint16]struct{}
	pushes   map[Side]push
	drag     drag
}

func New(settings Settings) *Watcher {
	return &Watcher{
		settings: settings,
		buttons:  make(map[uint16]struct{}),
		pushes:   make(map[Side]push),
	}
}

func (w *Watcher) SetBounds(bounds Bounds) {
	if bounds == w.bounds {
		return
	}
	w.bounds = bounds
	clear(w.pushes)
}

func (w *Watcher) Button(code uint16, pressed bool) {
	if !pressed {
		delete(w.buttons, code)
		if code == event.ButtonLeft {
			w.DragEnded()
		}
		return
	}
	w.buttons[code] = struct{}{}
	clear(w.pushes)
}

func (w *Watcher) DragStarted(dock Side) {
	if _, held := w.buttons[event.ButtonLeft]; !held {
		return
	}
	dragging := drag{on: true, dock: dock}
	if w.drag == dragging {
		return
	}
	w.drag = dragging
	clear(w.pushes)
}

func (w *Watcher) DragEnded() {
	if !w.drag.on {
		return
	}
	w.drag = drag{}
	clear(w.pushes)
}

func (w *Watcher) Move(now time.Time, x, y, dx, dy int) (Crossing, bool) {
	if w.buttonsBlock() || w.bounds.empty() {
		return Crossing{}, false
	}
	for _, side := range sides {
		c := w.bounds.contact(side, x, y, dx, dy)
		if !c.atEdge || c.outward < 0 || c.inCorner(w.settings.CornerSize) || w.dockSitsOn(side) {
			delete(w.pushes, side)
			continue
		}
		if c.outward == 0 {
			continue
		}
		if w.pushedLongEnough(side, now) {
			clear(w.pushes)
			return Crossing{Side: side, Proportion: c.proportion(), Dragging: w.drag.on}, true
		}
	}
	return Crossing{}, false
}

func (w *Watcher) buttonsBlock() bool {
	for code := range w.buttons {
		if code != event.ButtonLeft || !w.drag.on {
			return true
		}
	}
	return false
}

func (w *Watcher) dockSitsOn(side Side) bool {
	return w.drag.on && w.drag.dock == side
}

func (w *Watcher) pushTime() time.Duration {
	if w.drag.on {
		return w.settings.dragPushTime()
	}
	return w.settings.PushTime
}

func (w *Watcher) pushedLongEnough(side Side, now time.Time) bool {
	p, pushing := w.pushes[side]
	switch pause := now.Sub(p.latest); {
	case !pushing || pause > max(w.settings.PushGap, w.settings.PushPause):
		p = push{}
	case pause <= w.settings.PushGap:
		p.pushedFor += pause
	}
	p.latest = now
	w.pushes[side] = p
	return p.pushedFor >= w.pushTime()
}

type contact struct {
	atEdge  bool
	outward int
	along   int
	start   int
	end     int
}

func (b Bounds) contact(side Side, x, y, dx, dy int) contact {
	switch side {
	case Left:
		return touching(x <= b.MinX, -dx, y, b.MinY, b.MaxY)
	case Right:
		return touching(x >= b.MaxX, dx, y, b.MinY, b.MaxY)
	case Top:
		return touching(y <= b.MinY, -dy, x, b.MinX, b.MaxX)
	default:
		return touching(y >= b.MaxY, dy, x, b.MinX, b.MaxX)
	}
}

func touching(atEdge bool, outward, position, start, end int) contact {
	return contact{
		atEdge:  atEdge,
		outward: outward,
		along:   min(max(position, start), end),
		start:   start,
		end:     end,
	}
}

func (c contact) inCorner(size int) bool {
	return c.along-c.start < size || c.end-c.along < size
}

func (c contact) proportion() float64 {
	return float64(c.along-c.start) / float64(c.end-c.start)
}
