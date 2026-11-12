//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package evdev

import (
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const (
	deviceNameLength = 256
	devicePattern    = "/dev/input/event*"
)

var (
	deviceName = request(directionRead, 'E', 0x06, deviceNameLength)
	deviceGrab = request(directionWrite, 'E', 0x90, unsafe.Sizeof(int32(0)))
)

type Device struct {
	file *os.File
}

type Info struct {
	Path string
	Name string
}

func Open(path string) (*Device, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &Device{file: file}, nil
}

func List() ([]Info, error) {
	paths, err := filepath.Glob(devicePattern)
	if err != nil {
		return nil, err
	}
	devices := make([]Info, 0, len(paths))
	for _, path := range paths {
		info, err := describe(path)
		if err != nil {
			return nil, err
		}
		devices = append(devices, info)
	}
	return devices, nil
}

func describe(path string) (Info, error) {
	device, err := Open(path)
	if err != nil {
		return Info{}, err
	}
	defer device.Close()
	name, err := device.Name()
	if err != nil {
		return Info{}, err
	}
	return Info{Path: path, Name: name}, nil
}

func (d *Device) Name() (string, error) {
	var buffer [deviceNameLength]byte
	if err := ioctlPointer(d.file, deviceName, unsafe.Pointer(&buffer)); err != nil {
		return "", err
	}
	return textBeforeNUL(buffer[:]), nil
}

func (d *Device) Grab() error {
	return ioctlValue(d.file, deviceGrab, 1)
}

func (d *Device) Release() error {
	return ioctlValue(d.file, deviceGrab, 0)
}

func (d *Device) Read() (event.Event, error) {
	var raw rawEvent
	buffer := unsafe.Slice((*byte)(unsafe.Pointer(&raw)), rawEventSize)
	if _, err := io.ReadFull(d.file, buffer); err != nil {
		return event.Event{}, err
	}
	return raw.toEvent(), nil
}

func (d *Device) Close() error {
	return d.file.Close()
}
