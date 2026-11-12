package hotkey

import (
	"fmt"
	"slices"
	"strings"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const (
	keyDigit1     uint16 = 2
	keyDigit9     uint16 = 10
	keyLeftCtrl   uint16 = 29
	keyLeftShift  uint16 = 42
	keyRightShift uint16 = 54
	keyLeftAlt    uint16 = 56
	keyRightCtrl  uint16 = 97
	keyRightAlt   uint16 = 100
	keyLeftSuper  uint16 = 125
	keyRightSuper uint16 = 126
)

const DefaultModifiers = "Ctrl+Alt"

var modifierKeys = map[string][]uint16{
	"ctrl":  {keyLeftCtrl, keyRightCtrl},
	"alt":   {keyLeftAlt, keyRightAlt},
	"shift": {keyLeftShift, keyRightShift},
	"super": {keyLeftSuper, keyRightSuper},
}

type Result struct {
	Swallow bool
	Switch  bool
	Target  int
}

type Detector struct {
	modifiers [][]uint16
	held      map[uint16]struct{}
	swallowed map[uint16]struct{}
	armed     int
}

func New(modifiers string) (*Detector, error) {
	names := strings.Split(modifiers, "+")
	groups := make([][]uint16, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		normalized := strings.ToLower(strings.TrimSpace(name))
		keys, known := modifierKeys[normalized]
		if !known {
			return nil, fmt.Errorf("hotkey: unknown modifier %q, use Ctrl, Alt, Shift or Super", name)
		}
		if seen[normalized] {
			return nil, fmt.Errorf("hotkey: modifier %q is repeated", name)
		}
		seen[normalized] = true
		groups = append(groups, keys)
	}
	return &Detector{
		modifiers: groups,
		held:      make(map[uint16]struct{}),
		swallowed: make(map[uint16]struct{}),
	}, nil
}

func (d *Detector) Feed(e event.Event) Result {
	if e.Type != event.TypeKey {
		return Result{}
	}
	switch e.Value {
	case event.KeyPressed:
		return d.pressed(e.Code)
	case event.KeyReleased:
		return d.released(e.Code)
	}
	_, swallowed := d.swallowed[e.Code]
	return Result{Swallow: swallowed}
}

func (d *Detector) pressed(code uint16) Result {
	if target, isDigit := digitTarget(code); isDigit && d.onlyModifiersHeld() {
		d.armed = target
		d.swallowed[code] = struct{}{}
		return Result{Swallow: true}
	}
	d.held[code] = struct{}{}
	if !d.isModifier(code) {
		d.armed = 0
	}
	return Result{}
}

func (d *Detector) released(code uint16) Result {
	_, swallowed := d.swallowed[code]
	delete(d.swallowed, code)
	delete(d.held, code)

	result := Result{Swallow: swallowed}
	if d.armed != 0 && len(d.held) == 0 && len(d.swallowed) == 0 {
		result.Switch = true
		result.Target = d.armed
		d.armed = 0
	}
	return result
}

func (d *Detector) onlyModifiersHeld() bool {
	for _, group := range d.modifiers {
		if !d.anyHeld(group) {
			return false
		}
	}
	for code := range d.held {
		if !d.isModifier(code) {
			return false
		}
	}
	return true
}

func (d *Detector) anyHeld(codes []uint16) bool {
	for _, code := range codes {
		if _, held := d.held[code]; held {
			return true
		}
	}
	return false
}

func (d *Detector) isModifier(code uint16) bool {
	for _, group := range d.modifiers {
		if slices.Contains(group, code) {
			return true
		}
	}
	return false
}

func digitTarget(code uint16) (int, bool) {
	if code < keyDigit1 || code > keyDigit9 {
		return 0, false
	}
	return int(code-keyDigit1) + 1, true
}
