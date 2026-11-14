//go:build linux

package x11

import (
	"encoding/binary"
	"slices"
	"sync"
	"testing"
	"time"
)

const (
	classInputOutput = 1
	xtestFakeInput   = 2
	relativeMotion   = 1
	absoluteMotion   = 0
)

func openDragsOn(t *testing.T, display string) *Drags {
	t.Helper()
	d, err := parseDisplay(display)
	if err != nil {
		t.Fatalf("parseDisplay: %v", err)
	}
	drags, err := openDrags(d, nil)
	if err != nil {
		t.Fatalf("openDrags %s: %v", display, err)
	}
	t.Cleanup(func() { drags.Close() })
	return drags
}

func dialForTest(t *testing.T, display string, onEvent func([]byte)) *conn {
	t.Helper()
	d, err := parseDisplay(display)
	if err != nil {
		t.Fatalf("parseDisplay: %v", err)
	}
	c, err := dial(d, nil, onEvent)
	if err != nil {
		t.Fatalf("dial %s: %v", display, err)
	}
	t.Cleanup(func() { c.close() })
	return c
}

type fakeInput struct {
	c      *conn
	opcode byte
}

func startFakeInput(t *testing.T, display string) *fakeInput {
	t.Helper()
	c := dialForTest(t, display, func([]byte) {})
	reply, err := c.call(newRequest(opQueryExtension, 0).u16(uint16(len("XTEST"))).u16(0).raw([]byte("XTEST")).done())
	if err != nil {
		t.Fatalf("QueryExtension XTEST: %v", err)
	}
	if reply[8] == 0 {
		t.Skip("the X server has no XTEST extension")
	}
	return &fakeInput{c: c, opcode: reply[9]}
}

func (f *fakeInput) fake(kind, detail byte, root uint32, x, y int) error {
	request := newRequest(f.opcode, xtestFakeInput).u8(kind).u8(detail).u16(0).u32(currentTime).u32(root).
		u32(0).u32(0).u16(uint16(int16(x))).u16(uint16(int16(y))).u32(0).u16(0).u8(0).u8(0).done()
	if err := f.c.send(request); err != nil {
		return err
	}
	_, err := f.c.call(newRequest(opQueryPointer, 0).u32(f.c.root).done())
	return err
}

func (f *fakeInput) moveTo(x, y int) error {
	return f.fake(eventMotionNotify, absoluteMotion, f.c.root, x, y)
}

func (f *fakeInput) moveBy(dx, dy int) error {
	return f.fake(eventMotionNotify, relativeMotion, atomNone, dx, dy)
}

func (f *fakeInput) press(button byte) error {
	return f.fake(eventButtonPress, button, atomNone, 0, 0)
}

func (f *fakeInput) release(button byte) error {
	return f.fake(eventButtonRelease, button, atomNone, 0, 0)
}

type targetBehaviour struct {
	accept bool
	finish bool
}

type xdndTarget struct {
	c         *conn
	window    uint32
	atoms     map[string]uint32
	behaviour targetBehaviour
	events    chan []byte
	dropped   chan string
	state     sync.Mutex
	seen      []string
	version   int
	types     []uint32
}

func startXdndTarget(t *testing.T, display string, x, y, width, height int, behaviour targetBehaviour) *xdndTarget {
	t.Helper()
	target := &xdndTarget{
		atoms:     make(map[string]uint32),
		behaviour: behaviour,
		events:    make(chan []byte, 1024),
		dropped:   make(chan string, 1),
	}
	target.c = dialForTest(t, display, func(event []byte) {
		select {
		case target.events <- event:
		default:
		}
	})
	for _, name := range []string{"XdndAware", "XdndEnter", "XdndPosition", "XdndStatus", "XdndLeave", "XdndDrop", "XdndFinished", "XdndSelection", "XdndActionCopy", "text/uri-list", "LANHOP_TEST_DROP"} {
		atom, err := internDragAtom(target.c, name)
		if err != nil {
			t.Fatalf("intern %s: %v", name, err)
		}
		target.atoms[name] = atom
	}
	target.window = target.c.newID()
	create := newRequest(opCreateWindow, 0).u32(target.window).u32(target.c.root).
		u16(uint16(x)).u16(uint16(y)).u16(uint16(width)).u16(uint16(height)).u16(0).u16(classInputOutput).u32(0).u32(0).done()
	target.c.send(create)
	version := binary.LittleEndian.AppendUint32(nil, xdndVersion)
	target.c.send(newRequest(opChangeProperty, modeReplace).u32(target.window).u32(target.atoms["XdndAware"]).u32(atomAtom).u8(32).u8(0).u8(0).u8(0).u32(1).raw(version).done())
	target.c.send(newRequest(opMapWindow, 0).u32(target.window).done())
	if _, err := target.c.call(newRequest(opQueryPointer, 0).u32(target.c.root).done()); err != nil {
		t.Fatalf("sync the target: %v", err)
	}
	go target.serve()
	return target
}

func (x *xdndTarget) serve() {
	for {
		select {
		case event := <-x.events:
			x.handle(event)
		case <-x.c.closed:
			return
		}
	}
}

func (x *xdndTarget) record(name string) {
	x.state.Lock()
	x.seen = append(x.seen, name)
	x.state.Unlock()
}

func (x *xdndTarget) messages() []string {
	x.state.Lock()
	defer x.state.Unlock()
	return slices.Clone(x.seen)
}

func (x *xdndTarget) entered() (int, []uint32) {
	x.state.Lock()
	defer x.state.Unlock()
	return x.version, slices.Clone(x.types)
}

func (x *xdndTarget) send(destination, kind uint32, data [5]uint32) {
	event := make([]byte, 32)
	event[0] = eventClientMessage
	event[1] = 32
	binary.LittleEndian.PutUint32(event[4:8], destination)
	binary.LittleEndian.PutUint32(event[8:12], kind)
	for i, value := range data {
		binary.LittleEndian.PutUint32(event[12+4*i:], value)
	}
	x.c.send(newRequest(opSendEvent, 0).u32(destination).u32(0).raw(event).done())
}

func (x *xdndTarget) handle(event []byte) {
	if event[0]&^sentEventFlag != eventClientMessage {
		return
	}
	le := binary.LittleEndian
	kind := le.Uint32(event[8:12])
	var data [5]uint32
	for i := range data {
		data[i] = le.Uint32(event[12+4*i:])
	}
	source := data[0]
	switch kind {
	case x.atoms["XdndEnter"]:
		x.record("XdndEnter")
		x.state.Lock()
		x.version = int(data[1] >> 24)
		x.types = nil
		for _, offered := range data[2:5] {
			if offered != atomNone {
				x.types = append(x.types, offered)
			}
		}
		x.state.Unlock()
	case x.atoms["XdndPosition"]:
		x.record("XdndPosition")
		_, types := x.entered()
		accept := x.behaviour.accept && slices.Contains(types, x.atoms["text/uri-list"])
		flags, action := uint32(2), uint32(atomNone)
		if accept {
			flags, action = 3, x.atoms["XdndActionCopy"]
		}
		x.send(source, x.atoms["XdndStatus"], [5]uint32{x.window, flags, 0, 0, action})
	case x.atoms["XdndLeave"]:
		x.record("XdndLeave")
	case x.atoms["XdndDrop"]:
		x.record("XdndDrop")
		list := x.fetch(data[2])
		select {
		case x.dropped <- list:
		default:
		}
		if x.behaviour.finish {
			x.send(source, x.atoms["XdndFinished"], [5]uint32{x.window, 1, x.atoms["XdndActionCopy"]})
		}
	}
}

func (x *xdndTarget) fetch(when uint32) string {
	uriList, property := x.atoms["text/uri-list"], x.atoms["LANHOP_TEST_DROP"]
	x.c.send(newRequest(opConvertSelection, 0).u32(x.window).u32(x.atoms["XdndSelection"]).u32(uriList).u32(property).u32(when).done())
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-x.events:
			if event[0]&^sentEventFlag != eventSelectionNotify || binary.LittleEndian.Uint32(event[16:20]) != uriList {
				continue
			}
			if binary.LittleEndian.Uint32(event[20:24]) == atomNone {
				return "refused"
			}
			reply, err := x.c.call(newRequest(opGetProperty, 1).u32(x.window).u32(property).u32(0).u32(0).u32(16384).done())
			if err != nil {
				return "error: " + err.Error()
			}
			length := int(binary.LittleEndian.Uint32(reply[16:20]))
			return string(reply[32 : 32+min(length, len(reply)-32)])
		case <-deadline:
			return "no answer"
		}
	}
}
