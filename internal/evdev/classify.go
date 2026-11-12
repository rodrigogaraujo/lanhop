//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package evdev

import (
	"unsafe"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const (
	keyCodeCount      = 0x300
	relativeAxisCount = 0x10
)

type Kind uint8

const (
	Other    Kind = 0
	Keyboard Kind = 1 << 0
	Mouse    Kind = 1 << 1
)

func (k Kind) String() string {
	switch k {
	case Keyboard:
		return "keyboard"
	case Mouse:
		return "mouse"
	case Keyboard | Mouse:
		return "keyboard+mouse"
	}
	return "other"
}

type inputID struct {
	bus     uint16
	vendor  uint16
	product uint16
	version uint16
}

var deviceID = request(directionRead, 'E', 0x02, unsafe.Sizeof(inputID{}))

type codeSet []byte

func (s codeSet) has(code uint16) bool {
	return s[code/8]&(1<<(code%8)) != 0
}

func (s codeSet) hasAll(codes ...uint16) bool {
	for _, code := range codes {
		if !s.has(code) {
			return false
		}
	}
	return true
}

func (d *Device) supportedCodes(eventType uint16, codeCount int) (codeSet, error) {
	codes := make(codeSet, codeCount/8)
	supported := request(directionRead, 'E', 0x20+uintptr(eventType), uintptr(len(codes)))
	if err := ioctlPointer(d.file, supported, unsafe.Pointer(&codes[0])); err != nil {
		return nil, err
	}
	return codes, nil
}

func (d *Device) kind() (Kind, error) {
	keys, err := d.supportedCodes(event.TypeKey, keyCodeCount)
	if err != nil {
		return Other, err
	}
	axes, err := d.supportedCodes(event.TypeRelative, relativeAxisCount)
	if err != nil {
		return Other, err
	}
	kind := Other
	if keys.hasAll(event.KeyA, event.KeyEnter) {
		kind |= Keyboard
	}
	if axes.hasAll(event.RelX, event.RelY) && keys.has(event.ButtonLeft) {
		kind |= Mouse
	}
	return kind, nil
}

func (d *Device) isOwn() (bool, error) {
	var id inputID
	if err := ioctlPointer(d.file, deviceID, unsafe.Pointer(&id)); err != nil {
		return false, err
	}
	return id.bus == busVirtual && id.vendor == virtualVendor && id.product == virtualProduct, nil
}
