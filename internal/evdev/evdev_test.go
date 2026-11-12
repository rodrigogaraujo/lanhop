//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package evdev

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const virtualDeviceName = "lanhop test device"

func TestEventsEmittedByTheVirtualDeviceAreReadBack(t *testing.T) {
	virtual := createVirtualDevice(t)
	device := openEventNode(t, virtual)

	sent := []event.Event{
		event.Key(event.KeyA, event.KeyPressed),
		event.Sync(),
		event.Relative(event.RelX, 7),
		event.Relative(event.RelY, -3),
		event.Sync(),
		event.Key(event.ButtonLeft, event.KeyPressed),
		event.Key(event.ButtonLeft, event.KeyReleased),
		event.Key(event.KeyA, event.KeyReleased),
		event.Sync(),
	}
	emit(t, virtual, sent...)

	for i, want := range sent {
		got, err := device.Read()
		if err != nil {
			t.Fatalf("Read event %d: %v", i, err)
		}
		if got != want {
			t.Errorf("event %d = %+v, want %+v", i, got, want)
		}
	}
}

func TestVirtualDeviceReportsItsName(t *testing.T) {
	device := openEventNode(t, createVirtualDevice(t))

	name, err := device.Name()
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if name != virtualDeviceName {
		t.Errorf("Name = %q, want %q", name, virtualDeviceName)
	}
}

func TestGrabbedDeviceCannotBeGrabbedTwice(t *testing.T) {
	virtual := createVirtualDevice(t)
	first := openEventNode(t, virtual)
	second := openEventNode(t, virtual)

	if err := first.Grab(); err != nil {
		t.Fatalf("first Grab: %v", err)
	}
	if err := second.Grab(); !errors.Is(err, syscall.EBUSY) {
		t.Errorf("second Grab = %v, want EBUSY", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := second.Grab(); err != nil {
		t.Errorf("Grab after Release: %v", err)
	}
}

func createVirtualDevice(t *testing.T) *VirtualDevice {
	t.Helper()
	virtual, err := CreateVirtualDevice(virtualDeviceName)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		t.Skipf("uinput is not available here: %v", err)
	}
	if err != nil {
		t.Fatalf("CreateVirtualDevice: %v", err)
	}
	t.Cleanup(func() { virtual.Close() })
	return virtual
}

func openEventNode(t *testing.T, virtual *VirtualDevice) *Device {
	t.Helper()
	device, err := Open(eventNode(t, virtual))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { device.Close() })
	if err := device.file.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	return device
}

func eventNode(t *testing.T, virtual *VirtualDevice) string {
	t.Helper()
	sysPath := sysEventPath(t, virtual)
	node := filepath.Join("/dev/input", filepath.Base(sysPath))
	if _, err := os.Stat(node); err == nil {
		return node
	}
	return makeNodeFromSysfs(t, t.TempDir(), sysPath)
}

func sysEventPath(t *testing.T, virtual *VirtualDevice) string {
	t.Helper()
	sysName, err := virtual.SysName()
	if err != nil {
		t.Fatalf("SysName: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join("/sys/devices/virtual/input", sysName, "event*"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("event node of %s: matches=%v err=%v", sysName, matches, err)
	}
	return matches[0]
}

func makeNodeFromSysfs(t *testing.T, directory, sysPath string) string {
	t.Helper()
	numbers, err := os.ReadFile(filepath.Join(sysPath, "dev"))
	if err != nil {
		t.Fatalf("read device numbers: %v", err)
	}
	majorText, minorText, found := strings.Cut(strings.TrimSpace(string(numbers)), ":")
	if !found {
		t.Fatalf("unexpected device numbers %q", numbers)
	}
	major, majorErr := strconv.ParseUint(majorText, 10, 32)
	minor, minorErr := strconv.ParseUint(minorText, 10, 32)
	if majorErr != nil || minorErr != nil {
		t.Fatalf("unexpected device numbers %q", numbers)
	}
	node := filepath.Join(directory, filepath.Base(sysPath))
	if err := syscall.Mknod(node, syscall.S_IFCHR|0o600, int(deviceNumber(major, minor))); err != nil {
		t.Skipf("cannot create a device node here: %v", err)
	}
	return node
}

func deviceNumber(major, minor uint64) uint64 {
	return (major&0xfff)<<8 | (major&^0xfff)<<32 | minor&0xff | (minor&^0xff)<<12
}
