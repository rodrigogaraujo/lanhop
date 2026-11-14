//go:build linux

package x11

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

const (
	opGetWindowAttributes = 3
	opMapWindow           = 8
	opUnmapWindow         = 10
	opConfigureWindow     = 12

	overrideRedirectAttribute = 0x200
	stackModeValue            = 0x40
	stackAbove                = 0
	mapStateViewable          = 2
	statusAccepts             = 1
	statusWantsPositions      = 2
	finishedAccepted          = 1
	enterHasTypeList          = 1
	typeListLimit             = 256
	narrowestStrip            = 2
)

var (
	ErrEdgeStrip       = errors.New("x11: the edge strip must be 2 to 65535 pixels wide and high, at coordinates from -32768 to 32767")
	ErrEdgeNotViewable = errors.New("x11: the window at the edge is mapped but not viewable")
	ErrNoDropAtEdge    = errors.New("x11: nothing was dropped on the window at the edge in time")
)

type Rect struct {
	X, Y, Width, Height int
}

func (r Rect) fitsTheProtocol() bool {
	inside := func(value, low, high int) bool { return value >= low && value <= high }
	return inside(r.X, math.MinInt16, math.MaxInt16) && inside(r.Y, math.MinInt16, math.MaxInt16) &&
		inside(r.Width, narrowestStrip, math.MaxUint16) && inside(r.Height, narrowestStrip, math.MaxUint16)
}

type EdgeDrop struct {
	Paths []string
	Err   error
}

type EdgeCatch struct {
	c          *conn
	window     uint32
	atoms      dragAtoms
	reader     *selectionReader
	wholeRead  time.Duration
	limit      time.Duration
	raiseEvery time.Duration
	messages   chan clientMessage
	accepted   chan struct{}
	accepting  sync.Once
	over       atomic.Bool
	finishing  sync.Mutex
	unfinished edgeVisit
	dropped    chan EdgeDrop
	stop       chan struct{}
	stopping   sync.Once
	vanishing  sync.Once
	done       chan struct{}
}

type edgeVisit struct {
	source     uint32
	version    uint32
	offersList bool
}

func (d *Drags) CatchAtEdge(strip Rect) (*EdgeCatch, error) {
	if !strip.fitsTheProtocol() {
		return nil, fmt.Errorf("%w: %+v", ErrEdgeStrip, strip)
	}
	e := &EdgeCatch{
		atoms:      d.atoms,
		reader:     newSelectionReader(),
		wholeRead:  d.limits.read,
		limit:      d.limits.catch,
		raiseEvery: d.limits.raise,
		messages:   make(chan clientMessage, 256),
		accepted:   make(chan struct{}),
		dropped:    make(chan EdgeDrop, 1),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	c, err := dial(d.display, d.cookie, e.handle)
	if err != nil {
		return nil, err
	}
	e.c = c
	if err := e.show(strip); err != nil {
		e.vanish()
		return nil, fmt.Errorf("x11: cannot catch drops at the edge: %w", err)
	}
	go e.run()
	return e, nil
}

func (e *EdgeCatch) show(strip Rect) error {
	if err := e.reader.attach(e.c, e.atoms.selection, e.atoms.listProperty, e.atoms.incr); err != nil {
		return err
	}
	e.window = e.c.newID()
	create := newRequest(opCreateWindow, 0).u32(e.window).u32(e.c.root).
		u16(uint16(int16(strip.X))).u16(uint16(int16(strip.Y))).u16(uint16(strip.Width)).u16(uint16(strip.Height)).
		u16(0).u16(classInputOnly).u32(0).u32(overrideRedirectAttribute).u32(1).done()
	if err := e.c.send(create); err != nil {
		return err
	}
	version := binary.LittleEndian.AppendUint32(nil, xdndVersion)
	newPropertyWriter(e.c, e.atoms.incr).change(e.window, e.atoms.aware, atomAtom, 32, version)
	if err := e.c.send(newRequest(opMapWindow, 0).u32(e.window).done()); err != nil {
		return err
	}
	e.raise()
	reply, err := e.c.call(newRequest(opGetWindowAttributes, 0).u32(e.window).done())
	if err != nil {
		return err
	}
	if reply[26] != mapStateViewable {
		return ErrEdgeNotViewable
	}
	return nil
}

func (e *EdgeCatch) Accepted() <-chan struct{} {
	return e.accepted
}

func (e *EdgeCatch) Over() bool {
	return e.over.Load()
}

func (e *EdgeCatch) Dropped() <-chan EdgeDrop {
	return e.dropped
}

func (e *EdgeCatch) Close() error {
	e.stopping.Do(func() { close(e.stop) })
	e.finish(false)
	e.vanish()
	<-e.done
	return nil
}

func (e *EdgeCatch) run() {
	limit := time.NewTimer(e.limit)
	defer limit.Stop()
	raise := time.NewTicker(e.raiseEvery)
	defer raise.Stop()
	var visit edgeVisit
	for {
		select {
		case message := <-e.messages:
			if message.window != e.window {
				continue
			}
			if drop, dropped := e.take(message, &visit); dropped {
				e.end(drop)
				return
			}
		case <-raise.C:
			e.raise()
		case <-limit.C:
			e.end(EdgeDrop{Err: ErrNoDropAtEdge})
			return
		case <-e.stop:
			e.end(EdgeDrop{Err: ErrClosed})
			return
		case <-e.c.closed:
			e.end(EdgeDrop{Err: e.c.err()})
			return
		}
	}
}

func (e *EdgeCatch) take(message clientMessage, visit *edgeVisit) (EdgeDrop, bool) {
	source := message.data[0]
	switch {
	case message.kind == e.atoms.enter:
		e.over.Store(false)
		*visit = e.enter(message)
	case visit.source == atomNone || source != visit.source:
	case message.kind == e.atoms.position:
		e.answerPosition(*visit)
	case message.kind == e.atoms.leave:
		e.over.Store(false)
		*visit = edgeVisit{}
	case message.kind == e.atoms.drop:
		return e.takeDrop(*visit, message.data[2]), true
	}
	return EdgeDrop{}, false
}

func (e *EdgeCatch) enter(message clientMessage) edgeVisit {
	visit := edgeVisit{source: message.data[0], version: message.data[1] >> 24}
	types := message.data[2:5]
	if message.data[1]&enterHasTypeList != 0 {
		types = atomListOf(e.c, visit.source, e.atoms.typeList)
	}
	visit.offersList = slices.Contains(types, e.atoms.uriList)
	return visit
}

func (e *EdgeCatch) answerPosition(visit edgeVisit) {
	flags, action := uint32(statusWantsPositions), uint32(atomNone)
	if visit.offersList {
		flags, action = statusAccepts|statusWantsPositions, e.atoms.actionCopy
	}
	sendClientMessage(e.c, visit.source, visit.source, e.atoms.status, [5]uint32{e.window, flags, 0, 0, action})
	if !visit.offersList || e.over.Load() {
		return
	}
	if _, err := ownerOf(e.c, e.atoms.selection); err != nil {
		return
	}
	e.over.Store(true)
	e.accepting.Do(func() { close(e.accepted) })
}

func (e *EdgeCatch) takeDrop(visit edgeVisit, when uint32) EdgeDrop {
	e.finishing.Lock()
	e.unfinished = visit
	e.finishing.Unlock()
	drop := EdgeDrop{Err: clipboard.ErrNotFiles}
	if visit.offersList {
		drop.Paths, drop.Err = e.reader.readPaths(e.atoms.uriList, when, false, time.Now().Add(e.wholeRead))
	}
	e.finish(drop.Err == nil)
	return drop
}

func (e *EdgeCatch) finish(taken bool) {
	e.finishing.Lock()
	defer e.finishing.Unlock()
	visit := e.unfinished
	if visit.source == atomNone {
		return
	}
	e.unfinished = edgeVisit{}
	finished := [5]uint32{e.window}
	if visit.version >= xdndVersion && taken {
		finished[1], finished[2] = finishedAccepted, e.atoms.actionCopy
	}
	sendClientMessage(e.c, visit.source, visit.source, e.atoms.finished, finished)
}

func (e *EdgeCatch) end(drop EdgeDrop) {
	e.over.Store(false)
	e.vanish()
	select {
	case <-e.stop:
	default:
		e.dropped <- drop
	}
	close(e.dropped)
	close(e.done)
}

func (e *EdgeCatch) vanish() {
	e.vanishing.Do(func() {
		if e.window != atomNone {
			e.c.send(newRequest(opUnmapWindow, 0).u32(e.window).done())
			e.c.send(newRequest(opDestroyWindow, 0).u32(e.window).done())
			ownerOf(e.c, e.atoms.selection)
		}
		e.c.close()
	})
}

func (e *EdgeCatch) raise() {
	e.c.send(newRequest(opConfigureWindow, 0).u32(e.window).u16(stackModeValue).u16(0).u32(stackAbove).done())
}

func (e *EdgeCatch) handle(event []byte) {
	le := binary.LittleEndian
	switch event[0] &^ sentEventFlag {
	case eventClientMessage:
		push(e.messages, clientMessageFrom(event))
	case eventSelectionNotify:
		e.reader.notified(event)
	case eventPropertyNotify:
		e.reader.propertyChanged(le.Uint32(event[4:8]), le.Uint32(event[8:12]), event[16])
	}
}

func atomListOf(c *conn, window, property uint32) []uint32 {
	le := binary.LittleEndian
	reply, err := c.call(newRequest(opGetProperty, 0).u32(window).u32(property).u32(atomAtom).u32(0).u32(typeListLimit).done())
	if err != nil || reply[1] != 32 || le.Uint32(reply[8:12]) != atomAtom {
		return nil
	}
	count := int(le.Uint32(reply[16:20]))
	return atomsIn(reply[32 : 32+min(4*count, len(reply)-32)])
}
