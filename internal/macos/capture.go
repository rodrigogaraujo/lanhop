package macos

import (
	"slices"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/keymap"
)

const (
	TypeLeftMouseDown     uint32 = 1
	TypeLeftMouseUp       uint32 = 2
	TypeRightMouseDown    uint32 = 3
	TypeRightMouseUp      uint32 = 4
	TypeMouseMoved        uint32 = 5
	TypeLeftMouseDragged  uint32 = 6
	TypeRightMouseDragged uint32 = 7
	TypeKeyDown           uint32 = 10
	TypeKeyUp             uint32 = 11
	TypeFlagsChanged      uint32 = 12
	TypeScrollWheel       uint32 = 22
	TypeOtherMouseDown    uint32 = 25
	TypeOtherMouseUp      uint32 = 26
	TypeOtherMouseDragged uint32 = 27
	TypeTapDisabled       uint32 = 0xFFFFFFFE
)

const Marker int64 = 0x6c68

const (
	macOSCapsLock    uint16 = 0x39
	highestButton           = int(event.ButtonLast - event.ButtonFirst)
	pointsPerLine           = 10.0
	sideBitsOfAnyKey Flags  = 0x207F
)

var sideBits = map[uint16]Flags{
	0x3B: 0x0001,
	0x38: 0x0002,
	0x3C: 0x0004,
	0x37: 0x0008,
	0x36: 0x0010,
	0x3A: 0x0020,
	0x3D: 0x0040,
	0x3E: 0x2000,
}

type Observation struct {
	Type             uint32
	KeyCode          uint16
	Flags            Flags
	At               Point
	DX, DY           int64
	Clicks           int64
	Button           int64
	ScrollVertical   int64
	ScrollHorizontal int64
	PointsVertical   float64
	PointsHorizontal float64
	Continuous       bool
	Repeat           bool
	Marker           int64
}

var modifiersInOrder = []uint16{0x3B, 0x38, 0x3A, 0x37, 0x3E, 0x3C, 0x3D, 0x36}

type Hardware interface {
	KeyDown(macOSKey uint16) bool
	ButtonDown(number int) bool
}

type translator struct {
	keys          keymap.Translator
	hardware      Hardware
	heldModifiers map[uint16]bool
	heldKeys      map[uint16]bool
	heldButtons   map[int]bool
	scrolledUp    float64
	scrolledRight float64
}

func newTranslator(keys keymap.Translator, hardware Hardware) *translator {
	return &translator{
		keys:          keys,
		hardware:      hardware,
		heldModifiers: make(map[uint16]bool),
		heldKeys:      make(map[uint16]bool),
		heldButtons:   make(map[int]bool),
	}
}

func (t *translator) translate(seen Observation) []event.Event {
	if seen.Type == TypeTapDisabled {
		return t.releaseAll()
	}
	events := t.modifiers(seen)
	switch seen.Type {
	case TypeKeyDown:
		if !seen.Repeat {
			events = append(events, t.keyDown(seen.KeyCode)...)
		}
	case TypeKeyUp:
		events = append(events, t.keyUp(seen.KeyCode)...)
	case TypeFlagsChanged:
		if seen.KeyCode == macOSCapsLock {
			events = append(events, t.tap(seen.KeyCode)...)
		}
	case TypeMouseMoved, TypeLeftMouseDragged, TypeRightMouseDragged, TypeOtherMouseDragged:
		events = append(events, report(relative(event.RelX, seen.DX), relative(event.RelY, seen.DY))...)
	case TypeLeftMouseDown, TypeRightMouseDown, TypeOtherMouseDown:
		events = append(events, t.buttonDown(int(seen.Button))...)
	case TypeLeftMouseUp, TypeRightMouseUp, TypeOtherMouseUp:
		events = append(events, t.buttonUp(int(seen.Button))...)
	case TypeScrollWheel:
		events = append(events, t.scroll(seen)...)
	}
	return events
}

func (t *translator) modifiers(seen Observation) []event.Event {
	var events []event.Event
	pressed := false
	sidesKnown := seen.Flags&sideBitsOfAnyKey != 0
	for _, key := range modifiersInOrder {
		held := t.heldModifiers[key]
		down := held
		switch {
		case seen.Flags&modifierFlags[key] == 0:
			down = false
		case sidesKnown:
			down = seen.Flags&sideBits[key] != 0
		case seen.Type == TypeFlagsChanged && seen.KeyCode == key:
			down = !held
		}
		if down == held {
			continue
		}
		t.heldModifiers[key] = down
		if down {
			pressed = true
			events = append(events, t.key(key, event.KeyPressed)...)
		} else {
			events = append(events, t.key(key, event.KeyReleased)...)
		}
	}
	if pressed {
		events = append(events, t.releaseWhatTheHardwareLetGo()...)
	}
	return events
}

func (t *translator) releaseWhatTheHardwareLetGo() []event.Event {
	if t.hardware == nil {
		return nil
	}
	var events []event.Event
	for _, key := range sortedHeld(t.heldKeys) {
		if !t.hardware.KeyDown(key) {
			events = append(events, t.keyUp(key)...)
		}
	}
	for _, button := range sortedHeld(t.heldButtons) {
		if !t.hardware.ButtonDown(button) {
			events = append(events, t.buttonUp(button)...)
		}
	}
	return events
}

func (t *translator) releaseAll() []event.Event {
	var events []event.Event
	for _, key := range sortedHeld(t.heldKeys) {
		events = append(events, t.keyUp(key)...)
	}
	for _, button := range sortedHeld(t.heldButtons) {
		events = append(events, t.buttonUp(button)...)
	}
	for _, key := range modifiersInOrder {
		if t.heldModifiers[key] {
			t.heldModifiers[key] = false
			events = append(events, t.key(key, event.KeyReleased)...)
		}
	}
	return events
}

func sortedHeld[K uint16 | int](held map[K]bool) []K {
	keys := make([]K, 0, len(held))
	for key, down := range held {
		if down {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

func (t *translator) key(macOSKey uint16, value int32) []event.Event {
	linuxKey, mapped := t.keys.ToLinux(macOSKey)
	if !mapped {
		return nil
	}
	return []event.Event{event.Key(linuxKey, value), event.Sync()}
}

func (t *translator) tap(macOSKey uint16) []event.Event {
	return append(t.key(macOSKey, event.KeyPressed), t.key(macOSKey, event.KeyReleased)...)
}

func (t *translator) keyDown(macOSKey uint16) []event.Event {
	if _, modifier := modifierFlags[macOSKey]; modifier {
		return nil
	}
	lostRelease := t.keyUp(macOSKey)
	pressed := t.key(macOSKey, event.KeyPressed)
	if pressed != nil {
		t.heldKeys[macOSKey] = true
	}
	return append(lostRelease, pressed...)
}

func (t *translator) keyUp(macOSKey uint16) []event.Event {
	if !t.heldKeys[macOSKey] {
		return nil
	}
	delete(t.heldKeys, macOSKey)
	return t.key(macOSKey, event.KeyReleased)
}

func (t *translator) buttonDown(number int) []event.Event {
	if number < 0 || number > highestButton {
		return nil
	}
	lostRelease := t.buttonUp(number)
	t.heldButtons[number] = true
	return append(lostRelease, event.Key(event.ButtonFirst+uint16(number), event.KeyPressed), event.Sync())
}

func (t *translator) buttonUp(number int) []event.Event {
	if !t.heldButtons[number] {
		return nil
	}
	delete(t.heldButtons, number)
	return []event.Event{event.Key(event.ButtonFirst+uint16(number), event.KeyReleased), event.Sync()}
}

func (t *translator) scroll(seen Observation) []event.Event {
	if !seen.Continuous {
		return report(relative(event.RelWheel, seen.ScrollVertical), relative(event.RelHWheel, -seen.ScrollHorizontal))
	}
	t.scrolledUp += seen.PointsVertical
	t.scrolledRight -= seen.PointsHorizontal
	vertical := int64(t.scrolledUp / pointsPerLine)
	horizontal := int64(t.scrolledRight / pointsPerLine)
	t.scrolledUp -= float64(vertical) * pointsPerLine
	t.scrolledRight -= float64(horizontal) * pointsPerLine
	return report(relative(event.RelWheel, vertical), relative(event.RelHWheel, horizontal))
}

func relative(axis uint16, delta int64) []event.Event {
	if delta == 0 {
		return nil
	}
	return []event.Event{event.Relative(axis, int32(delta))}
}

func report(parts ...[]event.Event) []event.Event {
	var events []event.Event
	for _, part := range parts {
		events = append(events, part...)
	}
	if len(events) == 0 {
		return nil
	}
	return append(events, event.Sync())
}

func notPostedByLanhop(seen Observation) bool {
	return seen.Marker != Marker
}

type pump struct {
	observed     <-chan Observation
	lostInput    <-chan struct{}
	stop         <-chan struct{}
	accepts      func(Observation) bool
	translator   *translator
	events       chan event.Event
	handOffLimit time.Duration
	checkEvery   time.Duration
	stillAllowed func() bool
	giveBack     func()
}

func (p *pump) run() {
	defer close(p.events)
	check := time.NewTicker(p.checkEvery)
	defer check.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-p.lostInput:
			p.giveBack()
			return
		case <-check.C:
			if !p.stillAllowed() {
				p.giveBack()
				return
			}
		case seen, open := <-p.observed:
			if !open {
				return
			}
			if seen.Type != TypeTapDisabled && !p.accepts(seen) {
				continue
			}
			if !p.handOff(p.translator.translate(seen)) {
				return
			}
		}
	}
}

func (p *pump) handOff(events []event.Event) bool {
	if len(events) == 0 {
		return true
	}
	tooLong := time.NewTimer(p.handOffLimit)
	defer tooLong.Stop()
	for _, translated := range events {
		select {
		case p.events <- translated:
		case <-p.stop:
			return false
		case <-tooLong.C:
			p.giveBack()
			return false
		}
	}
	return true
}
