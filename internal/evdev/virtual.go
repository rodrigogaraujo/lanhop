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
)

type uinputSetup struct {
	busType      uint16
	vendor       uint16
	product      uint16
	version      uint16
	name         [uinputNameLength]byte
	ffEffectsMax uint32
}

var (
	uinputCreate    = request(0, 'U', 1, 0)
	uinputDestroy   = request(0, 'U', 2, 0)
	uinputSetupCall = request(directionWrite, 'U', 3, unsafe.Sizeof(uinputSetup{}))
	uinputSetType   = request(directionWrite, 'U', 100, unsafe.Sizeof(int32(0)))
	uinputSetKey    = request(directionWrite, 'U', 101, unsafe.Sizeof(int32(0)))
	uinputSetRel    = request(directionWrite, 'U', 102, unsafe.Sizeof(int32(0)))
	uinputSysName   = request(directionRead, 'U', 44, sysNameLength)
)

var relativeAxes = []uint16{event.RelX, event.RelY, event.RelHWheel, event.RelWheel}

type VirtualDevice struct {
	file *os.File
}

func CreateVirtualDevice(name string) (*VirtualDevice, error) {
	file, err := os.OpenFile(uinputPath, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	device := &VirtualDevice{file: file}
	if err := device.register(name); err != nil {
		file.Close()
		return nil, fmt.Errorf("register virtual device: %w", err)
	}
	return device, nil
}

func (d *VirtualDevice) register(name string) error {
	for _, kind := range []uint16{event.TypeKey, event.TypeRelative} {
		if err := ioctlValue(d.file, uinputSetType, uintptr(kind)); err != nil {
			return err
		}
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

	setup := uinputSetup{busType: busVirtual, vendor: virtualVendor, product: virtualProduct, version: 1}
	copy(setup.name[:uinputNameLength-1], name)
	if err := ioctlPointer(d.file, uinputSetupCall, unsafe.Pointer(&setup)); err != nil {
		return err
	}
	return ioctlValue(d.file, uinputCreate, 0)
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
