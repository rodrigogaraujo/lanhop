//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package evdev

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"unsafe"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const (
	typeAbsolute uint16 = 0x03
	absoluteX    uint16 = 0x00
	absoluteY    uint16 = 0x01
	keyPower     uint16 = 116
	buttonTouch  uint16 = 0x14a
	busUSB       uint16 = 0x03
)

var (
	someUSBDevice = inputID{bus: busUSB, vendor: 0x1234, product: 0x5678, version: 1}
	lanhopsDevice = inputID{bus: busVirtual, vendor: virtualVendor, product: virtualProduct, version: 1}
	typingKeys    = codesBetween(event.KeyFirst, event.KeyLast)
	mouseButtons  = []uint16{event.ButtonLeft, event.ButtonRight, event.ButtonMiddle}
)

type absoluteAxisSetup struct {
	axis       uint16
	value      int32
	minimum    int32
	maximum    int32
	fuzz       int32
	flat       int32
	resolution int32
}

var uinputSetAbsoluteAxis = request(directionWrite, 'U', 4, unsafe.Sizeof(absoluteAxisSetup{}))

type deviceSpec struct {
	name         string
	id           inputID
	keys         []uint16
	relativeAxes []uint16
	absoluteAxes []uint16
}

func TestDevicesAreClassifiedByWhatTheyCanDo(t *testing.T) {
	tests := []struct {
		name string
		spec deviceSpec
		want Kind
	}{
		{
			name: "a device with the typing keys is a keyboard",
			spec: deviceSpec{keys: typingKeys},
			want: Keyboard,
		},
		{
			name: "relative X and Y with a left button is a mouse",
			spec: deviceSpec{keys: mouseButtons, relativeAxes: []uint16{event.RelX, event.RelY, event.RelWheel}},
			want: Mouse,
		},
		{
			name: "typing keys with a pointer is both a keyboard and a mouse",
			spec: deviceSpec{keys: append(mouseButtons, typingKeys...), relativeAxes: []uint16{event.RelX, event.RelY}},
			want: Keyboard | Mouse,
		},
		{
			name: "a lone power key is not a keyboard",
			spec: deviceSpec{keys: []uint16{keyPower}},
			want: Other,
		},
		{
			name: "Enter without letters is not a keyboard",
			spec: deviceSpec{keys: []uint16{event.KeyEnter, keyPower}},
			want: Other,
		},
		{
			name: "letters without Enter is not a keyboard",
			spec: deviceSpec{keys: []uint16{event.KeyA}},
			want: Other,
		},
		{
			name: "absolute axes with a left button is not a mouse",
			spec: deviceSpec{keys: []uint16{event.ButtonLeft, buttonTouch}, absoluteAxes: []uint16{absoluteX, absoluteY}},
			want: Other,
		},
		{
			name: "a wheel with a left button is not a mouse",
			spec: deviceSpec{keys: mouseButtons, relativeAxes: []uint16{event.RelWheel}},
			want: Other,
		},
		{
			name: "relative X without Y is not a mouse",
			spec: deviceSpec{keys: mouseButtons, relativeAxes: []uint16{event.RelX}},
			want: Other,
		},
		{
			name: "relative X and Y without a left button is not a mouse",
			spec: deviceSpec{keys: []uint16{event.ButtonRight}, relativeAxes: []uint16{event.RelX, event.RelY}},
			want: Other,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.spec.name = "lanhop classified device"
			test.spec.id = someUSBDevice
			node := eventNode(t, createDevice(t, test.spec))

			got, err := describe(node)
			if err != nil {
				t.Fatalf("describe: %v", err)
			}
			if want := (Info{Path: node, Name: test.spec.name, Kind: test.want}); got != want {
				t.Errorf("describe = %+v, want %+v", got, want)
			}
		})
	}
}

func TestOnlyLanhopsBusVendorAndProductMarkADeviceAsItsOwn(t *testing.T) {
	tests := []struct {
		name string
		id   inputID
		want bool
	}{
		{"lanhop's bus, vendor and product", lanhopsDevice, true},
		{"another version of lanhop's device", inputID{bus: busVirtual, vendor: virtualVendor, product: virtualProduct, version: 7}, true},
		{"another bus", inputID{bus: busUSB, vendor: virtualVendor, product: virtualProduct, version: 1}, false},
		{"another vendor", inputID{bus: busVirtual, vendor: 0x1234, product: virtualProduct, version: 1}, false},
		{"another product", inputID{bus: busVirtual, vendor: virtualVendor, product: 0x0002, version: 1}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := deviceSpec{name: "lanhop identified device", id: test.id, keys: typingKeys}

			got, err := describe(eventNode(t, createDevice(t, spec)))
			if err != nil {
				t.Fatalf("describe: %v", err)
			}
			if got.Own != test.want {
				t.Errorf("Own = %v, want %v", got.Own, test.want)
			}
		})
	}
}

func TestTheVirtualDeviceLanhopCreatesIsItsOwnKeyboardAndMouse(t *testing.T) {
	node := eventNode(t, createVirtualDevice(t))

	got, err := describe(node)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if want := (Info{Path: node, Name: virtualDeviceName, Kind: Keyboard | Mouse, Own: true}); got != want {
		t.Errorf("describe = %+v, want %+v", got, want)
	}
}

func TestListDescribesEveryDeviceThatMatches(t *testing.T) {
	directory := t.TempDir()
	keyboard := deviceSpec{name: "lanhop listed keyboard", id: someUSBDevice, keys: typingKeys}
	keyboardNode := makeNodeFromSysfs(t, directory, sysEventPath(t, createDevice(t, keyboard)))
	ownNode := makeNodeFromSysfs(t, directory, sysEventPath(t, createVirtualDevice(t)))

	listed, err := listMatching(filepath.Join(directory, "event*"))
	if err != nil {
		t.Fatalf("listMatching: %v", err)
	}

	got := map[string]Info{}
	for _, info := range listed {
		got[info.Path] = info
	}
	want := map[string]Info{
		keyboardNode: {Path: keyboardNode, Name: keyboard.name, Kind: Keyboard},
		ownNode:      {Path: ownNode, Name: virtualDeviceName, Kind: Keyboard | Mouse, Own: true},
	}
	if len(listed) != len(want) || !reflect.DeepEqual(got, want) {
		t.Errorf("listMatching = %+v, want %+v", listed, want)
	}
}

func TestKindsHaveNames(t *testing.T) {
	tests := []struct {
		kind Kind
		want string
	}{
		{Keyboard, "keyboard"},
		{Mouse, "mouse"},
		{Keyboard | Mouse, "keyboard+mouse"},
		{Other, "other"},
	}
	for _, test := range tests {
		if got := test.kind.String(); got != test.want {
			t.Errorf("Kind(%d).String() = %q, want %q", test.kind, got, test.want)
		}
	}
}

func createDevice(t *testing.T, spec deviceSpec) *VirtualDevice {
	t.Helper()
	file, err := os.OpenFile(uinputPath, os.O_WRONLY, 0)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		t.Skipf("uinput is not available here: %v", err)
	}
	if err != nil {
		t.Fatalf("open uinput: %v", err)
	}
	device := &VirtualDevice{file: file}
	t.Cleanup(func() { device.Close() })
	if err := spec.register(file); err != nil {
		t.Fatalf("register %q: %v", spec.name, err)
	}
	return device
}

func (s deviceSpec) register(file *os.File) error {
	if err := enableCodes(file, event.TypeKey, uinputSetKey, s.keys); err != nil {
		return err
	}
	if err := enableCodes(file, event.TypeRelative, uinputSetRel, s.relativeAxes); err != nil {
		return err
	}
	if err := enableAbsoluteAxes(file, s.absoluteAxes); err != nil {
		return err
	}
	setup := uinputSetup{busType: s.id.bus, vendor: s.id.vendor, product: s.id.product, version: s.id.version}
	copy(setup.name[:uinputNameLength-1], s.name)
	if err := ioctlPointer(file, uinputSetupCall, unsafe.Pointer(&setup)); err != nil {
		return err
	}
	return ioctlValue(file, uinputCreate, 0)
}

func enableCodes(file *os.File, eventType uint16, enable uintptr, codes []uint16) error {
	if len(codes) == 0 {
		return nil
	}
	if err := ioctlValue(file, uinputSetType, uintptr(eventType)); err != nil {
		return err
	}
	for _, code := range codes {
		if err := ioctlValue(file, enable, uintptr(code)); err != nil {
			return err
		}
	}
	return nil
}

func enableAbsoluteAxes(file *os.File, axes []uint16) error {
	if len(axes) == 0 {
		return nil
	}
	if err := ioctlValue(file, uinputSetType, uintptr(typeAbsolute)); err != nil {
		return err
	}
	for _, axis := range axes {
		setup := absoluteAxisSetup{axis: axis, maximum: 1023}
		if err := ioctlPointer(file, uinputSetAbsoluteAxis, unsafe.Pointer(&setup)); err != nil {
			return err
		}
	}
	return nil
}

func codesBetween(first, last uint16) []uint16 {
	codes := make([]uint16, 0, last-first+1)
	for code := first; code <= last; code++ {
		codes = append(codes, code)
	}
	return codes
}
