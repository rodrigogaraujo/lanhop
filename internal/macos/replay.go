package macos

import (
	"errors"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/keymap"
)

type Flags uint64

const (
	FlagShift   Flags = 1 << 17
	FlagControl Flags = 1 << 18
	FlagOption  Flags = 1 << 19
	FlagCommand Flags = 1 << 20
)

type MouseAction int

const (
	MouseMoved MouseAction = iota
	MouseDragged
	MouseDown
	MouseUp
)

const (
	doubleClickInterval = 500 * time.Millisecond
	doubleClickDistance = 4.0
	cursorMemory        = 500 * time.Millisecond

	defaultRepeatDelay    = 500 * time.Millisecond
	defaultRepeatInterval = time.Second / 12
	shortestRepeat        = 16 * time.Millisecond

	capsLock uint16 = 0x39
)

var errClosed = errors.New("macos: the replay is closed")

var modifierFlags = map[uint16]Flags{
	0x38: FlagShift,
	0x3C: FlagShift,
	0x3B: FlagControl,
	0x3E: FlagControl,
	0x3A: FlagOption,
	0x3D: FlagOption,
	0x37: FlagCommand,
	0x36: FlagCommand,
}

type Point struct {
	X, Y float64
}

type Rect struct {
	X, Y, Width, Height float64
}

func (r Rect) contains(p Point) bool {
	return p.X >= r.X && p.X < r.X+r.Width && p.Y >= r.Y && p.Y < r.Y+r.Height
}

func (r Rect) nearestInside(p Point) Point {
	return Point{
		X: min(max(p.X, r.X), r.X+r.Width-1),
		Y: min(max(p.Y, r.Y), r.Y+r.Height-1),
	}
}

type KeyPress struct {
	Code     uint16
	Down     bool
	Modifier bool
	Repeat   bool
	Flags    Flags
}

type MouseEvent struct {
	Action MouseAction
	At     Point
	Button int
	Clicks int
	DX, DY int32
	Flags  Flags
}

type Scroll struct {
	Vertical, Horizontal int32
	Flags                Flags
}

type Screen interface {
	PostKey(KeyPress)
	PostMouse(MouseEvent)
	PostScroll(Scroll)
	Cursor() Point
	Displays() []Rect
	KeyRepeat() (delay, interval time.Duration, known bool)
}

type stopper interface {
	Stop() bool
}

type repeat struct {
	key      uint16
	interval time.Duration
	timer    stopper
}

type lastPress struct {
	button int
	count  int
	at     Point
	when   time.Time
}

type Replay struct {
	mu            sync.Mutex
	screen        Screen
	keys          keymap.Translator
	now           func() time.Time
	after         func(time.Duration, func()) stopper
	repeating     *repeat
	closed        bool
	heldModifiers map[uint16]Flags
	heldButtons   map[int]int
	moveX, moveY  int32
	wheel, hwheel int32
	previous      lastPress
	placed        Point
	placedAt      time.Time
}

func NewReplay(screen Screen, keys keymap.Translator) *Replay {
	return &Replay{
		screen:        screen,
		keys:          keys,
		now:           time.Now,
		after:         func(wait time.Duration, run func()) stopper { return time.AfterFunc(wait, run) },
		heldModifiers: make(map[uint16]Flags),
		heldButtons:   make(map[int]int),
	}
}

func (r *Replay) Emit(events ...event.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errClosed
	}
	for _, e := range events {
		switch e.Type {
		case event.TypeKey:
			r.key(e.Code, e.Value)
		case event.TypeRelative:
			r.accumulate(e.Code, e.Value)
		case event.TypeSync:
			r.flush()
		}
	}
	return nil
}

func (r *Replay) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopRepeating()
	r.closed = true
	return nil
}

func (r *Replay) key(code uint16, value int32) {
	if value != event.KeyPressed && value != event.KeyReleased {
		return
	}
	if code >= event.ButtonFirst && code <= event.ButtonLast {
		r.button(int(code-event.ButtonFirst), value)
		return
	}
	macOSKey, mapped := r.keys.ToMacOS(code)
	if !mapped {
		return
	}
	down := value != event.KeyReleased
	flag, modifier := modifierFlags[macOSKey]
	if modifier {
		if down {
			r.heldModifiers[macOSKey] = flag
		} else {
			delete(r.heldModifiers, macOSKey)
		}
	}
	r.screen.PostKey(KeyPress{Code: macOSKey, Down: down, Modifier: modifier, Flags: r.flags()})
	if modifier || macOSKey == capsLock {
		return
	}
	if down {
		r.startRepeating(macOSKey)
	} else if r.repeating != nil && r.repeating.key == macOSKey {
		r.stopRepeating()
	}
}

func (r *Replay) startRepeating(key uint16) {
	r.stopRepeating()
	delay, interval, on := repeatTiming(r.screen.KeyRepeat())
	if !on {
		return
	}
	held := &repeat{key: key, interval: interval}
	held.timer = r.after(delay, func() { r.repeatKey(held) })
	r.repeating = held
}

func (r *Replay) stopRepeating() {
	if r.repeating == nil {
		return
	}
	r.repeating.timer.Stop()
	r.repeating = nil
}

func (r *Replay) repeatKey(held *repeat) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.repeating != held {
		return
	}
	r.screen.PostKey(KeyPress{Code: held.key, Down: true, Repeat: true, Flags: r.flags()})
	held.timer = r.after(held.interval, func() { r.repeatKey(held) })
}

func repeatTiming(delay, interval time.Duration, known bool) (time.Duration, time.Duration, bool) {
	if !known {
		return defaultRepeatDelay, defaultRepeatInterval, true
	}
	if delay <= 0 || interval <= 0 {
		return 0, 0, false
	}
	return max(delay, shortestRepeat), max(interval, shortestRepeat), true
}

func (r *Replay) button(button int, value int32) {
	r.flush()
	at := r.cursor()
	switch value {
	case event.KeyPressed:
		r.remember(at)
		clicks := r.clicksFor(button, at)
		r.heldButtons[button] = clicks
		r.screen.PostMouse(MouseEvent{Action: MouseDown, At: at, Button: button, Clicks: clicks, Flags: r.flags()})
	case event.KeyReleased:
		clicks, held := r.heldButtons[button]
		if !held {
			return
		}
		delete(r.heldButtons, button)
		r.remember(at)
		r.screen.PostMouse(MouseEvent{Action: MouseUp, At: at, Button: button, Clicks: clicks, Flags: r.flags()})
	}
}

func (r *Replay) clicksFor(button int, at Point) int {
	now := r.now()
	count := 1
	sameSpot := abs(at.X-r.previous.at.X) <= doubleClickDistance && abs(at.Y-r.previous.at.Y) <= doubleClickDistance
	if r.previous.count > 0 && r.previous.button == button && now.Sub(r.previous.when) <= doubleClickInterval && sameSpot {
		count = r.previous.count + 1
	}
	r.previous = lastPress{button: button, count: count, at: at, when: now}
	return count
}

func (r *Replay) accumulate(axis uint16, delta int32) {
	switch axis {
	case event.RelX:
		r.moveX += delta
	case event.RelY:
		r.moveY += delta
	case event.RelWheel:
		r.wheel += delta
	case event.RelHWheel:
		r.hwheel += delta
	}
}

func (r *Replay) flush() {
	if r.moveX != 0 || r.moveY != 0 {
		r.move(r.moveX, r.moveY)
		r.moveX, r.moveY = 0, 0
	}
	if r.wheel != 0 || r.hwheel != 0 {
		r.screen.PostScroll(Scroll{Vertical: r.wheel, Horizontal: -r.hwheel, Flags: r.flags()})
		r.wheel, r.hwheel = 0, 0
	}
}

func (r *Replay) move(dx, dy int32) {
	from := r.cursor()
	to := keepOnDisplays(from, Point{X: from.X + float64(dx), Y: from.Y + float64(dy)}, r.screen.Displays())
	moved := MouseEvent{Action: MouseMoved, At: to, DX: dx, DY: dy, Flags: r.flags()}
	if button, dragging := r.lowestHeldButton(); dragging {
		moved.Action = MouseDragged
		moved.Button = button
		moved.Clicks = r.heldButtons[button]
	}
	r.screen.PostMouse(moved)
	r.remember(to)
}

func (r *Replay) cursor() Point {
	if !r.placedAt.IsZero() && r.now().Sub(r.placedAt) <= cursorMemory {
		return r.placed
	}
	return r.screen.Cursor()
}

func (r *Replay) remember(at Point) {
	r.placed = at
	r.placedAt = r.now()
}

func keepOnDisplays(from, to Point, displays []Rect) Point {
	if len(displays) == 0 {
		return to
	}
	home := displays[0]
	for _, display := range displays {
		if display.contains(to) {
			return to
		}
		if display.contains(from) {
			home = display
		}
	}
	return home.nearestInside(to)
}

func (r *Replay) lowestHeldButton() (int, bool) {
	lowest, found := 0, false
	for button := range r.heldButtons {
		if !found || button < lowest {
			lowest, found = button, true
		}
	}
	return lowest, found
}

func (r *Replay) flags() Flags {
	var flags Flags
	for _, flag := range r.heldModifiers {
		flags |= flag
	}
	return flags
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
