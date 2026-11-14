//go:build linux

package main

import (
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/evdev"
)

func physicalMouse(number string) evdev.Node {
	return evdev.Node{
		Path:       "/dev/input/event" + number,
		SysPath:    "/sys/devices/pci0000:00/0000:00:14.0/usb1/1-2/1-2:1.0/0003:046D:C077.0001/input/input" + number + "/event" + number,
		Name:       "Logitech USB Optical Mouse",
		Bus:        0x03,
		Vendor:     0x046d,
		Product:    0xc077,
		Kind:       evdev.Mouse,
		LeftButton: true,
		RelativeX:  true,
		Writable:   true,
	}
}

func holding(node evdev.Node) evdev.Node {
	node.LeftDown = true
	return node
}

func paths(nodes []evdev.Node) []string {
	var found []string
	for _, node := range nodes {
		found = append(found, node.Path)
	}
	return found
}

func everyKindOfNodeWithTheButtonDown() []evdev.Node {
	lanhop := holding(physicalMouse("11"))
	lanhop.Name, lanhop.Bus, lanhop.Vendor, lanhop.Product, lanhop.Own = "lanhop", 0x06, 0x6c68, 0x0001, true
	lanhop.SysPath = "/sys/devices/virtual/input/input11/event11"
	uinput := holding(physicalMouse("12"))
	uinput.Name, uinput.SysPath = "ydotoold virtual device", "/sys/devices/virtual/input/input12/event12"
	virtualBus := holding(physicalMouse("13"))
	virtualBus.Bus = 0x06
	combo := holding(physicalMouse("14"))
	combo.Name, combo.Kind = "Logitech K400 Plus", evdev.Keyboard|evdev.Mouse
	locked := holding(physicalMouse("15"))
	locked.Writable = false
	touchpad := holding(physicalMouse("16"))
	touchpad.Name, touchpad.RelativeX = "SynPS/2 Synaptics TouchPad", false
	keyboard := physicalMouse("17")
	keyboard.Name, keyboard.Kind, keyboard.LeftButton, keyboard.RelativeX = "AT Translated Set 2 keyboard", evdev.Keyboard, false, false
	unreadable := evdev.Node{Path: "/dev/input/event18", Err: &fs.PathError{Op: "open", Path: "/dev/input/event18", Err: syscall.EACCES}}
	noSysfs := holding(physicalMouse("19"))
	noSysfs.SysPath = ""
	return []evdev.Node{physicalMouse("4"), holding(physicalMouse("5")), lanhop, uinput, virtualBus, combo, locked, touchpad, keyboard, unreadable, noSysfs}
}

func TestOnlyThePhysicalMouseThatHoldsTheButtonIsChosen(t *testing.T) {
	verdicts := judge(everyKindOfNodeWithTheButtonDown())

	if got := paths(chosen(verdicts)); !slices.Equal(got, []string{"/dev/input/event5"}) {
		t.Errorf("chosen %v, want only /dev/input/event5", got)
	}
	want := map[string]string{
		"/dev/input/event4":  leftUp,
		"/dev/input/event11": ownDevice,
		"/dev/input/event12": virtualDevice,
		"/dev/input/event13": virtualDevice,
		"/dev/input/event14": alsoKeyboard,
		"/dev/input/event15": notWritable,
		"/dev/input/event16": notAPointer,
		"/dev/input/event17": notAPointer,
		"/dev/input/event19": unknownSysPath,
	}
	for _, verdict := range verdicts {
		if reason, listed := want[verdict.node.Path]; listed && verdict.refusal != reason {
			t.Errorf("%s refused for %q, want %q", verdict.node.Path, verdict.refusal, reason)
		}
	}
	if reason := refusal(everyKindOfNodeWithTheButtonDown()[9]); !strings.HasPrefix(reason, "not read:") {
		t.Errorf("an unreadable node refused for %q", reason)
	}
}

func TestABluetoothMouseBehindUhidIsPhysical(t *testing.T) {
	mouse := holding(physicalMouse("20"))
	mouse.Bus, mouse.SysPath = 0x05, "/sys/devices/virtual/misc/uhid/0005:046D:B023.0004/input/input20/event20"

	if reason := refusal(mouse); reason != "" {
		t.Errorf("a Bluetooth mouse through uhid was refused: %s", reason)
	}
}

func TestNothingIsChosenWhenNoPhysicalPointerHoldsTheButton(t *testing.T) {
	nodes := everyKindOfNodeWithTheButtonDown()
	nodes[1].LeftDown = false

	if got := chosen(judge(nodes)); len(got) != 0 {
		t.Errorf("chosen %v with no physical button down", paths(got))
	}
}

func TestTheStartNeedsAPhysicalPointerThisUserCanWriteTo(t *testing.T) {
	usable, err := preflight(judge(everyKindOfNodeWithTheButtonDown()))
	if err != nil || !slices.Equal(paths(usable), []string{"/dev/input/event4", "/dev/input/event5"}) {
		t.Errorf("preflight = %v, %v; want event4 and event5", paths(usable), err)
	}

	locked := physicalMouse("4")
	locked.Writable = false
	keyboard := physicalMouse("17")
	keyboard.LeftButton = false
	if _, err := preflight(judge([]evdev.Node{locked, keyboard})); !errors.Is(err, errNoAccess) {
		t.Errorf("with a pointer it cannot write to: %v, want %v", err, errNoAccess)
	}

	denied := evdev.Node{Path: "/dev/input/event3", Err: &fs.PathError{Op: "open", Path: "/dev/input/event3", Err: syscall.EACCES}}
	if _, err := preflight(judge([]evdev.Node{denied, denied})); !errors.Is(err, errNoAccess) {
		t.Errorf("with every node denied: %v, want %v", err, errNoAccess)
	}

	lanhop := physicalMouse("11")
	lanhop.Own = true
	missing := evdev.Node{Path: "/dev/input/event9", Err: &fs.PathError{Op: "open", Path: "/dev/input/event9", Err: os.ErrNotExist}}
	if _, err := preflight(judge([]evdev.Node{lanhop, keyboard, missing})); !errors.Is(err, errNoPointer) {
		t.Errorf("with no physical pointer: %v, want %v", err, errNoPointer)
	}
}

func TestTheDroppedAddressesBecomeLocalPaths(t *testing.T) {
	list := "# from Nautilus\r\nfile:///home/someone/drag-scratch/throw%20away.txt\r\nhttps://example.org/a\r\nfile:///tmp/b\r\n"

	if got := localPaths(list); !slices.Equal(got, []string{"/home/someone/drag-scratch/throw away.txt", "/tmp/b"}) {
		t.Errorf("localPaths = %q", got)
	}
}
