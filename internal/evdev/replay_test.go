//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package evdev

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
	"unsafe"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const (
	pointerDeviceName          = "lanhop test pointer"
	absoluteAxisCount          = 0x40
	virtualInputDevices        = "/sys/devices/virtual/input"
	readAbsoluteAxis           = 0x40
	syncMultitouch      uint16 = 0x02
)

func skipWithoutUinput(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		t.Skipf("uinput is not available here: %v", err)
	}
}

func createPointer(t *testing.T) *VirtualDevice {
	t.Helper()
	pointer, err := createAbsolutePointer(pointerDeviceName)
	skipWithoutUinput(t, err)
	if err != nil {
		t.Fatalf("createAbsolutePointer: %v", err)
	}
	t.Cleanup(func() { pointer.Close() })
	return pointer
}

func createReplay(t *testing.T) *Replay {
	t.Helper()
	replay, err := NewReplay(virtualDeviceName, pointerDeviceName)
	skipWithoutUinput(t, err)
	if err != nil {
		t.Fatalf("NewReplay: %v", err)
	}
	t.Cleanup(func() { replay.Close() })
	return replay
}

func axisInfo(t *testing.T, device *Device, axis uint16) absoluteAxisInfo {
	t.Helper()
	var info absoluteAxisInfo
	read := request(directionRead, 'E', readAbsoluteAxis+uintptr(axis), unsafe.Sizeof(info))
	if err := ioctlPointer(device.file, read, unsafe.Pointer(&info)); err != nil {
		t.Fatalf("read the range of axis %d: %v", axis, err)
	}
	return info
}

func supported(t *testing.T, device *Device, eventType uint16, codeCount int) []uint16 {
	t.Helper()
	codes, err := device.supportedCodes(eventType, codeCount)
	if err != nil {
		t.Fatalf("supportedCodes of type %d: %v", eventType, err)
	}
	var list []uint16
	for code := range uint16(codeCount) {
		if codes.has(code) {
			list = append(list, code)
		}
	}
	return list
}

func expectRead(t *testing.T, device *Device, want ...event.Event) {
	t.Helper()
	for i, wanted := range want {
		got, err := readWithin(t, device, capturePatience)
		if err != nil {
			t.Fatalf("Read event %d: %v", i, err)
		}
		if got != wanted {
			t.Errorf("event %d = %+v, want %+v", i, got, wanted)
		}
	}
}

func expectNothingToRead(t *testing.T, device *Device) {
	t.Helper()
	if got, err := readWithin(t, device, quietPeriod); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("read %+v, %v from a device that should have received nothing", got, err)
	}
}

func sysDevice(t *testing.T, virtual *VirtualDevice) string {
	t.Helper()
	sysName, err := virtual.SysName()
	if err != nil {
		t.Fatalf("SysName: %v", err)
	}
	return filepath.Join(virtualInputDevices, sysName)
}

func TestThePointerHasXAndYFromZeroTo65535AndThePointerButtonsAndNothingElse(t *testing.T) {
	device := openEventNode(t, createPointer(t))

	for _, axis := range []uint16{event.AbsX, event.AbsY} {
		if got, want := axisInfo(t, device, axis), (absoluteAxisInfo{minimum: 0, maximum: 65535}); got != want {
			t.Errorf("axis %d = %+v, want %+v", axis, got, want)
		}
	}
	if got, want := supported(t, device, event.TypeAbsolute, absoluteAxisCount), []uint16{event.AbsX, event.AbsY}; !slices.Equal(got, want) {
		t.Errorf("absolute axes = %v, want %v", got, want)
	}
	if got := supported(t, device, event.TypeKey, keyCodeCount); !slices.Equal(got, mouseButtons) {
		t.Errorf("keys and buttons = %v, want %v", got, mouseButtons)
	}
	if got := supported(t, device, event.TypeRelative, relativeAxisCount); len(got) != 0 {
		t.Errorf("relative axes = %v, want none", got)
	}
}

func TestPositionsAtBothEndsOfTheRangeAreReadBackUnchanged(t *testing.T) {
	pointer := createPointer(t)
	onlyReader := hideFromTheDesktop(t, pointer)

	sent := []event.Event{
		event.Absolute(event.AbsX, event.AbsoluteMax), event.Absolute(event.AbsY, event.AbsoluteMax), event.Sync(),
		event.Absolute(event.AbsX, 0), event.Absolute(event.AbsY, 0), event.Sync(),
		event.Absolute(event.AbsX, 32768), event.Sync(),
		event.Absolute(event.AbsY, 1), event.Sync(),
	}
	emit(t, pointer, sent...)

	expectRead(t, onlyReader, sent...)
}

func TestThePointerIsLanhopsOwnAndNeitherAKeyboardNorAMouse(t *testing.T) {
	pointer := createPointer(t)
	node := eventNode(t, pointer)

	got, err := describe(node)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if want := (Info{Path: node, Name: pointerDeviceName, Kind: Other, Own: true}); got != want {
		t.Errorf("describe = %+v, want %+v", got, want)
	}
	device := openEventNode(t, pointer)
	if device.isShared() {
		t.Error("the pointer would be shared with other machines")
	}
}

func TestEachEventGoesToItsDeviceAndTheEndOfAReportToEveryDeviceTheReportReached(t *testing.T) {
	const nowhere destination = 0
	var (
		pressA      = event.Key(event.KeyA, event.KeyPressed)
		pressLeft   = event.Key(event.ButtonLeft, event.KeyPressed)
		moveRight   = event.Relative(event.RelX, 7)
		scroll      = event.Relative(event.RelWheel, -1)
		placeX      = event.Absolute(event.AbsX, 100)
		placeY      = event.Absolute(event.AbsY, event.AbsoluteMax)
		deviceGone  = event.Event{Type: event.TypeSync, Code: event.SyncReport, Value: 1}
		contactEnds = event.Event{Type: event.TypeSync, Code: syncMultitouch}
	)
	type routed struct {
		event event.Event
		want  destination
	}
	tests := []struct {
		name   string
		events []routed
	}{
		{"a key", []routed{{pressA, toKeyboardAndMouse}, {event.Sync(), toKeyboardAndMouse}}},
		{"a button", []routed{{pressLeft, toKeyboardAndMouse}, {event.Sync(), toKeyboardAndMouse}}},
		{"relative movement and scrolling", []routed{{moveRight, toKeyboardAndMouse}, {scroll, toKeyboardAndMouse}, {event.Sync(), toKeyboardAndMouse}}},
		{"a position", []routed{{placeX, toPointer}, {placeY, toPointer}, {event.Sync(), toPointer}}},
		{"a position and a button in one report", []routed{{placeX, toPointer}, {pressLeft, toKeyboardAndMouse}, {event.Sync(), toKeyboardAndMouse | toPointer}}},
		{"a key and a position in one report", []routed{{pressA, toKeyboardAndMouse}, {placeY, toPointer}, {event.Sync(), toKeyboardAndMouse | toPointer}}},
		{"a position, then a key in the next report", []routed{{placeX, toPointer}, {event.Sync(), toPointer}, {pressA, toKeyboardAndMouse}, {event.Sync(), toKeyboardAndMouse}}},
		{"a key, then a position in the next report", []routed{{pressA, toKeyboardAndMouse}, {event.Sync(), toKeyboardAndMouse}, {placeX, toPointer}, {event.Sync(), toPointer}}},
		{"both kinds, then a position in the next report", []routed{{pressA, toKeyboardAndMouse}, {placeX, toPointer}, {event.Sync(), toKeyboardAndMouse | toPointer}, {placeY, toPointer}, {event.Sync(), toPointer}}},
		{"the end of a report that reached nothing", []routed{{event.Sync(), nowhere}}},
		{"the end of a report sent twice", []routed{{placeX, toPointer}, {event.Sync(), toPointer}, {event.Sync(), nowhere}}},
		{"the end of a report the kernel sends when a device goes away", []routed{{placeX, toPointer}, {deviceGone, toPointer}, {event.Sync(), nowhere}}},
		{"a sync that does not end the report", []routed{{placeX, toPointer}, {contactEnds, toKeyboardAndMouse}, {event.Sync(), toKeyboardAndMouse | toPointer}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var report reportRouter
			for i, step := range test.events {
				if got := report.route(step.event); got != step.want {
					t.Errorf("event %d (%+v) went to %02b, want %02b", i, step.event, got, step.want)
				}
			}
		})
	}
}

func TestTheReplayPutsPositionsOnThePointerAndEverythingElseOnTheKeyboardAndMouse(t *testing.T) {
	replay := createReplay(t)
	keyboardAndMouse := openEventNode(t, replay.keyboardAndMouse)
	pointer := hideFromTheDesktop(t, replay.pointer)

	position := []event.Event{
		event.Absolute(event.AbsX, event.AbsoluteMax), event.Absolute(event.AbsY, 1), event.Sync(),
		event.Absolute(event.AbsX, 0), event.Sync(),
	}
	emit(t, replay, position...)
	expectRead(t, pointer, position...)
	expectNothingToRead(t, keyboardAndMouse)

	everythingElse := []event.Event{
		event.Key(event.KeyA, event.KeyPressed), event.Sync(),
		event.Relative(event.RelX, 7), event.Relative(event.RelY, -3), event.Sync(),
		event.Relative(event.RelWheel, 1), event.Sync(),
		event.Key(event.ButtonLeft, event.KeyPressed), event.Sync(),
		event.Key(event.ButtonLeft, event.KeyReleased), event.Key(event.KeyA, event.KeyReleased), event.Sync(),
	}
	emit(t, replay, everythingElse...)
	expectRead(t, keyboardAndMouse, everythingElse...)
	expectNothingToRead(t, pointer)
}

func TestAReportWithAKeyAndAPositionEndsOnBothDevicesOfTheReplay(t *testing.T) {
	replay := createReplay(t)
	keyboardAndMouse := openEventNode(t, replay.keyboardAndMouse)
	pointer := hideFromTheDesktop(t, replay.pointer)

	for _, oneAtATime := range []event.Event{
		event.Key(event.KeyA, event.KeyPressed), event.Absolute(event.AbsX, 100), event.Absolute(event.AbsY, 200), event.Sync(),
		event.Key(event.KeyA, event.KeyReleased), event.Sync(),
	} {
		emit(t, replay, oneAtATime)
	}

	expectRead(t, keyboardAndMouse,
		event.Key(event.KeyA, event.KeyPressed), event.Sync(),
		event.Key(event.KeyA, event.KeyReleased), event.Sync(),
	)
	expectRead(t, pointer, event.Absolute(event.AbsX, 100), event.Absolute(event.AbsY, 200), event.Sync())
	expectNothingToRead(t, pointer)
}

func TestTheReplayCreatesItsTwoDevicesAndClosingItRemovesBoth(t *testing.T) {
	replay := createReplay(t)
	devices := map[string]string{
		virtualDeviceName: sysDevice(t, replay.keyboardAndMouse),
		pointerDeviceName: sysDevice(t, replay.pointer),
	}
	for name, device := range devices {
		contents, err := os.ReadFile(filepath.Join(device, "name"))
		if err != nil {
			t.Fatalf("the device %q does not exist while the replay is open: %v", name, err)
		}
		if got := string(contents); got != name+"\n" {
			t.Errorf("%s is named %q, want %q", device, got, name)
		}
	}

	if err := replay.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for name, device := range devices {
		for deadline := time.Now().Add(capturePatience); ; time.Sleep(time.Millisecond) {
			if _, err := os.Stat(device); errors.Is(err, os.ErrNotExist) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the device %q still exists after the replay closed", name)
			}
		}
	}
}
