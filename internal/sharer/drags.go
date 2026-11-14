package sharer

import (
	"slices"
	"sync"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
)

type Drag struct {
	Origin string
	Files  int
	Dock   edge.Side
}

type DragCrossing struct {
	Origin string
	From   string
	To     string
	Files  int
	Button uint16
}

type DragBlock int

const (
	DragCannotReach DragBlock = iota + 1
	DragCannotLeave
)

type BlockedDrag struct {
	Machine string
	Block   DragBlock
}

func (s *Sharer) DragStarted(drag Drag) {
	s.inbox.put(func() { s.dragStarted(drag) })
}

func (s *Sharer) DragEnded() {
	s.inbox.put(func() { s.drag = nil })
}

func (s *Sharer) OnDragCrossing(crossed func(DragCrossing)) {
	s.onDragCrossing = crossed
}

func (s *Sharer) OnBlockedDrag(blocked func(BlockedDrag)) {
	s.onBlockedDrag = blocked
}

func (s *Sharer) dragStarted(drag Drag) {
	s.docks[drag.Origin] = drag.Dock
	if s.drag != nil || drag.Origin != s.nameOfFocused() || !slices.Contains(s.heldHere.Held(), event.ButtonLeft) {
		return
	}
	s.drag = &drag
}

func (s *Sharer) hear(conn Conn, frame event.Event) {
	if frame.IsControl() && frame.Code == event.ControlFeatures {
		takes := frame.Value&(1<<features.Drag) != 0
		s.inbox.put(func() { s.sideTakesDrags(conn, takes) })
	}
}

func (s *Sharer) sideTakesDrags(conn Conn, takes bool) {
	if _, known := s.takesDrags[conn]; known {
		s.takesDrags[conn] = takes
	}
}

func (s *Sharer) takesDragsHere() bool {
	return s.edges.Features != nil && s.edges.Features.Has(features.Drag)
}

func (s *Sharer) armForTheDrag(watcher *edge.Watcher) {
	if s.drag == nil {
		watcher.DragEnded()
		return
	}
	watcher.DragStarted(s.docks[s.nameOfFocused()])
}

func (s *Sharer) dragMayEnter(machine string, takesDrags bool) bool {
	if s.focused == nil && s.edges.DragsStayHere {
		s.tellBlocked(BlockedDrag{Machine: s.edges.Name, Block: DragCannotLeave})
		return false
	}
	if takesDrags || s.drag != nil && machine == s.drag.Origin {
		return true
	}
	s.tellBlocked(BlockedDrag{Machine: machine, Block: DragCannotReach})
	return false
}

func (s *Sharer) tellBlocked(blocked BlockedDrag) {
	if s.toldBlocked[blocked] {
		return
	}
	s.toldBlocked[blocked] = true
	if s.onBlockedDrag != nil {
		s.onBlockedDrag(blocked)
	}
}

func (s *Sharer) tellDragCrossing(crossing edge.Crossing, from, to string) {
	if !crossing.Dragging || s.drag == nil || s.onDragCrossing == nil {
		return
	}
	s.onDragCrossing(DragCrossing{
		Origin: s.drag.Origin,
		From:   from,
		To:     to,
		Files:  s.drag.Files,
		Button: event.ButtonLeft,
	})
}

func (s *Sharer) wouldLeaveAKeyHeldHere(dragging bool) bool {
	if s.focused != nil {
		return false
	}
	for _, code := range s.heldHere.Held() {
		if code != event.ButtonLeft || !dragging {
			return true
		}
	}
	return false
}

type inbox struct {
	mutex  sync.Mutex
	queued []func()
	closed bool
	ready  chan struct{}
}

func newInbox() *inbox {
	return &inbox{ready: make(chan struct{}, 1)}
}

func (i *inbox) put(news func()) {
	i.mutex.Lock()
	if i.closed {
		i.mutex.Unlock()
		return
	}
	i.queued = append(i.queued, news)
	i.mutex.Unlock()
	select {
	case i.ready <- struct{}{}:
	default:
	}
}

func (i *inbox) take() []func() {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	taken := i.queued
	i.queued = nil
	return taken
}

func (i *inbox) close() {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	i.closed = true
	i.queued = nil
}

func (s *Sharer) readInbox() {
	for _, news := range s.inbox.take() {
		news()
	}
}
