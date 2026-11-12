//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64)

package evdev

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/keystate"
)

const (
	nodeAppearedOrChangedPermissions = syscall.IN_CREATE | syscall.IN_ATTRIB
	nodeChangesBufferLength          = 4096
)

var nodeNameLengthOffset = unsafe.Offsetof(syscall.InotifyEvent{}.Len)

type Capture struct {
	pattern string
	watcher *os.File
	events  chan event.Event
	closed  chan struct{}
	closing sync.Once
	running sync.WaitGroup
	mutex   sync.Mutex
	sources map[string]*source
	taken   bool
}

type source struct {
	device    *Device
	grabbed   bool
	held      keystate.Tracker
	midReport bool
}

func StartCapture() (*Capture, error) {
	return startCapture(devicePattern)
}

func startCapture(pattern string) (*Capture, error) {
	directory := filepath.Dir(pattern)
	watcher, err := watchDirectory(directory)
	if err != nil {
		return nil, fmt.Errorf("watch %s for new devices: %w", directory, err)
	}
	capture := &Capture{
		pattern: pattern,
		watcher: watcher,
		events:  make(chan event.Event),
		closed:  make(chan struct{}),
		sources: make(map[string]*source),
	}
	capture.running.Add(1)
	go capture.watch()

	present, err := filepath.Glob(pattern)
	if err != nil {
		capture.Close()
		return nil, err
	}
	for _, path := range present {
		capture.adopt(path)
	}
	return capture, nil
}

func watchDirectory(directory string) (*os.File, error) {
	descriptor, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	if _, err := syscall.InotifyAddWatch(descriptor, directory, nodeAppearedOrChangedPermissions); err != nil {
		syscall.Close(descriptor)
		return nil, err
	}
	return os.NewFile(uintptr(descriptor), directory), nil
}

func (c *Capture) Events() <-chan event.Event {
	return c.events
}

func (c *Capture) Take() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.taken = true
	for _, source := range c.sources {
		source.grab()
	}
	return nil
}

func (c *Capture) GiveBack() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.taken = false
	for _, source := range c.sources {
		source.release()
	}
	return nil
}

func (c *Capture) Close() error {
	c.closing.Do(func() {
		close(c.closed)
		c.watcher.Close()
		c.mutex.Lock()
		for path, source := range c.sources {
			source.device.Close()
			delete(c.sources, path)
		}
		c.mutex.Unlock()
		c.running.Wait()
		close(c.events)
	})
	return nil
}

func (c *Capture) watch() {
	defer c.running.Done()
	changes := make([]byte, nodeChangesBufferLength)
	for {
		length, err := c.watcher.Read(changes)
		if err != nil {
			return
		}
		for _, name := range changedNames(changes[:length]) {
			if matches, _ := filepath.Match(filepath.Base(c.pattern), name); matches {
				c.adopt(filepath.Join(filepath.Dir(c.pattern), name))
			}
		}
	}
}

func changedNames(changes []byte) []string {
	var names []string
	for len(changes) >= syscall.SizeofInotifyEvent {
		nameLength := binary.NativeEndian.Uint32(changes[nodeNameLengthOffset:])
		next := syscall.SizeofInotifyEvent + int(nameLength)
		if next > len(changes) {
			break
		}
		names = append(names, textBeforeNUL(changes[syscall.SizeofInotifyEvent:next]))
		changes = changes[next:]
	}
	return names
}

func (c *Capture) adopt(path string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if _, reading := c.sources[path]; reading || c.isClosed() {
		return
	}
	device, err := Open(path)
	if err != nil {
		return
	}
	if !device.isShared() {
		device.Close()
		return
	}
	adopted := &source{device: device}
	if c.taken {
		adopted.grab()
	}
	c.sources[path] = adopted
	c.running.Add(1)
	go c.read(path, adopted)
}

func (c *Capture) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

func (c *Capture) read(path string, from *source) {
	defer c.running.Done()
	for {
		captured, err := from.device.Read()
		if err != nil {
			c.abandon(path, from)
			return
		}
		from.observe(captured)
		if !c.deliver(captured) {
			return
		}
	}
}

func (c *Capture) abandon(path string, lost *source) {
	c.mutex.Lock()
	if c.sources[path] == lost {
		delete(c.sources, path)
	}
	lost.device.Close()
	c.mutex.Unlock()
	c.adopt(path)

	for _, ending := range lost.endOfInterruptedReport() {
		if !c.deliver(ending) {
			return
		}
	}
}

func (s *source) observe(captured event.Event) {
	s.held.Observe(captured)
	s.midReport = captured.Type != event.TypeSync
}

func (s *source) endOfInterruptedReport() []event.Event {
	if releases := s.held.ReleaseAll(); releases != nil {
		return releases
	}
	if s.midReport {
		return []event.Event{event.Sync()}
	}
	return nil
}

func (c *Capture) deliver(captured event.Event) bool {
	select {
	case c.events <- captured:
		return true
	case <-c.closed:
		return false
	}
}

func (s *source) grab() {
	if !s.grabbed {
		s.grabbed = s.device.Grab() == nil
	}
}

func (s *source) release() {
	if s.grabbed {
		s.device.Release()
		s.grabbed = false
	}
}

func (d *Device) isShared() bool {
	kind, err := d.kind()
	if err != nil || kind == Other {
		return false
	}
	own, err := d.isOwn()
	return err == nil && !own
}
