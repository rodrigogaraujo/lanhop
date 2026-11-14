//go:build linux

package x11

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	opGetAtomName = 17

	eventButtonPress   = 4
	eventButtonRelease = 5
	eventMotionNotify  = 6
	eventClientMessage = 33

	atomWindow = 33

	xdndVersion       = 5
	xdndOldestVersion = 3
	dragButton        = 1
	dropTargetDepth   = 32
)

var (
	ErrDragUnderWay        = errors.New("x11: a drag is already under way")
	ErrDragCancelled       = errors.New("x11: the drag was cancelled before a drop")
	ErrDragTooLong         = errors.New("x11: the button was held longer than a drag may hold the pointer")
	ErrDragTaken           = errors.New("x11: another program took XdndSelection from the drag")
	ErrNoDropTarget        = errors.New("x11: the button was released over no window that takes drops")
	ErrDropRefused         = errors.New("x11: the window under the cursor refused a copy of the files")
	ErrTargetSilent        = errors.New("x11: the window under the cursor did not answer in time")
	ErrNotFinished         = errors.New("x11: the window took the drop and did not say in time that it finished")
	ErrDropFailed          = errors.New("x11: the window finished the drop without taking the files")
	ErrFilesLate           = errors.New("x11: the files did not arrive in time for the drop")
	ErrFilesAlreadyArrived = errors.New("x11: the files of this drag have already arrived")
	ErrDragEnded           = errors.New("x11: the drag has ended")
)

type dragLimits struct {
	grab   time.Duration
	hold   time.Duration
	status time.Duration
	finish time.Duration
	files  time.Duration
}

var defaultDragLimits = dragLimits{
	grab:   time.Second,
	hold:   2 * time.Minute,
	status: 2 * time.Second,
	finish: 10 * time.Second,
	files:  15 * time.Minute,
}

type dragAtoms struct {
	selection, aware, proxy                        uint32
	enter, position, status, leave, drop, finished uint32
	actionCopy, uriList, targets, timestamp, incr  uint32
	timeProperty                                   uint32
}

type pointerEvent struct {
	button   byte
	released bool
	x, y     int
	time     uint32
}

func (e pointerEvent) endsTheDrag() bool {
	return e.released && e.button == dragButton
}

type clientMessage struct {
	kind uint32
	data [5]uint32
}

type dragTarget struct {
	window      uint32
	destination uint32
	version     uint32
}

type Drags struct {
	c        *conn
	window   uint32
	atoms    dragAtoms
	writer   *propertyWriter
	limits   dragLimits
	motions  chan pointerEvent
	buttons  chan pointerEvent
	messages chan clientMessage
	times    chan uint32
	lost     chan struct{}
	offering sync.Mutex
	offered  bool
	list     []byte
	since    uint32
	running  sync.Mutex
	current  *Drag
}

type Drag struct {
	arrived    chan []byte
	cancel     chan struct{}
	cancelling sync.Once
	letGo      chan struct{}
	accepted   chan struct{}
	done       chan struct{}
	files      sync.Mutex
	given      bool
	err        error
}

func OpenDrags() (*Drags, error) {
	return openSession(openDrags)
}

func openDrags(d display, cookie []byte) (*Drags, error) {
	drags := &Drags{
		limits:   defaultDragLimits,
		motions:  make(chan pointerEvent, 1),
		buttons:  make(chan pointerEvent, 64),
		messages: make(chan clientMessage, 256),
		times:    make(chan uint32, 8),
		lost:     make(chan struct{}, 1),
	}
	c, err := dial(d, cookie, drags.handle)
	if err != nil {
		return nil, err
	}
	drags.c = c
	if err := drags.prepare(); err != nil {
		c.close()
		return nil, err
	}
	return drags, nil
}

func internAtom(c *conn, name string) (uint32, error) {
	reply, err := c.call(newRequest(opInternAtom, 0).u16(uint16(len(name))).u16(0).raw([]byte(name)).done())
	if err != nil {
		return 0, fmt.Errorf("x11: cannot name atom %s: %w", name, err)
	}
	return binary.LittleEndian.Uint32(reply[8:12]), nil
}

func (d *Drags) prepare() error {
	names := map[*uint32]string{
		&d.atoms.selection:    "XdndSelection",
		&d.atoms.aware:        "XdndAware",
		&d.atoms.proxy:        "XdndProxy",
		&d.atoms.enter:        "XdndEnter",
		&d.atoms.position:     "XdndPosition",
		&d.atoms.status:       "XdndStatus",
		&d.atoms.leave:        "XdndLeave",
		&d.atoms.drop:         "XdndDrop",
		&d.atoms.finished:     "XdndFinished",
		&d.atoms.actionCopy:   "XdndActionCopy",
		&d.atoms.uriList:      "text/uri-list",
		&d.atoms.targets:      "TARGETS",
		&d.atoms.timestamp:    "TIMESTAMP",
		&d.atoms.incr:         "INCR",
		&d.atoms.timeProperty: "LANHOP_DRAG_TIME",
	}
	for atom, name := range names {
		value, err := internAtom(d.c, name)
		if err != nil {
			return err
		}
		*atom = value
	}
	d.writer = newPropertyWriter(d.c, d.atoms.incr)
	d.window = d.c.newID()
	create := newRequest(opCreateWindow, 0).u32(d.window).u32(d.c.root).
		u16(0).u16(0).u16(1).u16(1).u16(0).u16(classInputOnly).u32(0).
		u32(attributeEventMask).u32(propertyChangeMask).done()
	if err := d.c.send(create); err != nil {
		return err
	}
	_, err := d.selectionOwner()
	return err
}

func (d *Drags) Close() error {
	return d.c.close()
}

func (d *Drags) Start() (*Drag, error) {
	d.running.Lock()
	defer d.running.Unlock()
	if d.current != nil {
		return nil, ErrDragUnderWay
	}
	drain(d.motions)
	drain(d.buttons)
	drain(d.messages)
	drain(d.lost)
	if err := d.grabPointer(); err != nil {
		return nil, err
	}
	at, err := d.queryPointer()
	var when uint32
	if err == nil {
		when, err = d.serverTime()
	}
	if err != nil {
		d.ungrabPointer()
		return nil, err
	}
	drag := &Drag{
		arrived:  make(chan []byte, 1),
		cancel:   make(chan struct{}),
		letGo:    make(chan struct{}),
		accepted: make(chan struct{}),
		done:     make(chan struct{}),
	}
	d.current = drag
	run := &dragRun{d: d, drag: drag, grabbed: true, at: pointerEvent{x: at.x, y: at.y, time: when}}
	go run.run()
	return drag, nil
}

func (g *Drag) FilesArrived(paths []string) error {
	addresses, err := fileAddresses(paths)
	if err != nil {
		return err
	}
	g.files.Lock()
	defer g.files.Unlock()
	if g.given {
		return ErrFilesAlreadyArrived
	}
	select {
	case <-g.done:
		return ErrDragEnded
	default:
	}
	g.given = true
	g.arrived <- addressList(addresses)
	return nil
}

func (g *Drag) Cancel() {
	g.cancelling.Do(func() { close(g.cancel) })
	select {
	case <-g.letGo:
	case <-g.done:
	}
}

func (g *Drag) Accepted() <-chan struct{} {
	return g.accepted
}

func (g *Drag) Done() <-chan struct{} {
	return g.done
}

func (g *Drag) Wait() error {
	<-g.done
	return g.err
}

type dragRun struct {
	d        *Drags
	drag     *Drag
	grabbed  bool
	at       pointerEvent
	list     []byte
	owned    bool
	since    uint32
	target   dragTarget
	asked    bool
	askedAt  time.Time
	waiting  bool
	next     pointerEvent
	accepts  bool
	action   uint32
	accepted bool
	dropped  bool
}

func (r *dragRun) run() {
	r.end(r.carry())
}

func (r *dragRun) carry() error {
	released, on, err := r.follow()
	if err != nil {
		return err
	}
	if r.list == nil {
		if err := r.waitForFiles(); err != nil {
			return err
		}
		if err := r.enter(on); err != nil {
			return err
		}
		if err := r.position(released); err != nil {
			return err
		}
	}
	for r.asked {
		if err := r.awaitStatus(); err != nil {
			return err
		}
	}
	if !r.accepts {
		return ErrDropRefused
	}
	if r.action != r.d.atoms.actionCopy {
		return fmt.Errorf("%w: it chose %s", ErrDropRefused, r.d.atomName(r.action))
	}
	return r.drop(released)
}

func (r *dragRun) follow() (pointerEvent, dragTarget, error) {
	hold := time.NewTimer(r.d.limits.hold)
	defer hold.Stop()
	for {
		var err error
		select {
		case event := <-r.d.buttons:
			if event.endsTheDrag() {
				return r.release(event)
			}
		case event := <-r.d.motions:
			if release, found := r.d.releaseWaiting(); found {
				return r.release(release)
			}
			err = r.moveTo(event)
		case message := <-r.d.messages:
			err = r.message(message)
		case list := <-r.drag.arrived:
			if err = r.offer(list); err == nil {
				err = r.moveTo(r.at)
			}
		case <-r.drag.cancel:
			err = ErrDragCancelled
		case <-hold.C:
			err = ErrDragTooLong
		case <-r.d.lost:
			err = ErrDragTaken
		case <-r.d.c.closed:
			err = r.d.c.err()
		}
		if err != nil {
			return pointerEvent{}, dragTarget{}, err
		}
	}
}

func (r *dragRun) release(at pointerEvent) (pointerEvent, dragTarget, error) {
	r.letGo()
	on, err := r.d.dropTargetAt(at.x, at.y)
	if err != nil {
		return at, on, err
	}
	if on.window == atomNone {
		return at, on, ErrNoDropTarget
	}
	if r.list == nil {
		r.accept()
		return at, on, nil
	}
	return at, on, r.moveOver(at, on)
}

func (r *dragRun) waitForFiles() error {
	late := time.NewTimer(r.d.limits.files)
	defer late.Stop()
	select {
	case list := <-r.drag.arrived:
		return r.offer(list)
	case <-late.C:
		return ErrFilesLate
	case <-r.drag.cancel:
		return ErrDragCancelled
	case <-r.d.c.closed:
		return r.d.c.err()
	}
}

func (r *dragRun) awaitStatus() error {
	silent := time.NewTimer(time.Until(r.askedAt.Add(r.d.limits.status)))
	defer silent.Stop()
	select {
	case message := <-r.d.messages:
		return r.message(message)
	case <-silent.C:
		return ErrTargetSilent
	case <-r.drag.cancel:
		return ErrDragCancelled
	case <-r.d.lost:
		return ErrDragTaken
	case <-r.d.c.closed:
		return r.d.c.err()
	}
}

func (r *dragRun) drop(released pointerEvent) error {
	r.accept()
	if err := r.send(r.d.atoms.drop, [5]uint32{r.d.window, 0, latest(released.time, r.since)}); err != nil {
		return err
	}
	r.dropped = true
	unfinished := time.NewTimer(r.d.limits.finish)
	defer unfinished.Stop()
	for {
		select {
		case message := <-r.d.messages:
			if message.kind != r.d.atoms.finished || !r.fromTarget(message) {
				continue
			}
			if r.target.version >= 5 && message.data[1]&1 == 0 {
				return ErrDropFailed
			}
			return nil
		case <-unfinished.C:
			return ErrNotFinished
		case <-r.d.c.closed:
			return r.d.c.err()
		}
	}
}

func (r *dragRun) end(err error) {
	if !r.dropped {
		r.leave()
	}
	r.letGo()
	if r.owned {
		r.disown()
	}
	r.d.running.Lock()
	r.d.current = nil
	r.d.running.Unlock()
	r.drag.err = err
	close(r.drag.done)
}

func (r *dragRun) letGo() {
	if !r.grabbed {
		return
	}
	r.grabbed = false
	r.d.ungrabPointer()
	close(r.drag.letGo)
}

func (r *dragRun) accept() {
	if !r.accepted {
		r.accepted = true
		close(r.drag.accepted)
	}
}

func (r *dragRun) offer(list []byte) error {
	when, err := r.d.serverTime()
	if err != nil {
		return err
	}
	r.list, r.since, r.owned = list, when, true
	r.d.offering.Lock()
	r.d.offered, r.d.list, r.d.since = true, list, when
	r.d.offering.Unlock()
	if err := r.d.c.send(newRequest(opSetSelectionOwner, 0).u32(r.d.window).u32(r.d.atoms.selection).u32(when).done()); err != nil {
		return err
	}
	owner, err := r.d.selectionOwner()
	if err != nil {
		return err
	}
	if owner != r.d.window {
		return ErrDragTaken
	}
	return nil
}

func (r *dragRun) disown() {
	r.d.withdrawOffer()
	r.d.c.send(newRequest(opSetSelectionOwner, 0).u32(atomNone).u32(r.d.atoms.selection).u32(r.since).done())
}

func (r *dragRun) moveTo(at pointerEvent) error {
	r.at = at
	if r.list == nil {
		return nil
	}
	found, err := r.d.dropTargetAt(at.x, at.y)
	if err != nil {
		return err
	}
	return r.moveOver(at, found)
}

func (r *dragRun) moveOver(at pointerEvent, found dragTarget) error {
	r.at = at
	if found.window != r.target.window {
		r.leave()
		if found.window != atomNone {
			if err := r.enter(found); err != nil {
				return err
			}
		}
	}
	if r.target.window == atomNone {
		return nil
	}
	if r.asked {
		r.next, r.waiting = at, true
		return nil
	}
	return r.position(at)
}

func (r *dragRun) enter(found dragTarget) error {
	r.target = found
	r.asked, r.waiting, r.accepts, r.action = false, false, false, atomNone
	return r.send(r.d.atoms.enter, [5]uint32{r.d.window, found.version << 24, r.d.atoms.uriList})
}

func (r *dragRun) position(at pointerEvent) error {
	place := uint32(uint16(int16(at.x)))<<16 | uint32(uint16(int16(at.y)))
	if err := r.send(r.d.atoms.position, [5]uint32{r.d.window, 0, place, latest(at.time, r.since), r.d.atoms.actionCopy}); err != nil {
		return err
	}
	r.asked, r.askedAt = true, time.Now()
	return nil
}

func (r *dragRun) leave() {
	if r.target.window == atomNone {
		return
	}
	r.send(r.d.atoms.leave, [5]uint32{r.d.window})
	r.target = dragTarget{}
	r.asked, r.waiting, r.accepts, r.action = false, false, false, atomNone
}

func (r *dragRun) message(message clientMessage) error {
	if message.kind != r.d.atoms.status || !r.fromTarget(message) {
		return nil
	}
	r.accepts = message.data[1]&1 != 0
	r.action = message.data[4]
	r.asked = false
	if !r.waiting {
		return nil
	}
	r.waiting = false
	return r.position(r.next)
}

func (r *dragRun) fromTarget(message clientMessage) bool {
	return r.target.window != atomNone && (message.data[0] == r.target.window || message.data[0] == r.target.destination)
}

func (r *dragRun) send(kind uint32, data [5]uint32) error {
	return r.d.sendMessage(r.target.destination, r.target.window, kind, data)
}

func latest(a, b uint32) uint32 {
	if int32(a-b) > 0 {
		return a
	}
	return b
}

func (d *Drags) releaseWaiting() (pointerEvent, bool) {
	for {
		select {
		case event := <-d.buttons:
			if event.endsTheDrag() {
				return event, true
			}
		default:
			return pointerEvent{}, false
		}
	}
}

func (d *Drags) dropTargetAt(x, y int) (dragTarget, error) {
	window := d.c.root
	for range dropTargetDepth {
		child, err := d.childAt(window, x, y)
		var gone RequestError
		if errors.As(err, &gone) {
			return dragTarget{}, nil
		}
		if err != nil {
			return dragTarget{}, err
		}
		if child == atomNone {
			return dragTarget{}, nil
		}
		if found, aware := d.awareTarget(child); aware {
			return found, nil
		}
		window = child
	}
	return dragTarget{}, nil
}

func (d *Drags) awareTarget(window uint32) (dragTarget, bool) {
	destination := window
	if proxy, set := d.windowProperty(window, d.atoms.proxy, atomWindow); set && proxy != atomNone {
		if itself, set := d.windowProperty(proxy, d.atoms.proxy, atomWindow); set && itself == proxy {
			destination = proxy
		}
	}
	version, aware := d.windowProperty(destination, d.atoms.aware, atomAtom)
	if !aware || version < xdndOldestVersion {
		return dragTarget{}, false
	}
	return dragTarget{window: window, destination: destination, version: min(version, xdndVersion)}, true
}

func (d *Drags) windowProperty(window, property, kind uint32) (uint32, bool) {
	reply, err := d.c.call(newRequest(opGetProperty, 0).u32(window).u32(property).u32(kind).u32(0).u32(1).done())
	le := binary.LittleEndian
	if err != nil || reply[1] != 32 || le.Uint32(reply[8:12]) != kind || le.Uint32(reply[16:20]) < 1 {
		return 0, false
	}
	return le.Uint32(reply[32:36]), true
}

func (d *Drags) atomName(atom uint32) string {
	if atom == atomNone {
		return "no action"
	}
	reply, err := d.c.call(newRequest(opGetAtomName, 0).u32(atom).done())
	if err != nil {
		return fmt.Sprintf("atom %d", atom)
	}
	length := int(binary.LittleEndian.Uint16(reply[8:10]))
	return string(reply[32 : 32+min(length, len(reply)-32)])
}

func (d *Drags) serverTime() (uint32, error) {
	drain(d.times)
	d.writer.changeMode(d.window, d.atoms.timeProperty, atomString, 8, nil, modeAppend)
	timer := time.NewTimer(replyTimeout)
	defer timer.Stop()
	select {
	case when := <-d.times:
		return when, nil
	case <-timer.C:
		return 0, errors.New("x11: the X server did not tell its time")
	case <-d.c.closed:
		return 0, d.c.err()
	}
}

func (d *Drags) selectionOwner() (uint32, error) {
	reply, err := d.c.call(newRequest(opGetSelectionOwner, 0).u32(d.atoms.selection).done())
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(reply[8:12]), nil
}

func (d *Drags) withdrawOffer() bool {
	d.offering.Lock()
	defer d.offering.Unlock()
	wasOffered := d.offered
	d.offered, d.list = false, nil
	return wasOffered
}

func (d *Drags) sendMessage(destination, window, kind uint32, data [5]uint32) error {
	le := binary.LittleEndian
	event := make([]byte, 32)
	event[0] = eventClientMessage
	event[1] = 32
	le.PutUint32(event[4:8], window)
	le.PutUint32(event[8:12], kind)
	for i, value := range data {
		le.PutUint32(event[12+4*i:], value)
	}
	return d.c.send(newRequest(opSendEvent, 0).u32(destination).u32(0).raw(event).done())
}

func (d *Drags) handle(event []byte) {
	le := binary.LittleEndian
	synthetic := event[0]&sentEventFlag != 0
	switch event[0] &^ sentEventFlag {
	case eventMotionNotify:
		if !synthetic {
			replace(d.motions, pointerEventFrom(event))
		}
	case eventButtonPress, eventButtonRelease:
		if !synthetic {
			push(d.buttons, pointerEventFrom(event))
		}
	case eventClientMessage:
		message := clientMessage{kind: le.Uint32(event[8:12])}
		for i := range message.data {
			message.data[i] = le.Uint32(event[12+4*i:])
		}
		push(d.messages, message)
	case eventSelectionRequest:
		d.answer(event)
	case eventSelectionClear:
		if le.Uint32(event[12:16]) == d.atoms.selection && d.withdrawOffer() {
			push(d.lost, struct{}{})
		}
	case eventPropertyNotify:
		window, property, state := le.Uint32(event[4:8]), le.Uint32(event[8:12]), event[16]
		switch {
		case window == d.window && property == d.atoms.timeProperty && state == propertyNewValue:
			push(d.times, le.Uint32(event[12:16]))
		case window != d.window && state == propertyDeleted:
			d.writer.propertyDeleted(window, property)
		}
	}
}

func pointerEventFrom(event []byte) pointerEvent {
	le := binary.LittleEndian
	return pointerEvent{
		button:   event[1],
		released: event[0]&^sentEventFlag == eventButtonRelease,
		x:        int(int16(le.Uint16(event[20:22]))),
		y:        int(int16(le.Uint16(event[22:24]))),
		time:     le.Uint32(event[4:8]),
	}
}

func (d *Drags) answer(event []byte) {
	le := binary.LittleEndian
	when := le.Uint32(event[4:8])
	requestor := le.Uint32(event[12:16])
	selection := le.Uint32(event[16:20])
	target := le.Uint32(event[20:24])
	property := le.Uint32(event[24:28])
	if property == atomNone {
		property = target
	}
	d.offering.Lock()
	offered, list, since := d.offered, d.list, d.since
	d.offering.Unlock()

	answered := uint32(atomNone)
	askedInTime := when == currentTime || int32(when-since) >= 0
	if offered && askedInTime && selection == d.atoms.selection {
		switch target {
		case d.atoms.targets:
			var data []byte
			for _, atom := range []uint32{d.atoms.targets, d.atoms.timestamp, d.atoms.uriList} {
				data = le.AppendUint32(data, atom)
			}
			d.writer.change(requestor, property, atomAtom, 32, data)
			answered = property
		case d.atoms.timestamp:
			d.writer.change(requestor, property, atomInteger, 32, le.AppendUint32(nil, since))
			answered = property
		case d.atoms.uriList:
			d.writer.give(requestor, property, d.atoms.uriList, list)
			answered = property
		}
	}
	notifySelection(d.c, when, requestor, selection, target, answered)
}

func push[T any](events chan T, value T) {
	select {
	case events <- value:
	default:
	}
}

func replace[T any](slot chan T, value T) {
	select {
	case <-slot:
	default:
	}
	push(slot, value)
}
