//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package evdev

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const (
	capturePatience = 5 * time.Second
	quietPeriod     = 200 * time.Millisecond
)

const (
	keyDigit1   uint16 = 2
	keyDigit2   uint16 = 3
	keyLeftCtrl uint16 = 29
	keyLeftAlt  uint16 = 56
)

var (
	usbKeyboard = deviceSpec{name: "lanhop captured keyboard", id: someUSBDevice, keys: typingKeys}
	usbMouse    = deviceSpec{name: "lanhop captured mouse", id: someUSBDevice, keys: mouseButtons, relativeAxes: []uint16{event.RelX, event.RelY, event.RelWheel}}
	usbTouchpad = deviceSpec{name: "lanhop ignored touchpad", id: someUSBDevice, keys: []uint16{event.ButtonLeft, buttonTouch}, absoluteAxes: []uint16{event.AbsX, event.AbsY}}
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

var keysSafeToPressOnTheMachineRunningTheTests = []uint16{event.KeyA, event.ButtonLeft, keyLeftCtrl, keyLeftAlt, keyDigit1, keyDigit2}

func firstUnsafeEvent(events []event.Event) (event.Event, bool) {
	for _, e := range events {
		switch e.Type {
		case event.TypeSync, event.TypeRelative:
		case event.TypeKey:
			if !slices.Contains(keysSafeToPressOnTheMachineRunningTheTests, e.Code) {
				return e, true
			}
		default:
			return e, true
		}
	}
	return event.Event{}, false
}

type emitter interface {
	Emit(events ...event.Event) error
}

func emit(t *testing.T, device emitter, events ...event.Event) {
	t.Helper()
	if unsafe, found := firstEventUnsafeOn(t, device, events); found {
		t.Fatalf("refusing to emit %+v: the machine running the tests would act on it", unsafe)
	}
	if err := device.Emit(events...); err != nil {
		t.Fatalf("Emit: %v", err)
	}
}

func firstEventUnsafeOn(t *testing.T, device emitter, events []event.Event) (event.Event, bool) {
	t.Helper()
	if !slices.ContainsFunc(events, isPosition) || !isGrabbed(t, deviceReachedByPositions(t, device)) {
		return firstUnsafeEvent(events)
	}
	if unsafe, found := firstUnsafeEvent(slices.DeleteFunc(slices.Clone(events), isPosition)); found {
		return unsafe, true
	}
	return positionLeftInAnOpenReport(events)
}

func positionLeftInAnOpenReport(events []event.Event) (event.Event, bool) {
	for i := len(events) - 1; i >= 0 && !isEndOfReport(events[i]); i-- {
		if isPosition(events[i]) {
			return events[i], true
		}
	}
	return event.Event{}, false
}

func isPosition(e event.Event) bool {
	isPositionAxis := e.Code == event.AbsX || e.Code == event.AbsY
	return e.Type == event.TypeAbsolute && isPositionAxis && e.Value >= 0 && e.Value <= event.AbsoluteMax
}

func deviceReachedByPositions(t *testing.T, device emitter) *VirtualDevice {
	t.Helper()
	switch reached := device.(type) {
	case *VirtualDevice:
		return reached
	case *Replay:
		return reached.pointer
	}
	t.Fatalf("cannot tell which device the positions emitted through a %T reach", device)
	return nil
}

func isGrabbed(t *testing.T, virtual *VirtualDevice) bool {
	t.Helper()
	probe, err := Open(eventNode(t, virtual))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer probe.Close()
	switch err := probe.Grab(); {
	case err == nil:
		if err := probe.Release(); err != nil {
			t.Fatalf("Release: %v", err)
		}
		return false
	case errors.Is(err, syscall.EBUSY):
		return true
	default:
		t.Fatalf("Grab: %v", err)
		return false
	}
}

func hideFromTheDesktop(t *testing.T, virtual *VirtualDevice) *Device {
	t.Helper()
	onlyReader := openEventNode(t, virtual)
	if err := onlyReader.Grab(); err != nil {
		t.Fatalf("Grab: %v", err)
	}
	return onlyReader
}

func TestTestsMayPressOnlyTheLetterATheLeftButtonAndTheHotkeysOfTheFirstTwoMachines(t *testing.T) {
	const (
		lastKeyCode uint16 = 0x2ff
		letterA     uint16 = 30
		leftButton  uint16 = 0x110
		leftCtrl    uint16 = 29
		leftAlt     uint16 = 56
		digit1      uint16 = 2
		digit2      uint16 = 3
	)
	for code := uint16(0); code <= lastKeyCode; code++ {
		_, refused := firstUnsafeEvent([]event.Event{event.Key(code, event.KeyPressed)})
		safe := slices.Contains([]uint16{letterA, leftButton, leftCtrl, leftAlt, digit1, digit2}, code)
		if refused == safe {
			t.Errorf("key %d: refused = %v, want %v", code, refused, !safe)
		}
	}
}

func TestUnsafeEventsAreRefusedWhereverAndHoweverTheyAppear(t *testing.T) {
	const (
		typeSwitch   uint16 = 0x05
		keyDigit3    uint16 = 4
		keyF1        uint16 = 59
		keyRightCtrl uint16 = 97
		keyRightAlt  uint16 = 100
		keyDelete    uint16 = 111
	)
	safe := event.Key(event.KeyA, event.KeyPressed)
	ctrlAlt := []event.Event{event.Key(keyLeftCtrl, event.KeyPressed), event.Key(keyLeftAlt, event.KeyPressed)}
	for name, events := range map[string][]event.Event{
		"unsafe key after a safe one":         {safe, event.Sync(), event.Key(keyPower, event.KeyPressed)},
		"unsafe key before a safe one":        {event.Key(keyPower, event.KeyPressed), event.Sync(), safe},
		"unsafe key released":                 {event.Key(keyPower, event.KeyReleased)},
		"unsafe key repeated":                 {event.Key(keyPower, event.KeyRepeated)},
		"Delete after Ctrl and Alt":           append(slices.Clone(ctrlAlt), event.Key(keyDelete, event.KeyPressed)),
		"F1 after Ctrl and Alt":               append(slices.Clone(ctrlAlt), event.Key(keyF1, event.KeyPressed)),
		"hotkey of a third machine":           append(slices.Clone(ctrlAlt), event.Key(keyDigit3, event.KeyPressed)),
		"Ctrl and Alt of the right-hand side": {event.Key(keyRightCtrl, event.KeyPressed), event.Key(keyRightAlt, event.KeyPressed)},
		"switch such as the lid":              {{Type: typeSwitch, Code: 0, Value: 1}},
		"absolute position":                   {event.Absolute(event.AbsX, 10)},
		"control event":                       {event.Enter()},
	} {
		if _, refused := firstUnsafeEvent(events); !refused {
			t.Errorf("%s was allowed", name)
		}
	}
}

func TestSafeEventsAreAllowed(t *testing.T) {
	allowed := []event.Event{
		event.Key(event.KeyA, event.KeyPressed), event.Key(event.KeyA, event.KeyRepeated), event.Key(event.KeyA, event.KeyReleased),
		event.Key(event.ButtonLeft, event.KeyPressed), event.Key(event.ButtonLeft, event.KeyReleased),
		event.Key(keyLeftCtrl, event.KeyPressed), event.Key(keyLeftAlt, event.KeyPressed),
		event.Key(keyDigit1, event.KeyPressed), event.Key(keyDigit1, event.KeyReleased),
		event.Key(keyDigit2, event.KeyPressed), event.Key(keyDigit2, event.KeyReleased),
		event.Key(keyLeftAlt, event.KeyReleased), event.Key(keyLeftCtrl, event.KeyReleased),
		event.Relative(event.RelX, 7), event.Relative(event.RelY, -3), event.Relative(event.RelWheel, 1), event.Sync(),
	}
	if unsafe, refused := firstUnsafeEvent(allowed); refused {
		t.Errorf("%+v was refused", unsafe)
	}
}

func TestOnlyXAndYFromZeroTo65535CountAsPositions(t *testing.T) {
	const (
		axisZ        uint16 = 0x02
		axisPressure uint16 = 0x18
	)
	tests := []struct {
		name  string
		event event.Event
		want  bool
	}{
		{"the left edge", event.Absolute(event.AbsX, 0), true},
		{"the right edge", event.Absolute(event.AbsX, event.AbsoluteMax), true},
		{"the top edge", event.Absolute(event.AbsY, 0), true},
		{"the bottom edge", event.Absolute(event.AbsY, event.AbsoluteMax), true},
		{"left of the left edge", event.Absolute(event.AbsX, -1), false},
		{"below the bottom edge", event.Absolute(event.AbsY, event.AbsoluteMax+1), false},
		{"a third axis", event.Absolute(axisZ, 10), false},
		{"pressure", event.Absolute(axisPressure, 10), false},
		{"relative movement on X", event.Relative(event.RelX, 10), false},
		{"a key with the code of the X axis", event.Key(event.AbsX, event.KeyPressed), false},
		{"the end of a report", event.Sync(), false},
	}
	for _, test := range tests {
		if got := isPosition(test.event); got != test.want {
			t.Errorf("%s: isPosition(%+v) = %v, want %v", test.name, test.event, got, test.want)
		}
	}
}

func TestADeviceCountsAsGrabbedOnlyWhileAReaderHoldsIt(t *testing.T) {
	pointer := createPointer(t)

	for range 2 {
		if isGrabbed(t, pointer) {
			t.Fatal("a device that nobody grabbed counts as grabbed")
		}
	}
	onlyReader := hideFromTheDesktop(t, pointer)
	if !isGrabbed(t, pointer) {
		t.Error("a device that a reader grabbed does not count as grabbed")
	}
	if err := onlyReader.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if isGrabbed(t, pointer) {
		t.Error("a device still counts as grabbed after its reader released it")
	}
}

func TestPositionsAreRefusedUntilTheDeviceTheyReachIsGrabbed(t *testing.T) {
	position := []event.Event{event.Absolute(event.AbsX, 10), event.Absolute(event.AbsY, event.AbsoluteMax), event.Sync()}
	tests := []struct {
		name   string
		create func(t *testing.T) (through emitter, reached *VirtualDevice)
	}{
		{"emitted on the pointer", func(t *testing.T) (emitter, *VirtualDevice) {
			pointer := createPointer(t)
			return pointer, pointer
		}},
		{"emitted through the replay", func(t *testing.T) (emitter, *VirtualDevice) {
			replay := createReplay(t)
			return replay, replay.pointer
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			through, reached := test.create(t)

			if _, refused := firstEventUnsafeOn(t, through, position); !refused {
				t.Error("positions were allowed on a pointer that the desktop can read")
			}
			hideFromTheDesktop(t, reached)
			if unsafe, refused := firstEventUnsafeOn(t, through, position); refused {
				t.Errorf("%+v was refused on a pointer that only the test can read", unsafe)
			}
		})
	}
}

func TestGrabbingTheKeyboardAndMouseOfAReplayDoesNotAllowPositions(t *testing.T) {
	replay := createReplay(t)
	hideFromTheDesktop(t, replay.keyboardAndMouse)

	if _, refused := firstEventUnsafeOn(t, replay, []event.Event{event.Absolute(event.AbsX, 10)}); !refused {
		t.Error("positions were allowed while the desktop can read the pointer of the replay")
	}
}

func TestAGrabbedPointerAllowsPositionsAndNothingElseThatIsUnsafe(t *testing.T) {
	const (
		typeSwitch uint16 = 0x05
		axisZ      uint16 = 0x02
	)
	replay := createReplay(t)
	hideFromTheDesktop(t, replay.pointer)
	position := event.Absolute(event.AbsX, 10)

	allowed := []event.Event{
		position, event.Absolute(event.AbsY, 20), event.Key(event.KeyA, event.KeyPressed),
		event.Key(event.ButtonLeft, event.KeyPressed), event.Relative(event.RelX, 7), event.Sync(),
	}
	if unsafe, refused := firstEventUnsafeOn(t, replay, allowed); refused {
		t.Errorf("%+v was refused", unsafe)
	}
	for name, unsafe := range map[string]event.Event{
		"unsafe key":                 event.Key(keyPower, event.KeyPressed),
		"switch such as the lid":     {Type: typeSwitch, Code: 0, Value: 1},
		"position outside the range": event.Absolute(event.AbsX, event.AbsoluteMax+1),
		"axis that is not X or Y":    event.Absolute(axisZ, 10),
		"control event":              event.Enter(),
	} {
		for order, events := range map[string][]event.Event{
			"after a position":  {position, unsafe, event.Sync()},
			"before a position": {unsafe, position, event.Sync()},
		} {
			if got, refused := firstEventUnsafeOn(t, replay, events); !refused || got != unsafe {
				t.Errorf("%s %s: refused %+v, %v, want %+v refused", name, order, got, refused, unsafe)
			}
		}
	}
}

func TestAPositionIsLeftInAnOpenReportUnlessTheReportEndsAfterIt(t *testing.T) {
	var (
		placeX      = event.Absolute(event.AbsX, 10)
		placeY      = event.Absolute(event.AbsY, 20)
		pressLeft   = event.Key(event.ButtonLeft, event.KeyPressed)
		contactEnds = event.Event{Type: event.TypeSync, Code: syncMultitouch}
	)
	tests := []struct {
		name     string
		events   []event.Event
		leftOpen []event.Event
	}{
		{"nothing", nil, nil},
		{"no position", []event.Event{pressLeft}, nil},
		{"a position and the end of its report", []event.Event{placeX, placeY, event.Sync()}, nil},
		{"a button after the end of the report of a position", []event.Event{placeX, event.Sync(), pressLeft}, nil},
		{"a position alone", []event.Event{placeX}, []event.Event{placeX}},
		{"two positions", []event.Event{placeX, placeY}, []event.Event{placeY}},
		{"a position, then a button", []event.Event{placeX, pressLeft}, []event.Event{placeX}},
		{"a position after the end of another report", []event.Event{placeX, event.Sync(), placeY}, []event.Event{placeY}},
		{"a position, then a sync that does not end the report", []event.Event{placeX, contactEnds}, []event.Event{placeX}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var leftOpen []event.Event
			if position, found := positionLeftInAnOpenReport(test.events); found {
				leftOpen = append(leftOpen, position)
			}
			if !slices.Equal(leftOpen, test.leftOpen) {
				t.Errorf("left in an open report: %+v, want %+v", leftOpen, test.leftOpen)
			}
		})
	}
}

func TestAGrabbedPointerRefusesAPositionWhoseReportDoesNotEndInTheSameCall(t *testing.T) {
	leftOpen := event.Absolute(event.AbsY, 20)
	calls := map[string][]event.Event{
		"a position alone":                           {leftOpen},
		"a position, then a button":                  {leftOpen, event.Key(event.ButtonLeft, event.KeyPressed)},
		"a position after the end of another report": {event.Absolute(event.AbsX, 10), event.Sync(), leftOpen},
	}
	tests := []struct {
		name   string
		create func(t *testing.T) (through emitter, reached *VirtualDevice)
	}{
		{"emitted on the pointer", func(t *testing.T) (emitter, *VirtualDevice) {
			pointer := createPointer(t)
			return pointer, pointer
		}},
		{"emitted through the replay", func(t *testing.T) (emitter, *VirtualDevice) {
			replay := createReplay(t)
			return replay, replay.pointer
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			through, reached := test.create(t)
			hideFromTheDesktop(t, reached)

			for name, events := range calls {
				if got, refused := firstEventUnsafeOn(t, through, events); !refused || got != leftOpen {
					t.Errorf("%s: refused %+v, %v, want %+v refused", name, got, refused, leftOpen)
				}
			}
		})
	}
}

func TestTestsReachDevicesOnlyThroughTheGuardedHelper(t *testing.T) {
	const guardedHelper = "emit"
	files := token.NewFileSet()
	packages, err := parser.ParseDir(files, ".", func(file fs.FileInfo) bool { return strings.HasSuffix(file.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(packages) == 0 {
		t.Skip("the test files are not in the working directory")
	}
	guardedCalls := 0
	for _, parsed := range packages {
		for _, file := range parsed.Files {
			for _, declaration := range file.Decls {
				function, isFunction := declaration.(*ast.FuncDecl)
				if !isFunction || function.Body == nil {
					continue
				}
				ast.Inspect(function.Body, func(node ast.Node) bool {
					call, isCall := node.(*ast.CallExpr)
					if !isCall {
						return true
					}
					selector, isSelector := call.Fun.(*ast.SelectorExpr)
					if !isSelector || selector.Sel.Name != "Emit" {
						return true
					}
					if function.Name.Name == guardedHelper {
						guardedCalls++
						return true
					}
					t.Errorf("%s: %s calls Emit directly, use the %s helper", files.Position(call.Pos()), function.Name.Name, guardedHelper)
					return true
				})
			}
		}
	}
	if guardedCalls != 1 {
		t.Errorf("found %d Emit calls inside the %s helper, want exactly 1", guardedCalls, guardedHelper)
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
	makeNodeFromSysfs(t, directory, sysEventPath(t, createPointer(t)))
	plugInto(t, directory, deviceSpec{name: "lanhop keyboard with the identity of the pointer", id: lanhopsPointer, keys: typingKeys})
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
