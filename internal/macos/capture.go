package macos

import (
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
)

const (
	macOSCapsLock    uint16 = 0x39
	highestButton           = int64(event.ButtonLast - event.ButtonFirst)
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
	Autorepeat       bool
	Marker           int64
}

type translator struct {
	keys          keymap.Translator
	heldModifiers map[uint16]bool
	heldButtons   map[uint16]bool
	scrolledUp    float64
	scrolledRight float64
}

func newTranslator(keys keymap.Translator) *translator {
	return &translator{keys: keys, heldModifiers: make(map[uint16]bool), heldButtons: make(map[uint16]bool)}
}

func (t *translator) translate(seen Observation) []event.Event {
	switch seen.Type {
	case TypeKeyDown:
		if seen.Autorepeat {
			return nil
		}
		return t.key(seen.KeyCode, event.KeyPressed)
	case TypeKeyUp:
		return t.key(seen.KeyCode, event.KeyReleased)
	case TypeFlagsChanged:
		return t.modifier(seen.KeyCode, seen.Flags)
	case TypeMouseMoved, TypeLeftMouseDragged, TypeRightMouseDragged, TypeOtherMouseDragged:
		return report(relative(event.RelX, seen.DX), relative(event.RelY, seen.DY))
	case TypeLeftMouseDown, TypeRightMouseDown, TypeOtherMouseDown:
		return t.button(seen.Button, true)
	case TypeLeftMouseUp, TypeRightMouseUp, TypeOtherMouseUp:
		return t.button(seen.Button, false)
	case TypeScrollWheel:
		return t.scroll(seen)
	}
	return nil
}

func (t *translator) key(macOSKey uint16, value int32) []event.Event {
	linuxKey, mapped := t.keys.ToLinux(macOSKey)
	if !mapped {
		return nil
	}
	return []event.Event{event.Key(linuxKey, value), event.Sync()}
}

func (t *translator) modifier(macOSKey uint16, flags Flags) []event.Event {
	if macOSKey == macOSCapsLock {
		return append(t.key(macOSKey, event.KeyPressed), t.key(macOSKey, event.KeyReleased)...)
	}
	flag, known := modifierFlags[macOSKey]
	if !known {
		return nil
	}
	down := t.isDown(macOSKey, flag, flags)
	if down == t.heldModifiers[macOSKey] {
		return nil
	}
	t.heldModifiers[macOSKey] = down
	if down {
		return t.key(macOSKey, event.KeyPressed)
	}
	return t.key(macOSKey, event.KeyReleased)
}

func (t *translator) isDown(macOSKey uint16, flag, flags Flags) bool {
	if flags&flag == 0 {
		return false
	}
	if flags&sideBitsOfAnyKey != 0 {
		return flags&sideBits[macOSKey] != 0
	}
	return !t.heldModifiers[macOSKey]
}

func (t *translator) button(number int64, down bool) []event.Event {
	if number < 0 || number > highestButton {
		return nil
	}
	code := event.ButtonFirst + uint16(number)
	if down == t.heldButtons[code] {
		return nil
	}
	t.heldButtons[code] = down
	if down {
		return []event.Event{event.Key(code, event.KeyPressed), event.Sync()}
	}
	return []event.Event{event.Key(code, event.KeyReleased), event.Sync()}
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
