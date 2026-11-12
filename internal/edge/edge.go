package edge

import "time"

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
	CornerSize int
}

func DefaultSettings() Settings {
	return Settings{
		PushTime:   150 * time.Millisecond,
		PushGap:    50 * time.Millisecond,
		CornerSize: 32,
	}
}

type Crossing struct {
	Side       Side
	Proportion float64
}

type push struct {
	started time.Time
	latest  time.Time
}

type Watcher struct {
	settings Settings
	bounds   Bounds
	buttons  map[uint16]struct{}
	pushes   map[Side]push
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
		return
	}
	w.buttons[code] = struct{}{}
	clear(w.pushes)
}

func (w *Watcher) Move(now time.Time, x, y, dx, dy int) (Crossing, bool) {
	if len(w.buttons) > 0 || w.bounds.empty() {
		return Crossing{}, false
	}
	for _, side := range sides {
		c := w.bounds.contact(side, x, y, dx, dy)
		if !c.atEdge || c.outward < 0 || c.inCorner(w.settings.CornerSize) {
			delete(w.pushes, side)
			continue
		}
		if c.outward == 0 {
			continue
		}
		if w.pushedLongEnough(side, now) {
			clear(w.pushes)
			return Crossing{Side: side, Proportion: c.proportion()}, true
		}
	}
	return Crossing{}, false
}

func (w *Watcher) pushedLongEnough(side Side, now time.Time) bool {
	p, pushing := w.pushes[side]
	if !pushing || now.Sub(p.latest) > w.settings.PushGap {
		p.started = now
	}
	p.latest = now
	w.pushes[side] = p
	return now.Sub(p.started) >= w.settings.PushTime
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
