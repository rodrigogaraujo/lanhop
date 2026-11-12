//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package evdev

import (
	"fmt"
	"os"
	"unsafe"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const (
	uinputPath       = "/dev/uinput"
	uinputNameLength = 80
	sysNameLength    = 64
	busVirtual       = 0x06
	virtualVendor    = 0x6c68
	virtualProduct   = 0x0001
	pointerProduct   = 0x0002
)

type uinputSetup struct {
	busType      uint16
	vendor       uint16
	product      uint16
	version      uint16
	name         [uinputNameLength]byte
	ffEffectsMax uint32
}

type absoluteAxisInfo struct {
	value      int32
	minimum    int32
	maximum    int32
	fuzz       int32
	flat       int32
	resolution int32
}

type absoluteAxisSetup struct {
	axis uint16
	info absoluteAxisInfo
}

var (
	uinputCreate          = request(0, 'U', 1, 0)
	uinputDestroy         = request(0, 'U', 2, 0)
	uinputSetupCall       = request(directionWrite, 'U', 3, unsafe.Sizeof(uinputSetup{}))
	uinputSetAbsoluteAxis = request(directionWrite, 'U', 4, unsafe.Sizeof(absoluteAxisSetup{}))
	uinputSetType         = request(directionWrite, 'U', 100, unsafe.Sizeof(int32(0)))
	uinputSetKey          = request(directionWrite, 'U', 101, unsafe.Sizeof(int32(0)))
	uinputSetRel          = request(directionWrite, 'U', 102, unsafe.Sizeof(int32(0)))
	uinputSysName         = request(directionRead, 'U', 44, sysNameLength)
)

var (
	relativeAxes = []uint16{event.RelX, event.RelY, event.RelHWheel, event.RelWheel}
	positionAxes = []uint16{event.AbsX, event.AbsY}
)

type VirtualDevice struct {
	file *os.File
}

func CreateVirtualDevice(name string) (*VirtualDevice, error) {
	return createWith(name, virtualProduct, (*VirtualDevice).enableKeysButtonsAndRelativeAxes)
}

func createAbsolutePointer(name string) (*VirtualDevice, error) {
	return createWith(name, pointerProduct, (*VirtualDevice).enablePositionsAndPointerButtons)
}

func createWith(name string, product uint16, enableEvents func(*VirtualDevice) error) (*VirtualDevice, error) {
	file, err := os.OpenFile(uinputPath, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	device := &VirtualDevice{file: file}
	if err := device.register(name, product, enableEvents); err != nil {
		file.Close()
		return nil, fmt.Errorf("register virtual device: %w", err)
	}
	return device, nil
}

func (d *VirtualDevice) register(name string, product uint16, enableEvents func(*VirtualDevice) error) error {
	if err := enableEvents(d); err != nil {
		return err
	}
	setup := uinputSetup{busType: busVirtual, vendor: virtualVendor, product: product, version: 1}
	copy(setup.name[:uinputNameLength-1], name)
	if err := ioctlPointer(d.file, uinputSetupCall, unsafe.Pointer(&setup)); err != nil {
		return err
	}
	return ioctlValue(d.file, uinputCreate, 0)
}

func (d *VirtualDevice) enableKeysButtonsAndRelativeAxes() error {
	if err := d.enableTypes(event.TypeKey, event.TypeRelative); err != nil {
		return err
	}
	if err := d.enableRange(uinputSetKey, event.KeyFirst, event.KeyLast); err != nil {
		return err
	}
	if err := d.enableRange(uinputSetKey, event.ButtonFirst, event.ButtonLast); err != nil {
		return err
	}
	for _, axis := range relativeAxes {
		if err := ioctlValue(d.file, uinputSetRel, uintptr(axis)); err != nil {
			return err
		}
	}
	return nil
}

func (d *VirtualDevice) enablePositionsAndPointerButtons() error {
	if err := d.enableTypes(event.TypeKey, event.TypeAbsolute); err != nil {
		return err
	}
	if err := d.enableRange(uinputSetKey, event.ButtonLeft, event.ButtonMiddle); err != nil {
		return err
	}
	for _, axis := range positionAxes {
		setup := absoluteAxisSetup{axis: axis, info: absoluteAxisInfo{maximum: event.AbsoluteMax}}
		if err := ioctlPointer(d.file, uinputSetAbsoluteAxis, unsafe.Pointer(&setup)); err != nil {
			return err
		}
	}
	return nil
}

func (d *VirtualDevice) enableTypes(types ...uint16) error {
	for _, kind := range types {
		if err := ioctlValue(d.file, uinputSetType, uintptr(kind)); err != nil {
			return err
		}
	}
	return nil
}

func (d *VirtualDevice) enableRange(req uintptr, first, last uint16) error {
	for code := first; code <= last; code++ {
		if err := ioctlValue(d.file, req, uintptr(code)); err != nil {
			return err
		}
	}
	return nil
}

func (d *VirtualDevice) Emit(events ...event.Event) error {
	if len(events) == 0 {
		return nil
	}
	raw := make([]rawEvent, len(events))
	for i, e := range events {
		raw[i] = toRaw(e)
	}
	buffer := unsafe.Slice((*byte)(unsafe.Pointer(&raw[0])), len(raw)*rawEventSize)
	_, err := d.file.Write(buffer)
	return err
}

func (d *VirtualDevice) SysName() (string, error) {
	var buffer [sysNameLength]byte
	if err := ioctlPointer(d.file, uinputSysName, unsafe.Pointer(&buffer)); err != nil {
		return "", err
	}
	return textBeforeNUL(buffer[:]), nil
}

func (d *VirtualDevice) Close() error {
	destroyErr := ioctlValue(d.file, uinputDestroy, 0)
	if err := d.file.Close(); err != nil {
		return err
	}
	return destroyErr
}
