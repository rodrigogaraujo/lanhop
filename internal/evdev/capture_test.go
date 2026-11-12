//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package evdev

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const (
	capturePatience = 5 * time.Second
	quietPeriod     = 200 * time.Millisecond
)

var (
	usbKeyboard = deviceSpec{name: "lanhop captured keyboard", id: someUSBDevice, keys: typingKeys}
	usbMouse    = deviceSpec{name: "lanhop captured mouse", id: someUSBDevice, keys: mouseButtons, relativeAxes: []uint16{event.RelX, event.RelY, event.RelWheel}}
	usbTouchpad = deviceSpec{name: "lanhop ignored touchpad", id: someUSBDevice, keys: []uint16{event.ButtonLeft, buttonTouch}, absoluteAxes: []uint16{absoluteX, absoluteY}}
)

func plugInto(t *testing.T, directory string, spec deviceSpec) (*VirtualDevice, string) {
	t.Helper()
	device := createDevice(t, spec)
	return device, makeNodeFromSysfs(t, directory, sysEventPath(t, device))
}

func startCaptureIn(t *testing.T, directory string) *Capture {
	t.Helper()
	capture, err := startCapture(filepath.Join(directory, "event*"))
	if err != nil {
		t.Fatalf("startCapture: %v", err)
	}
	t.Cleanup(func() { capture.Close() })
	return capture
}

func emit(t *testing.T, device *VirtualDevice, events ...event.Event) {
	t.Helper()
	if err := device.Emit(events...); err != nil {
		t.Fatalf("Emit: %v", err)
	}
}

func captured(t *testing.T, capture *Capture, count int) []event.Event {
	t.Helper()
	got := make([]event.Event, 0, count)
	for len(got) < count {
		select {
		case next, streaming := <-capture.Events():
			if !streaming {
				t.Fatalf("the stream ended after %+v", got)
			}
			got = append(got, next)
		case <-time.After(capturePatience):
			t.Fatalf("captured only %+v, want %d events", got, count)
		}
	}
	return got
}

func capturedUntil(t *testing.T, capture *Capture, last event.Event) {
	t.Helper()
	for captured(t, capture, 1)[0] != last {
	}
}

func expectCaptured(t *testing.T, capture *Capture, want ...event.Event) {
	t.Helper()
	if got := captured(t, capture, len(want)); !slices.Equal(got, want) {
		t.Errorf("captured %+v, want %+v", got, want)
	}
}

func pathsRead(capture *Capture) []string {
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	paths := make([]string, 0, len(capture.sources))
	for path := range capture.sources {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

func waitUntilRead(t *testing.T, capture *Capture, want ...string) {
	t.Helper()
	slices.Sort(want)
	for deadline := time.Now().Add(capturePatience); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if slices.Equal(pathsRead(capture), want) {
			return
		}
	}
	t.Fatalf("the capture reads %v, want %v", pathsRead(capture), want)
}

func openAsAnotherReader(t *testing.T, node string) *Device {
	t.Helper()
	device, err := Open(node)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { device.Close() })
	return device
}

func readWithin(t *testing.T, device *Device, limit time.Duration) (event.Event, error) {
	t.Helper()
	if err := device.file.SetReadDeadline(time.Now().Add(limit)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	return device.Read()
}

func isEndOfReport(e event.Event) bool {
	return e.Type == event.TypeSync && e.Code == event.SyncReport
}

func TestEventsOfEveryKeyboardAndMouseArriveInOneStream(t *testing.T) {
	directory := t.TempDir()
	keyboard, _ := plugInto(t, directory, usbKeyboard)
	mouse, _ := plugInto(t, directory, usbMouse)
	capture := startCaptureIn(t, directory)

	emit(t, keyboard, event.Key(event.KeyA, event.KeyPressed), event.Sync())
	emit(t, mouse, event.Relative(event.RelX, 7), event.Relative(event.RelY, -3), event.Sync())
	emit(t, keyboard, event.Key(event.KeyA, event.KeyReleased), event.Sync())

	var keys, movement, reportEnds []event.Event
	for _, got := range captured(t, capture, 7) {
		switch got.Type {
		case event.TypeKey:
			keys = append(keys, got)
		case event.TypeRelative:
			movement = append(movement, got)
		default:
			reportEnds = append(reportEnds, got)
		}
	}
	if want := []event.Event{event.Key(event.KeyA, event.KeyPressed), event.Key(event.KeyA, event.KeyReleased)}; !slices.Equal(keys, want) {
		t.Errorf("captured keys %+v, want %+v", keys, want)
	}
	if want := []event.Event{event.Relative(event.RelX, 7), event.Relative(event.RelY, -3)}; !slices.Equal(movement, want) {
		t.Errorf("captured movement %+v, want %+v", movement, want)
	}
	if want := []event.Event{event.Sync(), event.Sync(), event.Sync()}; !slices.Equal(reportEnds, want) {
		t.Errorf("captured %+v besides keys and movement, want %+v", reportEnds, want)
	}
}

func TestTakingTheInputAwayHidesItFromOtherReadersUntilItIsGivenBack(t *testing.T) {
	directory := t.TempDir()
	keyboard, node := plugInto(t, directory, usbKeyboard)
	capture := startCaptureIn(t, directory)
	desktop := openAsAnotherReader(t, node)

	if err := capture.Take(); err != nil {
		t.Fatalf("Take: %v", err)
	}
	emit(t, keyboard, event.Key(event.KeyA, event.KeyPressed), event.Sync())
	expectCaptured(t, capture, event.Key(event.KeyA, event.KeyPressed), event.Sync())
	if seen, err := readWithin(t, desktop, quietPeriod); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("another reader got %+v, %v while the input was taken away", seen, err)
	}

	for range 2 {
		if err := capture.GiveBack(); err != nil {
			t.Fatalf("GiveBack: %v", err)
		}
	}
	emit(t, keyboard, event.Key(event.KeyA, event.KeyReleased), event.Sync())
	expectCaptured(t, capture, event.Key(event.KeyA, event.KeyReleased), event.Sync())
	if seen, err := readWithin(t, desktop, capturePatience); err != nil || seen != event.Key(event.KeyA, event.KeyReleased) {
		t.Errorf("another reader got %+v, %v after the input was given back, want the release of A", seen, err)
	}
}

func TestDevicePluggedInAfterTheStartIsCaptured(t *testing.T) {
	directory := t.TempDir()
	capture := startCaptureIn(t, directory)

	mouse, node := plugInto(t, directory, usbMouse)
	waitUntilRead(t, capture, node)
	emit(t, mouse, event.Relative(event.RelX, 3), event.Sync())

	expectCaptured(t, capture, event.Relative(event.RelX, 3), event.Sync())
}

func TestDevicePluggedInWhileTheInputIsTakenAwayIsTakenAwayToo(t *testing.T) {
	directory := t.TempDir()
	capture := startCaptureIn(t, directory)
	if err := capture.Take(); err != nil {
		t.Fatalf("Take: %v", err)
	}

	_, node := plugInto(t, directory, usbKeyboard)
	waitUntilRead(t, capture, node)
	desktop := openAsAnotherReader(t, node)

	if err := desktop.Grab(); !errors.Is(err, syscall.EBUSY) {
		t.Errorf("another reader grabbing the new device got %v, want EBUSY", err)
	}
	if err := capture.GiveBack(); err != nil {
		t.Fatalf("GiveBack: %v", err)
	}
	if err := desktop.Grab(); err != nil {
		t.Errorf("another reader grabbing the new device after the input was given back: %v", err)
	}
}

func TestUnpluggedDeviceIsDroppedWithItsKeysReleasedAndTheOthersKeepWorking(t *testing.T) {
	directory := t.TempDir()
	keyboard, keyboardNode := plugInto(t, directory, usbKeyboard)
	mouse, mouseNode := plugInto(t, directory, usbMouse)
	capture := startCaptureIn(t, directory)
	waitUntilRead(t, capture, keyboardNode, mouseNode)
	emit(t, keyboard, event.Key(event.KeyA, event.KeyPressed), event.Sync())
	expectCaptured(t, capture, event.Key(event.KeyA, event.KeyPressed), event.Sync())

	if err := keyboard.Close(); err != nil {
		t.Fatalf("unplug the keyboard: %v", err)
	}

	released := captured(t, capture, 2)
	if released[0] != event.Key(event.KeyA, event.KeyReleased) || !isEndOfReport(released[1]) {
		t.Errorf("captured %+v after the keyboard went away, want the release of A and the end of the report", released)
	}
	waitUntilRead(t, capture, mouseNode)
	emit(t, mouse, event.Relative(event.RelX, 5), event.Sync())
	expectCaptured(t, capture, event.Relative(event.RelX, 5), event.Sync())
}

func TestDevicePluggedInUnderTheNodeOfOneWhoseLossWasNotNoticedYetIsCaptured(t *testing.T) {
	directory := t.TempDir()
	unplugged, node := plugInto(t, directory, usbKeyboard)
	capture := startCaptureIn(t, directory)
	emit(t, unplugged, event.Key(event.KeyA, event.KeyPressed), event.Sync())
	expectCaptured(t, capture, event.Key(event.KeyA, event.KeyPressed))
	time.Sleep(quietPeriod)

	if err := unplugged.Close(); err != nil {
		t.Fatalf("unplug the keyboard: %v", err)
	}
	replacement, appearedElsewhere := plugInto(t, t.TempDir(), usbKeyboard)
	if err := os.Rename(appearedElsewhere, node); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := os.Chmod(node, 0o660); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	time.Sleep(quietPeriod)

	capturedUntil(t, capture, event.Key(event.KeyA, event.KeyReleased))
	expectCaptured(t, capture, event.Sync())
	emit(t, replacement, event.Key(event.KeyA, event.KeyPressed), event.Sync())
	expectCaptured(t, capture, event.Key(event.KeyA, event.KeyPressed), event.Sync())
}

func TestOnlyKeyboardsAndMiceThatAreNotLanhopsOwnAreCaptured(t *testing.T) {
	directory := t.TempDir()
	plugInto(t, directory, usbTouchpad)
	makeNodeFromSysfs(t, directory, sysEventPath(t, createVirtualDevice(t)))
	keyboard, keyboardNode := plugInto(t, directory, usbKeyboard)
	mouse, mouseNode := plugInto(t, directory, usbMouse)
	capture := startCaptureIn(t, directory)

	shared := []string{keyboardNode, mouseNode}
	slices.Sort(shared)
	if got := pathsRead(capture); !slices.Equal(got, shared) {
		t.Errorf("the capture reads %v, want only %v", got, shared)
	}
	emit(t, keyboard, event.Key(event.KeyA, event.KeyPressed), event.Sync())
	expectCaptured(t, capture, event.Key(event.KeyA, event.KeyPressed), event.Sync())
	emit(t, mouse, event.Relative(event.RelY, 2), event.Sync())
	expectCaptured(t, capture, event.Relative(event.RelY, 2), event.Sync())
}

func TestNodesThatCannotBeOpenedOrQueriedDoNotStopTheCapture(t *testing.T) {
	directory := t.TempDir()
	keyboard, keyboardNode := plugInto(t, directory, usbKeyboard)
	gone, goneNode := plugInto(t, directory, usbMouse)
	if err := gone.Close(); err != nil {
		t.Fatalf("unplug: %v", err)
	}
	notADevice := filepath.Join(directory, "event9000")
	if err := os.WriteFile(notADevice, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := listMatching(filepath.Join(directory, "event*")); err == nil {
		t.Fatalf("%s and %s were expected to be nodes that cannot be described", goneNode, notADevice)
	}

	capture := startCaptureIn(t, directory)

	if got, want := pathsRead(capture), []string{keyboardNode}; !slices.Equal(got, want) {
		t.Errorf("the capture reads %v, want only %v", got, want)
	}
	emit(t, keyboard, event.Key(event.KeyA, event.KeyPressed), event.Sync())
	expectCaptured(t, capture, event.Key(event.KeyA, event.KeyPressed), event.Sync())
}

func TestNodeWhosePermissionsChangeAfterItAppearedIsCaptured(t *testing.T) {
	directory := t.TempDir()
	capture := startCaptureIn(t, directory)
	keyboard, appearedElsewhere := plugInto(t, t.TempDir(), usbKeyboard)
	node := filepath.Join(directory, filepath.Base(appearedElsewhere))
	if err := os.Rename(appearedElsewhere, node); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	if err := os.Chmod(node, 0o660); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	waitUntilRead(t, capture, node)
	emit(t, keyboard, event.Key(event.KeyA, event.KeyPressed), event.Sync())

	expectCaptured(t, capture, event.Key(event.KeyA, event.KeyPressed), event.Sync())
}

func TestClosingTheCaptureGivesTheInputBackAndEndsTheStream(t *testing.T) {
	directory := t.TempDir()
	_, node := plugInto(t, directory, usbKeyboard)
	capture := startCaptureIn(t, directory)
	desktop := openAsAnotherReader(t, node)
	if err := capture.Take(); err != nil {
		t.Fatalf("Take: %v", err)
	}
	if err := desktop.Grab(); !errors.Is(err, syscall.EBUSY) {
		t.Fatalf("another reader grabbing the device got %v, want EBUSY while the input is taken away", err)
	}

	if err := capture.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := desktop.Grab(); err != nil {
		t.Errorf("another reader grabbing the device after the capture closed: %v", err)
	}
	select {
	case got, streaming := <-capture.Events():
		if streaming {
			t.Errorf("captured %+v after the capture closed", got)
		}
	case <-time.After(capturePatience):
		t.Error("the stream did not end")
	}
}

func TestCaptureDoesNotStartWithoutTheDirectoryOfTheDevices(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")

	capture, err := startCapture(filepath.Join(missing, "event*"))

	if err == nil {
		capture.Close()
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("startCapture = %v, want an error that says %s does not exist", err, missing)
	}
}

func TestWhatEndsTheStreamOfADeviceThatWentAway(t *testing.T) {
	pressA := event.Key(event.KeyA, event.KeyPressed)
	releaseA := event.Key(event.KeyA, event.KeyReleased)
	pressLeft := event.Key(event.ButtonLeft, event.KeyPressed)
	tests := []struct {
		name      string
		readSoFar []event.Event
		want      []event.Event
	}{
		{"nothing was read", nil, nil},
		{"every key was released and the report ended", []event.Event{pressA, event.Sync(), releaseA, event.Sync()}, nil},
		{"movement ended with its report", []event.Event{event.Relative(event.RelX, 3), event.Sync()}, nil},
		{"a key and a button are held", []event.Event{pressA, event.Sync(), pressLeft, event.Sync()}, []event.Event{releaseA, event.Key(event.ButtonLeft, event.KeyReleased), event.Sync()}},
		{"a key is held in a report that did not end", []event.Event{pressA}, []event.Event{releaseA, event.Sync()}},
		{"a release was read without the end of its report", []event.Event{pressA, event.Sync(), releaseA}, []event.Event{event.Sync()}},
		{"movement was read without the end of its report", []event.Event{event.Relative(event.RelX, 3)}, []event.Event{event.Sync()}},
		{"the report ended with the sync the kernel sends when a device goes away", []event.Event{pressA, event.Sync(), releaseA, {Type: event.TypeSync, Code: event.SyncReport, Value: 1}}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gone source
			for _, read := range test.readSoFar {
				gone.observe(read)
			}

			if got := gone.endOfInterruptedReport(); !slices.Equal(got, test.want) {
				t.Errorf("the stream ends with %+v, want %+v", got, test.want)
			}
		})
	}
}
