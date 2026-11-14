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
	FilesOffThere
	DragCannotLeave
)

type BlockedDrag struct {
	Machine string
	Block   DragBlock
}

type dragInProgress struct {
	Drag
	origin  Conn
	holder  Conn
	crossed bool
}

type announcement struct {
	bits  int32
	heard bool
}

func (s *Sharer) DragStarted(drag Drag) {
	s.inbox.put(func() { s.dragStarted(drag) })
}

func (s *Sharer) DragCancelled() {
	s.inbox.put(func() { s.drag = nil })
}

func (s *Sharer) SourceDragEnded(origin string) {
	s.inbox.put(func() { s.sourceDragEnded(origin) })
}

func (s *Sharer) SetDock(machine string, dock edge.Side) {
	s.inbox.put(func() { s.docks[machine] = dock })
}

func (s *Sharer) OnDragCrossing(crossing func(DragCrossing)) {
	s.onDragCrossing = crossing
}

func (s *Sharer) OnDragEntered(entered func(DragCrossing)) {
	s.onDragEntered = entered
}

func (s *Sharer) OnBlockedDrag(blocked func(BlockedDrag)) {
	s.onBlockedDrag = blocked
}

func (s *Sharer) OnDragUnblocked(unblocked func(BlockedDrag)) {
	s.onDragUnblocked = unblocked
}

func (s *Sharer) dragStarted(drag Drag) {
	if drag.Dock != 0 {
		s.docks[drag.Origin] = drag.Dock
	}
	if s.drag != nil || drag.Origin != s.nameOfFocused() || !slices.Contains(s.heldHere.Held(), event.ButtonLeft) {
		return
	}
	s.drag = &dragInProgress{Drag: drag, origin: s.focused, holder: s.focused}
}

func (s *Sharer) sourceDragEnded(origin string) {
	if s.drag != nil && !s.drag.crossed && s.drag.Origin == origin {
		s.drag = nil
	}
}

func (s *Sharer) dragHere() *dragInProgress {
	if s.drag != nil && s.drag.holder != s.focused {
		s.drag = nil
	}
	return s.drag
}

func (s *Sharer) forgetTheDragOf(conn Conn) {
	if s.drag != nil && (s.drag.origin == conn || s.drag.holder == conn) {
		s.drag = nil
	}
}

func (s *Sharer) armForTheDrag(watcher *edge.Watcher) {
	if s.dragHere() == nil || s.onDragCrossing == nil {
		watcher.DragEnded()
		return
	}
	watcher.DragStarted(s.docks[s.nameOfFocused()])
}

func (s *Sharer) crossOver(dragging bool, from, to string, onto Conn, move func() error) error {
	if !dragging || s.dragHere() == nil || s.onDragCrossing == nil {
		s.drag = nil
		return move()
	}
	crossing := DragCrossing{Origin: s.drag.Origin, From: from, To: to, Files: s.drag.Files, Button: event.ButtonLeft}
	s.onDragCrossing(crossing)
	s.originEndsItsDrag = s.focused != nil && s.focused == s.drag.origin
	err := move()
	s.originEndsItsDrag = false
	if err != nil || s.drag == nil || s.focused != onto {
		s.drag = nil
		return err
	}
	s.drag.holder, s.drag.crossed = onto, true
	if s.onDragEntered != nil {
		s.onDragEntered(crossing)
	}
	if to == s.drag.Origin {
		s.drag = nil
	}
	return nil
}

func (s *Sharer) farewellReleases() []event.Event {
	releases := s.heldOnFocused.ReleaseAll()
	if !s.originEndsItsDrag {
		return releases
	}
	return slices.DeleteFunc(releases, func(release event.Event) bool {
		return release.Type == event.TypeKey && release.Code == event.ButtonLeft
	})
}

func (s *Sharer) hear(conn Conn, frame event.Event) {
	if frame.IsControl() && frame.Code == event.ControlFeatures {
		bits := frame.Value
		s.inbox.put(func() { s.sideAnnounced(conn, bits) })
	}
}

func (s *Sharer) sideAnnounced(conn Conn, bits int32) {
	if _, known := s.announced[conn]; !known {
		return
	}
	s.announced[conn] = announcement{bits: bits, heard: true}
	machine := conn.Peer().Name
	for _, block := range []DragBlock{DragCannotReach, FilesOffThere} {
		told := BlockedDrag{Machine: machine, Block: block}
		if !s.toldBlocked[told] || blockFor(bits) == block {
			continue
		}
		delete(s.toldBlocked, told)
		if s.onDragUnblocked != nil {
			s.onDragUnblocked(told)
		}
	}
}

func (s *Sharer) announcedHere() announcement {
	if s.edges.Features == nil {
		return announcement{heard: true}
	}
	return announcement{bits: s.edges.Features.Value(), heard: true}
}

func says(bits int32, bit int) bool {
	return bits&(1<<bit) != 0
}

func blockFor(bits int32) DragBlock {
	switch {
	case says(bits, features.Drag):
		return 0
	case says(bits, features.Files):
		return DragCannotReach
	}
	return FilesOffThere
}

func (s *Sharer) dragMayEnter(machine string, heard announcement) bool {
	if s.drag == nil {
		return false
	}
	if s.drag.origin == nil && s.edges.DragsStayHere {
		s.tellBlocked(BlockedDrag{Machine: s.edges.Name, Block: DragCannotLeave})
		return false
	}
	if machine == s.drag.Origin {
		return true
	}
	block := blockFor(heard.bits)
	if block == 0 {
		return true
	}
	if heard.heard {
		s.tellBlocked(BlockedDrag{Machine: machine, Block: block})
	}
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
