//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package evdev

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/rodrigogaraujo/lanhop/internal/event"
)

const (
	sysClassInput = "/sys/class/input"
	writeAccess   = 2
)

var (
	deviceKeyState = request(directionRead, 'E', 0x18, keyCodeCount/8)

	ErrNotAllowed = errors.New("evdev: refused to write to that node")
)

type Node struct {
	Path       string
	SysPath    string
	Name       string
	Bus        uint16
	Vendor     uint16
	Product    uint16
	Kind       Kind
	LeftButton bool
	RelativeX  bool
	Own        bool
	LeftDown   bool
	Writable   bool
	Err        error
}

func Nodes() []Node {
	paths, _ := filepath.Glob(devicePattern)
	nodes := make([]Node, 0, len(paths))
	for _, path := range paths {
		nodes = append(nodes, InspectNode(path))
	}
	return nodes
}

func InspectNode(path string) Node {
	node := Node{Path: path, SysPath: sysPath(path), Writable: syscall.Access(path, writeAccess) == nil}
	device, err := Open(path)
	if err != nil {
		node.Err = err
		return node
	}
	defer device.Close()
	node.Err = device.inspect(&node)
	return node
}

func sysPath(path string) string {
	resolved, err := filepath.EvalSymlinks(filepath.Join(sysClassInput, filepath.Base(path)))
	if err != nil {
		return ""
	}
	return resolved
}

func (d *Device) inspect(node *Node) error {
	var err error
	if node.Name, err = d.Name(); err != nil {
		return err
	}
	var id inputID
	if err := ioctlPointer(d.file, deviceID, unsafe.Pointer(&id)); err != nil {
		return err
	}
	node.Bus, node.Vendor, node.Product = id.bus, id.vendor, id.product
	keys, err := d.supportedCodes(event.TypeKey, keyCodeCount)
	if err != nil {
		return err
	}
	axes, err := d.supportedCodes(event.TypeRelative, relativeAxisCount)
	if err != nil {
		return err
	}
	node.LeftButton, node.RelativeX = keys.has(event.ButtonLeft), axes.has(event.RelX)
	if node.Kind, err = d.kind(); err != nil {
		return err
	}
	if node.Own, err = d.isOwn(); err != nil {
		return err
	}
	node.LeftDown, err = d.leftDown()
	return err
}

func (d *Device) leftDown() (bool, error) {
	state := make(codeSet, keyCodeCount/8)
	if err := ioctlPointer(d.file, deviceKeyState, unsafe.Pointer(&state[0])); err != nil {
		return false, err
	}
	return state.has(event.ButtonLeft), nil
}

func ReleaseLeft(path string, allowed func(Node) error) (Node, error) {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return Node{Path: path, Err: err}, err
	}
	device := &Device{file: file}
	defer device.Close()
	node := Node{Path: path, SysPath: sysPath(path), Writable: true}
	if err := device.inspect(&node); err != nil {
		node.Err = err
		return node, err
	}
	switch {
	case node.Kind&Keyboard != 0:
		return node, fmt.Errorf("%w: %s is also a keyboard", ErrNotAllowed, path)
	case !node.LeftButton || !node.LeftDown:
		return node, fmt.Errorf("%w: BTN_LEFT of %s is not down", ErrNotAllowed, path)
	}
	if err := allowed(node); err != nil {
		return node, fmt.Errorf("%w: %s: %v", ErrNotAllowed, path, err)
	}
	release := []rawEvent{toRaw(event.Key(event.ButtonLeft, event.KeyReleased)), toRaw(event.Sync())}
	buffer := unsafe.Slice((*byte)(unsafe.Pointer(&release[0])), len(release)*rawEventSize)
	if _, err := file.Write(buffer); err != nil {
		return node, err
	}
	node.LeftDown, node.Err = device.leftDown()
	return node, node.Err
}
