//go:build linux

package x11

import (
	"encoding/binary"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	opGetWindowAttributes = 3
	opMapWindow           = 8
	opConfigureWindow     = 12
	configureX            = 1
	configureY            = 2
	configureWidth        = 4
	configureHeight       = 8
	classInputOutput      = 1
	xtestFakeInput        = 2
	relativeMotion        = 1
	absoluteMotion        = 0
	mapStateUnmapped      = 0
)

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

func syncWith(t *testing.T, c *conn) {
	t.Helper()
	if _, err := c.call(newRequest(opQueryPointer, 0).u32(c.root).done()); err != nil {
		t.Fatalf("sync with the X server: %v", err)
	}
}

func setProperty32(c *conn, window, property, kind, value uint32) {
	c.send(newRequest(opChangeProperty, modeReplace).u32(window).u32(property).u32(kind).u8(32).u8(0).u8(0).u8(0).u32(1).raw(binary.LittleEndian.AppendUint32(nil, value)).done())
}

func grabFrom(t *testing.T, c *conn) byte {
	t.Helper()
	request := newRequest(opGrabPointer, 0).u32(c.root).u16(buttonPressMask | buttonReleaseMask | pointerMotionMask).
		u8(grabModeAsync).u8(grabModeAsync).u32(atomNone).u32(atomNone).u32(currentTime).done()
	reply, err := c.call(request)
	if err != nil {
		t.Fatalf("GrabPointer: %v", err)
	}
	return reply[1]
}

func ungrabFrom(t *testing.T, c *conn) {
	t.Helper()
	c.send(newRequest(opUngrabPointer, 0).u32(currentTime).done())
	syncWith(t, c)
}

func pointerIsFree(t *testing.T, display string) bool {
	t.Helper()
	other := dialForTest(t, display, func([]byte) {})
	if grabFrom(t, other) != grabSuccess {
		return false
	}
	ungrabFrom(t, other)
	return true
}

func takeXdndSelection(t *testing.T, display string) uint32 {
	t.Helper()
	other := dialForTest(t, display, func([]byte) {})
	selection, err := internAtom(other, "XdndSelection")
	if err != nil {
		t.Fatalf("intern XdndSelection: %v", err)
	}
	window := other.newID()
	other.send(newRequest(opCreateWindow, 0).u32(window).u32(other.root).u16(0).u16(0).u16(1).u16(1).u16(0).u16(classInputOnly).u32(0).u32(0).done())
	other.send(newRequest(opSetSelectionOwner, 0).u32(window).u32(selection).u32(currentTime).done())
	reply, err := other.call(newRequest(opGetSelectionOwner, 0).u32(selection).done())
	if err != nil || binary.LittleEndian.Uint32(reply[8:12]) != window {
		t.Fatalf("the other program does not own XdndSelection: %v", err)
	}
	return window
}

func askXdndSelection(t *testing.T, display, target string, when uint32) (string, bool) {
	t.Helper()
	events := make(chan []byte, 64)
	asker := dialForTest(t, display, func(event []byte) {
		select {
		case events <- event:
		default:
		}
	})
	atoms := make(map[string]uint32)
	for _, name := range []string{"XdndSelection", target, "LANHOP_TEST_ASK"} {
		atom, err := internAtom(asker, name)
		if err != nil {
			t.Fatalf("intern %s: %v", name, err)
		}
		atoms[name] = atom
	}
	window := asker.newID()
	asker.send(newRequest(opCreateWindow, 0).u32(window).u32(asker.root).u16(0).u16(0).u16(1).u16(1).u16(0).u16(classInputOnly).u32(0).u32(0).done())
	asker.send(newRequest(opConvertSelection, 0).u32(window).u32(atoms["XdndSelection"]).u32(atoms[target]).u32(atoms["LANHOP_TEST_ASK"]).u32(when).done())
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			if event[0]&^sentEventFlag != eventSelectionNotify {
				continue
			}
			if binary.LittleEndian.Uint32(event[20:24]) == atomNone {
				return "", true
			}
			reply, err := asker.call(newRequest(opGetProperty, 1).u32(window).u32(atoms["LANHOP_TEST_ASK"]).u32(0).u32(0).u32(1 << 16).done())
			if err != nil {
				t.Fatalf("GetProperty: %v", err)
			}
			length := int(binary.LittleEndian.Uint32(reply[16:20])) * max(int(reply[1])/8, 1)
			return string(reply[32 : 32+min(length, len(reply)-32)]), false
		case <-deadline:
			t.Fatalf("nobody answered a request for %s of XdndSelection", target)
			return "", true
		}
	}
}

func dragSelectionOwner(t *testing.T, source *Drags) uint32 {
	t.Helper()
	owner, err := source.selectionOwner()
	if err != nil {
		t.Fatalf("GetSelectionOwner: %v", err)
	}
	return owner
}

func checkLeftNothingBehind(t *testing.T, display string, source *Drags) {
	t.Helper()
	if !pointerIsFree(t, display) {
		t.Error("lanhop still holds the pointer after the drag")
	}
	if owner := dragSelectionOwner(t, source); owner == source.window {
		t.Error("lanhop still owns XdndSelection after the drag")
	}
	if !neverMapped(t, source) {
		t.Error("the window of the drag was mapped")
	}
}

func neverMapped(t *testing.T, source *Drags) bool {
	t.Helper()
	reply, err := source.c.call(newRequest(opGetWindowAttributes, 0).u32(source.window).done())
	if err != nil {
		t.Fatalf("GetWindowAttributes: %v", err)
	}
	return reply[26] == mapStateUnmapped
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

func (f *fakeInput) fake(t *testing.T, kind, detail byte, root uint32, x, y int) {
	t.Helper()
	request := newRequest(f.opcode, xtestFakeInput).u8(kind).u8(detail).u16(0).u32(currentTime).u32(root).
		u32(0).u32(0).u16(uint16(int16(x))).u16(uint16(int16(y))).u32(0).u16(0).u8(0).u8(0).done()
	if err := f.c.send(request); err != nil {
		t.Fatalf("XTEST: %v", err)
	}
	syncWith(t, f.c)
}

func (f *fakeInput) moveTo(t *testing.T, x, y int) {
	t.Helper()
	f.fake(t, eventMotionNotify, absoluteMotion, f.c.root, x, y)
}

func (f *fakeInput) moveBy(t *testing.T, dx, dy int) {
	t.Helper()
	f.fake(t, eventMotionNotify, relativeMotion, atomNone, dx, dy)
}

func (f *fakeInput) press(t *testing.T, button byte) {
	t.Helper()
	f.fake(t, eventButtonPress, button, atomNone, 0, 0)
}

func (f *fakeInput) release(t *testing.T, button byte) {
	t.Helper()
	f.fake(t, eventButtonRelease, button, atomNone, 0, 0)
}

type targetBehaviour struct {
	refuses        bool
	action         string
	silent         bool
	keepsTheDrop   bool
	failsTheDrop   bool
	version        uint32
	asksOnPosition []string
	behindAProxy   bool
}

type conversion struct {
	target  string
	data    string
	refused bool
}

type xdndTarget struct {
	c         *conn
	window    uint32
	receiver  uint32
	atoms     map[string]uint32
	behaviour targetBehaviour
	events    chan []byte
	backlog   [][]byte
	dropped   chan string
	askedOnce bool
	state     sync.Mutex
	seen      []string
	source    uint32
	version   uint32
	types     []uint32
	lastAt    [2]int
	times     []uint32
	asked     []conversion
}

func startXdndTarget(t *testing.T, display string, behaviour targetBehaviour) *xdndTarget {
	t.Helper()
	return startXdndTargetAt(t, display, behaviour, 100, 100, 200, 200)
}

func startXdndTargetAt(t *testing.T, display string, behaviour targetBehaviour, x, y, width, height int) *xdndTarget {
	t.Helper()
	target := &xdndTarget{
		atoms:     make(map[string]uint32),
		behaviour: behaviour,
		events:    make(chan []byte, 4096),
		dropped:   make(chan string, 1),
	}
	target.c = dialForTest(t, display, func(event []byte) {
		select {
		case target.events <- event:
		default:
		}
	})
	names := []string{"XdndAware", "XdndProxy", "XdndEnter", "XdndPosition", "XdndStatus", "XdndLeave", "XdndDrop", "XdndFinished",
		"XdndSelection", "XdndActionCopy", "XdndActionMove", "text/uri-list", "TARGETS", "TIMESTAMP", "INCR", "LANHOP_TEST_DROP"}
	for _, name := range append(names, behaviour.asksOnPosition...) {
		atom, err := internAtom(target.c, name)
		if err != nil {
			t.Fatalf("intern %s: %v", name, err)
		}
		target.atoms[name] = atom
	}
	version := behaviour.version
	if version == 0 {
		version = xdndVersion
	}
	owner := target.c
	if behaviour.behindAProxy {
		owner = dialForTest(t, display, func([]byte) {})
	}
	target.window = owner.newID()
	owner.send(newRequest(opCreateWindow, 0).u32(target.window).u32(owner.root).
		u16(uint16(x)).u16(uint16(y)).u16(uint16(width)).u16(uint16(height)).u16(0).u16(classInputOutput).u32(0).
		u32(attributeEventMask).u32(propertyChangeMask).done())
	target.receiver = target.window
	if behaviour.behindAProxy {
		target.receiver = target.c.newID()
		target.c.send(newRequest(opCreateWindow, 0).u32(target.receiver).u32(target.c.root).
			u16(0).u16(0).u16(1).u16(1).u16(0).u16(classInputOnly).u32(0).
			u32(attributeEventMask).u32(propertyChangeMask).done())
		setProperty32(owner, target.window, target.atoms["XdndProxy"], atomWindow, target.receiver)
		setProperty32(target.c, target.receiver, target.atoms["XdndProxy"], atomWindow, target.receiver)
	}
	setProperty32(target.c, target.receiver, target.atoms["XdndAware"], atomAtom, version)
	owner.send(newRequest(opMapWindow, 0).u32(target.window).done())
	syncWith(t, target.c)
	syncWith(t, owner)
	go target.serve()
	return target
}

func (x *xdndTarget) serve() {
	for {
		if len(x.backlog) > 0 {
			event := x.backlog[0]
			x.backlog = x.backlog[1:]
			x.handle(event)
			continue
		}
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

func (x *xdndTarget) messagesOnceItSaw(name string) []string {
	deadline := time.Now().Add(3 * time.Second)
	for {
		seen := x.messages()
		if slices.Contains(seen, name) || time.Now().After(deadline) {
			return seen
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (x *xdndTarget) entered() (uint32, []uint32) {
	x.state.Lock()
	defer x.state.Unlock()
	return x.version, slices.Clone(x.types)
}

func (x *xdndTarget) lastPosition() [2]int {
	x.state.Lock()
	defer x.state.Unlock()
	return x.lastAt
}

func (x *xdndTarget) positionTimes() []uint32 {
	x.state.Lock()
	defer x.state.Unlock()
	return slices.Clone(x.times)
}

func (x *xdndTarget) count(name string) int {
	n := 0
	for _, seen := range x.messages() {
		if seen == name {
			n++
		}
	}
	return n
}

func (x *xdndTarget) answerWithCopy() {
	x.state.Lock()
	source := x.source
	x.state.Unlock()
	x.send(source, x.atoms["XdndStatus"], [5]uint32{x.window, 3, 0, 0, x.atoms["XdndActionCopy"]})
}

func (x *xdndTarget) configure(t *testing.T, mask uint16, values ...uint32) {
	t.Helper()
	request := newRequest(opConfigureWindow, 0).u32(x.window).u16(mask).u16(0)
	for _, value := range values {
		request.u32(value)
	}
	x.c.send(request.done())
	syncWith(t, x.c)
}

func (x *xdndTarget) close(t *testing.T) {
	t.Helper()
	x.c.send(newRequest(opDestroyWindow, 0).u32(x.window).done())
	syncWith(t, x.c)
}

func (x *xdndTarget) conversions() []conversion {
	x.state.Lock()
	defer x.state.Unlock()
	return slices.Clone(x.asked)
}

func (x *xdndTarget) droppedList(t *testing.T) string {
	t.Helper()
	select {
	case list := <-x.dropped:
		return list
	case <-time.After(3 * time.Second):
		t.Fatal("the target fetched no list at the drop")
		return ""
	}
}

func (x *xdndTarget) send(destination, kind uint32, data [5]uint32) {
	le := binary.LittleEndian
	event := make([]byte, 32)
	event[0] = eventClientMessage
	event[1] = 32
	le.PutUint32(event[4:8], destination)
	le.PutUint32(event[8:12], kind)
	for i, value := range data {
		le.PutUint32(event[12+4*i:], value)
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
		x.askedOnce = false
		x.state.Lock()
		x.source = source
		x.version = data[1] >> 24
		x.types = nil
		for _, offered := range data[2:5] {
			if offered != atomNone {
				x.types = append(x.types, offered)
			}
		}
		x.state.Unlock()
	case x.atoms["XdndPosition"]:
		x.record("XdndPosition")
		x.state.Lock()
		x.lastAt = [2]int{int(int16(data[2] >> 16)), int(int16(data[2] & 0xffff))}
		x.times = append(x.times, data[3])
		x.state.Unlock()
		if !x.askedOnce {
			x.askedOnce = true
			for _, name := range x.behaviour.asksOnPosition {
				got, refused := x.fetch(x.atoms[name], data[3])
				x.state.Lock()
				x.asked = append(x.asked, conversion{target: name, data: got, refused: refused})
				x.state.Unlock()
			}
		}
		if x.behaviour.silent {
			return
		}
		_, types := x.entered()
		flags, action := uint32(2), uint32(atomNone)
		if !x.behaviour.refuses && slices.Contains(types, x.atoms["text/uri-list"]) {
			flags, action = 3, x.atoms["XdndActionCopy"]
			if x.behaviour.action != "" {
				action = x.atoms[x.behaviour.action]
			}
		}
		x.send(source, x.atoms["XdndStatus"], [5]uint32{x.window, flags, 0, 0, action})
	case x.atoms["XdndLeave"]:
		x.record("XdndLeave")
	case x.atoms["XdndDrop"]:
		x.record("XdndDrop")
		list, _ := x.fetch(x.atoms["text/uri-list"], data[2])
		select {
		case x.dropped <- list:
		default:
		}
		if x.behaviour.keepsTheDrop {
			return
		}
		took := uint32(1)
		if x.behaviour.failsTheDrop {
			took = 0
		}
		x.send(source, x.atoms["XdndFinished"], [5]uint32{x.window, took, x.atoms["XdndActionCopy"]})
	}
}

func (x *xdndTarget) nextEvent(deadline <-chan time.Time) ([]byte, bool) {
	select {
	case event := <-x.events:
		return event, true
	case <-deadline:
		return nil, false
	case <-x.c.closed:
		return nil, false
	}
}

func (x *xdndTarget) fetch(target, when uint32) (string, bool) {
	le := binary.LittleEndian
	property := x.atoms["LANHOP_TEST_DROP"]
	x.c.send(newRequest(opDeleteProperty, 0).u32(x.receiver).u32(property).done())
	x.c.send(newRequest(opConvertSelection, 0).u32(x.receiver).u32(x.atoms["XdndSelection"]).u32(target).u32(property).u32(when).done())
	deadline := time.After(3 * time.Second)
	for {
		event, ok := x.nextEvent(deadline)
		if !ok {
			return "no answer", true
		}
		if event[0]&^sentEventFlag == eventSelectionNotify && le.Uint32(event[16:20]) == target {
			if le.Uint32(event[20:24]) == atomNone {
				return "", true
			}
			return x.takeWhole(property, deadline), false
		}
		x.backlog = append(x.backlog, event)
	}
}

func (x *xdndTarget) takeWhole(property uint32, deadline <-chan time.Time) string {
	kind, data := x.takeProperty(property)
	if kind != x.atoms["INCR"] {
		return data
	}
	var whole strings.Builder
	for {
		if !x.waitForNewValue(property, deadline) {
			return "cut short after " + whole.String()
		}
		_, part := x.takeProperty(property)
		if part == "" {
			return whole.String()
		}
		whole.WriteString(part)
	}
}

func (x *xdndTarget) takeProperty(property uint32) (uint32, string) {
	reply, err := x.c.call(newRequest(opGetProperty, 1).u32(x.receiver).u32(property).u32(0).u32(0).u32(1 << 16).done())
	if err != nil {
		return 0, ""
	}
	le := binary.LittleEndian
	format := int(reply[1])
	length := int(le.Uint32(reply[16:20])) * max(format/8, 1)
	if format == 0 {
		length = 0
	}
	return le.Uint32(reply[8:12]), string(reply[32 : 32+min(length, len(reply)-32)])
}

func (x *xdndTarget) waitForNewValue(property uint32, deadline <-chan time.Time) bool {
	le := binary.LittleEndian
	for {
		event, ok := x.nextEvent(deadline)
		if !ok {
			return false
		}
		if event[0]&^sentEventFlag != eventPropertyNotify {
			x.backlog = append(x.backlog, event)
			continue
		}
		if le.Uint32(event[4:8]) == x.receiver && le.Uint32(event[8:12]) == property && event[16] == propertyNewValue {
			return true
		}
	}
}

type dragDesk struct {
	display string
	target  *xdndTarget
	source  *Drags
	input   *fakeInput
}

func newDragDesk(t *testing.T, behaviour targetBehaviour) *dragDesk {
	t.Helper()
	display := startXvfb(t)
	desk := &dragDesk{
		display: display,
		target:  startXdndTarget(t, display, behaviour),
		source:  openDragsOn(t, display),
		input:   startFakeInput(t, display),
	}
	desk.input.moveTo(t, 20, 20)
	return desk
}

func (desk *dragDesk) start(t *testing.T) *Drag {
	t.Helper()
	drag, err := desk.source.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		drag.Cancel()
		waitForDrag(t, drag)
		checkLeftNothingBehind(t, desk.display, desk.source)
	})
	return drag
}

func (desk *dragDesk) moveAlong(t *testing.T, path ...[2]int) {
	t.Helper()
	for _, at := range path {
		desk.input.moveTo(t, at[0], at[1])
		time.Sleep(10 * time.Millisecond)
	}
}

func (desk *dragDesk) dragIntoTheTarget(t *testing.T) {
	t.Helper()
	desk.moveAlong(t, [2]int{60, 60}, [2]int{110, 110}, [2]int{150, 150}, [2]int{180, 170}, [2]int{200, 200})
}

func waitForDrag(t *testing.T, drag *Drag) error {
	t.Helper()
	select {
	case <-drag.Done():
		return drag.Wait()
	case <-time.After(15 * time.Second):
		t.Fatal("the drag did not end")
		return nil
	}
}

func waitForAccepted(t *testing.T, drag *Drag) {
	t.Helper()
	select {
	case <-drag.Accepted():
	case <-drag.Done():
		t.Fatalf("the drag ended without an accepted drop: %v", drag.Wait())
	case <-time.After(5 * time.Second):
		t.Fatal("the drop was not accepted")
	}
}

func wasAccepted(drag *Drag) bool {
	select {
	case <-drag.Accepted():
		return true
	default:
		return false
	}
}
