//go:build linux

package x11

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"time"
)

const (
	opGetWindowAttributes = 3
	opUnmapWindow         = 10
	opConfigureWindow     = 12
	opGetGeometry         = 14

	overrideRedirectAttribute = 0x200
	stackModeValue            = 0x40
	stackAbove                = 0
	mapStateViewable          = 2
	statusAccepts             = 1
	statusWantsPositions      = 2
	typeListLimit             = 256
	edgeRaiseEvery            = time.Second
	defaultEdgeSide           = "right"
	defaultEdgeWidth          = 40
	defaultFetchWait          = 2 * time.Second
)

var (
	ErrEdgeSide        = errors.New("x11: an edge is left, right, top or bottom")
	ErrEdgeWidth       = errors.New("x11: the edge strip does not fit the screen")
	ErrEdgeNotViewable = errors.New("x11: the edge window is mapped but not viewable")
)

type Rect struct {
	X, Y, Width, Height int
}

func EdgeRect(side string, width, screenWidth, screenHeight int) (Rect, error) {
	if width < 1 || width > screenWidth/2 || width > screenHeight/2 {
		return Rect{}, fmt.Errorf("%w: %d pixels on a %dx%d screen", ErrEdgeWidth, width, screenWidth, screenHeight)
	}
	switch side {
	case "left":
		return Rect{X: 0, Y: 0, Width: width, Height: screenHeight}, nil
	case "right":
		return Rect{X: screenWidth - width, Y: 0, Width: width, Height: screenHeight}, nil
	case "top":
		return Rect{X: 0, Y: 0, Width: screenWidth, Height: width}, nil
	case "bottom":
		return Rect{X: 0, Y: screenHeight - width, Width: screenWidth, Height: width}, nil
	}
	return Rect{}, fmt.Errorf("%w, not %q", ErrEdgeSide, side)
}

type EdgeOptions struct {
	Side      string
	Width     int
	FetchWait time.Duration
	Say       func(line string)
}

func (o EdgeOptions) withDefaults() EdgeOptions {
	if o.Side == "" {
		o.Side = defaultEdgeSide
	}
	if o.Width == 0 {
		o.Width = defaultEdgeWidth
	}
	if o.FetchWait <= 0 {
		o.FetchWait = defaultFetchWait
	}
	if o.Say == nil {
		o.Say = func(string) {}
	}
	return o
}

type EdgeDrop struct {
	Source          uint32
	Version         int
	Types           []string
	OffersURIList   bool
	Positions       int
	X, Y            int
	RequestedAction string
	DropTime        uint32
	EnterToDrop     time.Duration
	URIList         string
	URIListErr      error
	Accepted        bool
	FinishAction    string
	DropToFinish    time.Duration
}

type Edge struct {
	d      *Drags
	o      EdgeOptions
	window uint32
	rect   Rect
	closed bool
}

func (d *Drags) OpenEdge(o EdgeOptions) (*Edge, error) {
	o = o.withDefaults()
	screenWidth, screenHeight, err := d.screenSize()
	if err != nil {
		return nil, err
	}
	rect, err := EdgeRect(o.Side, o.Width, screenWidth, screenHeight)
	if err != nil {
		return nil, err
	}
	e := &Edge{d: d, o: o, window: d.c.newID(), rect: rect}
	create := newRequest(opCreateWindow, 0).u32(e.window).u32(d.c.root).
		u16(uint16(rect.X)).u16(uint16(rect.Y)).u16(uint16(rect.Width)).u16(uint16(rect.Height)).u16(0).u16(classInputOnly).u32(0).
		u32(overrideRedirectAttribute).u32(1).done()
	if err := d.c.send(create); err != nil {
		return nil, err
	}
	d.setPropertyMode(e.window, d.atoms.aware, atomAtom, 32, binary.LittleEndian.AppendUint32(nil, xdndVersion), modeReplace)
	if err := d.c.send(newRequest(opMapWindow, 0).u32(e.window).done()); err != nil {
		return nil, err
	}
	e.raise()
	viewable, err := e.Viewable()
	if err == nil && !viewable {
		err = ErrEdgeNotViewable
	}
	if err != nil {
		e.Close()
		return nil, fmt.Errorf("x11: the edge window over the %s edge: %w", o.Side, err)
	}
	return e, nil
}

func (d *Drags) screenSize() (int, int, error) {
	reply, err := d.c.call(newRequest(opGetGeometry, 0).u32(d.c.root).done())
	if err != nil {
		return 0, 0, err
	}
	return int(binary.LittleEndian.Uint16(reply[16:18])), int(binary.LittleEndian.Uint16(reply[18:20])), nil
}

func (e *Edge) Window() uint32 {
	return e.window
}

func (e *Edge) Rect() Rect {
	return e.rect
}

func (e *Edge) Viewable() (bool, error) {
	reply, err := e.d.c.call(newRequest(opGetWindowAttributes, 0).u32(e.window).done())
	if err != nil {
		return false, err
	}
	return reply[26] == mapStateViewable, nil
}

func (e *Edge) raise() {
	e.d.c.send(newRequest(opConfigureWindow, 0).u32(e.window).u16(stackModeValue).u16(0).u32(stackAbove).done())
}

func (e *Edge) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	e.d.c.send(newRequest(opUnmapWindow, 0).u32(e.window).done())
	e.d.c.send(newRequest(opDestroyWindow, 0).u32(e.window).done())
	_, err := e.d.Owner()
	return err
}

func (e *Edge) say(format string, args ...any) {
	e.o.Say(fmt.Sprintf(format, args...))
}

func millis(d time.Duration) float64 {
	return d.Seconds() * 1000
}

func (e *Edge) NextDrop(ctx context.Context) (EdgeDrop, error) {
	raise := time.NewTicker(edgeRaiseEvery)
	defer raise.Stop()
	var visit EdgeDrop
	var enteredAt time.Time
	inside := false
	for {
		select {
		case <-ctx.Done():
			if inside {
				e.say("stopped while 0x%x was over the edge, after %d XdndPosition", visit.Source, visit.Positions)
			}
			return visit, ctx.Err()
		case <-e.d.c.closed:
			return visit, e.d.c.err()
		case <-raise.C:
			e.raise()
		case message := <-e.d.messages:
			if message.window != e.window {
				continue
			}
			source := message.data[0]
			switch {
			case message.kind == e.d.atoms.enter:
				visit, enteredAt, inside = e.entered(message), time.Now(), true
			case !inside || source != visit.Source:
				e.say("%s from 0x%x ignored: no XdndEnter came from it", e.d.AtomName(message.kind), source)
			case message.kind == e.d.atoms.position:
				e.position(&visit, message, enteredAt)
			case message.kind == e.d.atoms.leave:
				e.say("XdndLeave from 0x%x %.1f ms after XdndEnter, after %d XdndPosition", source, millis(time.Since(enteredAt)), visit.Positions)
				visit, inside = EdgeDrop{}, false
			case message.kind == e.d.atoms.drop:
				visit.EnterToDrop = time.Since(enteredAt)
				return e.dropped(visit, message), nil
			}
		}
	}
}

func (e *Edge) entered(message clientMessage) EdgeDrop {
	visit := EdgeDrop{Source: message.data[0], Version: int(message.data[1] >> 24)}
	moreThanThree := message.data[1]&1 != 0
	var types []uint32
	for _, offered := range message.data[2:5] {
		if offered != atomNone {
			types = append(types, offered)
		}
	}
	if moreThanThree {
		listed, err := e.d.atomList(visit.Source, e.d.atoms.typeList)
		if err != nil {
			e.say("XdndTypeList of 0x%x: %v", visit.Source, err)
		} else {
			types = listed
		}
	}
	for _, atom := range types {
		visit.Types = append(visit.Types, e.d.AtomName(atom))
	}
	visit.OffersURIList = slices.Contains(types, e.d.atoms.uriList)
	e.say("XdndEnter from 0x%x: XDND version %d, %d types, text/uri-list among them: %v\n%v", visit.Source, visit.Version, len(visit.Types), visit.OffersURIList, visit.Types)
	return visit
}

func (e *Edge) position(visit *EdgeDrop, message clientMessage, enteredAt time.Time) {
	visit.Positions++
	x, y := int(int16(message.data[2]>>16)), int(int16(message.data[2]))
	requested := e.d.AtomName(message.data[4])
	e.d.sendMessage(visit.Source, visit.Source, e.d.atoms.status, [5]uint32{e.window, statusAccepts | statusWantsPositions, 0, 0, e.d.atoms.actionCopy})
	if visit.Positions == 1 || requested != visit.RequestedAction {
		e.say("XdndPosition %d from 0x%x at %d,%d asks for %s, %.1f ms after XdndEnter; answered XdndStatus: accept, XdndActionCopy", visit.Positions, visit.Source, x, y, requested, millis(time.Since(enteredAt)))
	}
	visit.X, visit.Y, visit.RequestedAction = x, y, requested
}

func (e *Edge) dropped(visit EdgeDrop, message clientMessage) EdgeDrop {
	visit.DropTime = message.data[2]
	droppedAt := time.Now()
	e.say("XdndDrop from 0x%x at server time %d, %.1f ms after XdndEnter, after %d XdndPosition; last position %d,%d", visit.Source, visit.DropTime, millis(visit.EnterToDrop), visit.Positions, visit.X, visit.Y)
	list, err := e.d.convertAt(e.d.atoms.uriList, visit.DropTime, e.o.FetchWait)
	visit.URIList, visit.URIListErr = string(list), err
	if err != nil {
		e.say("text/uri-list from XdndSelection: %v, %.1f ms after XdndDrop", err, millis(time.Since(droppedAt)))
	} else {
		e.say("text/uri-list from XdndSelection, %.1f ms after XdndDrop (%d bytes, every address a file: %v):\n%s", millis(time.Since(droppedAt)), len(list), everyDragAddressIsAFile(visit.URIList), visit.URIList)
	}
	visit.Accepted = err == nil
	action := uint32(atomNone)
	if visit.Accepted {
		action = e.d.atoms.actionCopy
	}
	data := [5]uint32{e.window}
	if visit.Version >= xdndVersion {
		if visit.Accepted {
			data[1] = 1
		}
		data[2] = action
	}
	e.d.sendMessage(visit.Source, visit.Source, e.d.atoms.finished, data)
	visit.FinishAction = e.d.AtomName(action)
	visit.DropToFinish = time.Since(droppedAt)
	e.say("XdndFinished sent to 0x%x %.1f ms after XdndDrop: accepted %v, action %s", visit.Source, millis(visit.DropToFinish), visit.Accepted, visit.FinishAction)
	return visit
}

func (d *Drags) atomList(window, property uint32) ([]uint32, error) {
	le := binary.LittleEndian
	reply, err := d.c.call(newRequest(opGetProperty, 0).u32(window).u32(property).u32(atomAtom).u32(0).u32(typeListLimit).done())
	if err != nil {
		return nil, err
	}
	if reply[1] != 32 || le.Uint32(reply[8:12]) != atomAtom {
		return nil, errors.New("x11: the property holds no atom list")
	}
	count := int(le.Uint32(reply[16:20]))
	atoms := make([]uint32, 0, count)
	for i := 0; i < count && 36+4*i <= len(reply); i++ {
		atoms = append(atoms, le.Uint32(reply[32+4*i:]))
	}
	return atoms, nil
}
