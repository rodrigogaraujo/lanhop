//go:build linux

package x11

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	opMapWindow   = 8
	opGetAtomName = 17

	eventButtonPress   = 4
	eventButtonRelease = 5
	eventMotionNotify  = 6
	eventClientMessage = 33

	xdndVersion       = 5
	dragButton        = 1
	xdndOldestVersion = 3
	dragListLimit     = 64 << 10
	targetSearchDepth = 32
)

var (
	ErrDragRefused  = errors.New("x11: the owner of the drag refused that format")
	ErrDragNoAnswer = errors.New("x11: the owner of the drag did not answer")
	ErrDragInParts  = errors.New("x11: the owner of the drag sent that format in parts (INCR)")
	ErrDragNotOwned = errors.New("x11: the X server did not let lanhop own XdndSelection")
	ErrNoServerTime = errors.New("x11: the X server did not tell its time")

	ErrFirstButtonNotHeld = errors.New("x11: the X server does not report button 1 held")
)

type dragAtoms struct {
	selection, aware, proxy, typeList                    uint32
	enter, position, status, leave, drop, finished       uint32
	actionCopy, uriList, targets, timestamp, incremental uint32
	property, timeProperty                               uint32
}

type OwnerReason byte

const (
	NewOwner             OwnerReason = 0
	OwnerWindowDestroyed OwnerReason = 1
	OwnerClientClosed    OwnerReason = 2
)

func (r OwnerReason) String() string {
	switch r {
	case NewOwner:
		return "new owner"
	case OwnerWindowDestroyed:
		return "owner window destroyed"
	case OwnerClientClosed:
		return "owner program gone"
	}
	return fmt.Sprintf("reason %d", byte(r))
}

type OwnerChange struct {
	Reason        OwnerReason
	Owner         uint32
	Time          uint32
	SelectionTime uint32
	At            time.Time
}

type PointerKind int

const (
	PointerMoved PointerKind = iota
	ButtonPressed
	ButtonReleased
)

type PointerEvent struct {
	Kind   PointerKind
	Button int
	X, Y   int
	Time   uint32
	State  uint16
}

type Served struct {
	Requestor uint32
	Target    uint32
	Answered  bool
	At        time.Time
}

type Offer struct {
	Targets    []string
	TargetsErr error
	URIList    string
	URIListErr error
}

func (o Offer) AllFiles() bool {
	return o.URIListErr == nil && everyDragAddressIsAFile(o.URIList)
}

type dragNotify struct {
	target   uint32
	property uint32
}

type clientMessage struct {
	window uint32
	kind   uint32
	data   [5]uint32
}

type Drags struct {
	c           *conn
	window      uint32
	atoms       dragAtoms
	xfixes      byte
	xfixesEvent byte
	owners      chan OwnerChange
	notifies    chan dragNotify
	pointer     chan PointerEvent
	messages    chan clientMessage
	times       chan uint32
	served      chan Served
	answerWait  time.Duration
	names       map[uint32]string
	offerState  sync.Mutex
	offered     bool
	offer       []byte
	offeredAt   uint32
}

func OpenDrags(displayName string) (*Drags, error) {
	if displayName == "" {
		displayName = os.Getenv("DISPLAY")
	}
	if displayName == "" {
		return nil, ErrNoDisplay
	}
	d, err := parseDisplay(displayName)
	if err != nil {
		return nil, err
	}
	return openDrags(d, cookieFromFile(d.number, authorityFile()))
}

func openDrags(d display, cookie []byte) (*Drags, error) {
	drags := &Drags{
		owners:     make(chan OwnerChange, 64),
		notifies:   make(chan dragNotify, 8),
		pointer:    make(chan PointerEvent, 4096),
		messages:   make(chan clientMessage, 256),
		times:      make(chan uint32, 8),
		served:     make(chan Served, 64),
		answerWait: time.Second,
		names:      make(map[uint32]string),
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

func internDragAtom(c *conn, name string) (uint32, error) {
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
		&d.atoms.typeList:     "XdndTypeList",
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
		&d.atoms.incremental:  "INCR",
		&d.atoms.property:     "LANHOP_DRAG",
		&d.atoms.timeProperty: "LANHOP_DRAG_TIME",
	}
	for atom, name := range names {
		value, err := internDragAtom(d.c, name)
		if err != nil {
			return err
		}
		*atom = value
	}
	reply, err := d.c.call(newRequest(opQueryExtension, 0).u16(uint16(len("XFIXES"))).u16(0).raw([]byte("XFIXES")).done())
	if err != nil {
		return err
	}
	if reply[8] == 0 {
		return ErrNoXFixes
	}
	d.xfixes, d.xfixesEvent = reply[9], reply[10]
	if _, err := d.c.call(newRequest(d.xfixes, xfixesQueryVersion).u32(5).u32(0).done()); err != nil {
		return fmt.Errorf("x11: XFIXES did not answer: %w", err)
	}
	d.window = d.c.newID()
	create := newRequest(opCreateWindow, 0).u32(d.window).u32(d.c.root).
		u16(0).u16(0).u16(1).u16(1).u16(0).u16(classInputOnly).u32(0).
		u32(attributeEventMask).u32(propertyChangeMask).done()
	if err := d.c.send(create); err != nil {
		return err
	}
	_, err = d.Owner()
	return err
}

func (d *Drags) Close() error {
	return d.c.close()
}

func (d *Drags) Window() uint32 {
	return d.window
}

func (d *Drags) Watch() error {
	if err := d.c.send(newRequest(d.xfixes, xfixesSelectSelectionInput).u32(d.window).u32(d.atoms.selection).u32(selectionEventsMask).done()); err != nil {
		return err
	}
	_, err := d.Owner()
	return err
}

func (d *Drags) OwnerChanges() <-chan OwnerChange {
	return d.owners
}

func (d *Drags) Owner() (uint32, error) {
	reply, err := d.c.call(newRequest(opGetSelectionOwner, 0).u32(d.atoms.selection).done())
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(reply[8:12]), nil
}

func (d *Drags) WaitForDragStart(ctx context.Context) (OwnerChange, error) {
	for {
		select {
		case change := <-d.owners:
			if change.Reason == NewOwner && change.Owner != atomNone && change.Owner != d.window {
				return change, nil
			}
		case <-ctx.Done():
			return OwnerChange{}, ctx.Err()
		case <-d.c.closed:
			return OwnerChange{}, d.c.err()
		}
	}
}

func (d *Drags) EndDragAfter(ctx context.Context, wait time.Duration, seen func(OwnerChange), end func() error) (OwnerChange, error) {
	change, err := d.WaitForDragStart(ctx)
	if err != nil {
		return change, err
	}
	if seen != nil {
		seen(change)
	}
	timer := time.NewTimer(time.Until(change.At.Add(wait)))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return change, ctx.Err()
	}
	state, err := d.Pointer()
	if err != nil {
		return change, err
	}
	if !slices.Contains(state.Held(), dragButton) {
		return change, fmt.Errorf("%w (buttons held: %v)", ErrFirstButtonNotHeld, state.Held())
	}
	return change, end()
}

func (d *Drags) ReadOffer() Offer {
	var offer Offer
	data, err := d.convert(d.atoms.targets)
	if err == nil {
		for i := 0; i+4 <= len(data); i += 4 {
			offer.Targets = append(offer.Targets, d.AtomName(binary.LittleEndian.Uint32(data[i:i+4])))
		}
	}
	offer.TargetsErr = err
	data, err = d.convert(d.atoms.uriList)
	offer.URIList, offer.URIListErr = string(data), err
	return offer
}

func (d *Drags) AtomName(atom uint32) string {
	if atom == atomNone {
		return "None"
	}
	if name, known := d.names[atom]; known {
		return name
	}
	reply, err := d.c.call(newRequest(opGetAtomName, 0).u32(atom).done())
	if err != nil {
		return fmt.Sprintf("atom %d", atom)
	}
	length := int(binary.LittleEndian.Uint16(reply[8:10]))
	name := string(reply[32 : 32+min(length, len(reply)-32)])
	d.names[atom] = name
	return name
}

func (d *Drags) convert(target uint32) ([]byte, error) {
	drain(d.notifies)
	if err := d.c.send(newRequest(opDeleteProperty, 0).u32(d.window).u32(d.atoms.property).done()); err != nil {
		return nil, err
	}
	request := newRequest(opConvertSelection, 0).u32(d.window).u32(d.atoms.selection).u32(target).u32(d.atoms.property).u32(currentTime).done()
	if err := d.c.send(request); err != nil {
		return nil, err
	}
	deadline := time.NewTimer(d.answerWait)
	defer deadline.Stop()
	for {
		select {
		case got := <-d.notifies:
			if got.target != target {
				continue
			}
			if got.property == atomNone {
				return nil, ErrDragRefused
			}
			return d.takeProperty()
		case <-deadline.C:
			return nil, ErrDragNoAnswer
		case <-d.c.closed:
			return nil, d.c.err()
		}
	}
}

func (d *Drags) takeProperty() ([]byte, error) {
	reply, err := d.c.call(newRequest(opGetProperty, 1).u32(d.window).u32(d.atoms.property).u32(0).u32(0).u32(dragListLimit / 4).done())
	if err != nil {
		return nil, err
	}
	format := int(reply[1])
	kind := binary.LittleEndian.Uint32(reply[8:12])
	length := int(binary.LittleEndian.Uint32(reply[16:20])) * max(format/8, 1)
	if format == 0 {
		length = 0
	}
	if kind == d.atoms.incremental {
		return nil, ErrDragInParts
	}
	return reply[32 : 32+min(length, len(reply)-32)], nil
}

func (d *Drags) serverTime() (uint32, error) {
	drain(d.times)
	d.setPropertyMode(d.window, d.atoms.timeProperty, atomString, 8, nil, modeAppend)
	timer := time.NewTimer(replyTimeout)
	defer timer.Stop()
	select {
	case when := <-d.times:
		return when, nil
	case <-timer.C:
		return 0, ErrNoServerTime
	case <-d.c.closed:
		return 0, d.c.err()
	}
}

func (d *Drags) offerDrag(list []byte, when uint32) error {
	d.offerState.Lock()
	d.offered, d.offer, d.offeredAt = true, list, when
	d.offerState.Unlock()
	if err := d.c.send(newRequest(opSetSelectionOwner, 0).u32(d.window).u32(d.atoms.selection).u32(when).done()); err != nil {
		return err
	}
	owner, err := d.Owner()
	if err != nil {
		return err
	}
	if owner != d.window {
		d.offerState.Lock()
		d.offered = false
		d.offerState.Unlock()
		return ErrDragNotOwned
	}
	return nil
}

func (d *Drags) handle(event []byte) {
	le := binary.LittleEndian
	code := event[0] &^ sentEventFlag
	switch {
	case d.xfixesEvent != 0 && code == d.xfixesEvent:
		if le.Uint32(event[12:16]) != d.atoms.selection {
			return
		}
		push(d.owners, OwnerChange{
			Reason:        OwnerReason(event[1]),
			Owner:         le.Uint32(event[8:12]),
			Time:          le.Uint32(event[16:20]),
			SelectionTime: le.Uint32(event[20:24]),
			At:            time.Now(),
		})
	case code == eventSelectionNotify:
		push(d.notifies, dragNotify{target: le.Uint32(event[16:20]), property: le.Uint32(event[20:24])})
	case code == eventSelectionRequest:
		d.answer(event)
	case code == eventSelectionClear:
		if le.Uint32(event[12:16]) == d.atoms.selection {
			d.offerState.Lock()
			d.offered = false
			d.offerState.Unlock()
		}
	case code == eventPropertyNotify:
		if le.Uint32(event[4:8]) == d.window && le.Uint32(event[8:12]) == d.atoms.timeProperty && event[16] == propertyNewValue {
			push(d.times, le.Uint32(event[12:16]))
		}
	case code == eventClientMessage:
		message := clientMessage{window: le.Uint32(event[4:8]), kind: le.Uint32(event[8:12])}
		for i := range message.data {
			message.data[i] = le.Uint32(event[12+4*i:])
		}
		push(d.messages, message)
	case code == eventButtonPress || code == eventButtonRelease || code == eventMotionNotify:
		kind := map[byte]PointerKind{eventButtonPress: ButtonPressed, eventButtonRelease: ButtonReleased, eventMotionNotify: PointerMoved}[code]
		push(d.pointer, PointerEvent{
			Kind:   kind,
			Button: int(event[1]),
			X:      int(int16(le.Uint16(event[20:22]))),
			Y:      int(int16(le.Uint16(event[22:24]))),
			Time:   le.Uint32(event[4:8]),
			State:  le.Uint16(event[28:30]),
		})
	}
}

func push[T any](events chan T, value T) {
	select {
	case events <- value:
	default:
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
	d.offerState.Lock()
	offered, list, since := d.offered, d.offer, d.offeredAt
	d.offerState.Unlock()

	answered := uint32(atomNone)
	if offered && selection == d.atoms.selection {
		switch target {
		case d.atoms.targets:
			var data []byte
			for _, atom := range []uint32{d.atoms.targets, d.atoms.timestamp, d.atoms.uriList} {
				data = le.AppendUint32(data, atom)
			}
			d.setPropertyMode(requestor, property, atomAtom, 32, data, modeReplace)
			answered = property
		case d.atoms.timestamp:
			d.setPropertyMode(requestor, property, atomInteger, 32, le.AppendUint32(nil, since), modeReplace)
			answered = property
		case d.atoms.uriList:
			d.setPropertyMode(requestor, property, d.atoms.uriList, 8, list, modeReplace)
			answered = property
		}
	}
	notify := make([]byte, 32)
	notify[0] = eventSelectionNotify
	le.PutUint32(notify[4:8], when)
	le.PutUint32(notify[8:12], requestor)
	le.PutUint32(notify[12:16], selection)
	le.PutUint32(notify[16:20], target)
	le.PutUint32(notify[20:24], answered)
	d.c.send(newRequest(opSendEvent, 0).u32(requestor).u32(0).raw(notify).done())
	push(d.served, Served{Requestor: requestor, Target: target, Answered: answered != atomNone, At: time.Now()})
}

func (d *Drags) setPropertyMode(window, property, kind uint32, format byte, data []byte, mode byte) {
	units := len(data) / int(format/8)
	d.c.send(newRequest(opChangeProperty, mode).u32(window).u32(property).u32(kind).u8(format).u8(0).u8(0).u8(0).u32(uint32(units)).raw(data).done())
}

func (d *Drags) sendMessage(destination, window, kind uint32, data [5]uint32) error {
	event := make([]byte, 32)
	event[0] = eventClientMessage
	event[1] = 32
	binary.LittleEndian.PutUint32(event[4:8], window)
	binary.LittleEndian.PutUint32(event[8:12], kind)
	for i, value := range data {
		binary.LittleEndian.PutUint32(event[12+4*i:], value)
	}
	return d.c.send(newRequest(opSendEvent, 0).u32(destination).u32(0).raw(event).done())
}

type dragTarget struct {
	window      uint32
	destination uint32
	version     int
}

func (d *Drags) targetUnderPointer() (dragTarget, error) {
	window := d.c.root
	for depth := 0; depth < targetSearchDepth; depth++ {
		state, err := d.queryPointer(window)
		if err != nil {
			return dragTarget{}, err
		}
		if state.Child == atomNone {
			return dragTarget{}, nil
		}
		if found, aware := d.awareTarget(state.Child); aware {
			return found, nil
		}
		window = state.Child
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
	return dragTarget{window: window, destination: destination, version: int(min(version, xdndVersion))}, true
}

func (d *Drags) windowProperty(window, property, kind uint32) (uint32, bool) {
	reply, err := d.c.call(newRequest(opGetProperty, 0).u32(window).u32(property).u32(kind).u32(0).u32(1).done())
	if err != nil || reply[1] != 32 || binary.LittleEndian.Uint32(reply[8:12]) != kind || binary.LittleEndian.Uint32(reply[16:20]) < 1 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(reply[32:36]), true
}

func FileURI(path string) string {
	address := url.URL{Scheme: "file", Path: path}
	return address.String()
}

func URIList(uris []string) []byte {
	var list strings.Builder
	for _, uri := range uris {
		list.WriteString(uri)
		list.WriteString("\r\n")
	}
	return []byte(list.String())
}

func everyDragAddressIsAFile(list string) bool {
	addresses := 0
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(line), "file:") {
			return false
		}
		addresses++
	}
	return addresses > 0
}
