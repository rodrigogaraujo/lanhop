package sharer

import (
	"math"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/layout"
)

const (
	assumedDesktopWidth  = 1920
	assumedDesktopHeight = 1080
)

var remoteDesktop = edge.Bounds{MaxX: int(event.AbsoluteMax), MaxY: int(event.AbsoluteMax)}

type Speed struct {
	X, Y float64
}

func DefaultSpeed() Speed {
	return Speed{
		X: float64(event.AbsoluteMax) / assumedDesktopWidth,
		Y: float64(event.AbsoluteMax) / assumedDesktopHeight,
	}
}

type EdgeSwitching struct {
	Name   string
	Layout func() *layout.Layout
	Speed  Speed
	Watch  edge.Settings
}

type CursorCapture interface {
	Cursor() (x, y int, desktop edge.Bounds, known bool)
	PlaceCursor(x, y int)
}

func watcherOfRemoteDesktops(settings edge.Settings) *edge.Watcher {
	settings.CornerSize = settings.CornerSize * int(event.AbsoluteMax) / assumedDesktopHeight
	watcher := edge.New(settings)
	watcher.SetBounds(remoteDesktop)
	return watcher
}

func (s *Sharer) OwnScreenHasEdges() bool {
	screen, tellsTheCursor := s.capture.(CursorCapture)
	if !tellsTheCursor {
		return false
	}
	_, _, _, known := screen.Cursor()
	return known
}

func (s *Sharer) layout() *layout.Layout {
	if s.edges.Layout == nil {
		return nil
	}
	return s.edges.Layout()
}

type movement struct {
	dx, dy int
}

func isMovement(e event.Event) bool {
	return e.Type == event.TypeRelative && (e.Code == event.RelX || e.Code == event.RelY)
}

func isEndOfReport(e event.Event) bool {
	return e.Type == event.TypeSync && e.Code == event.SyncReport
}

func (m *movement) add(moved event.Event) {
	if moved.Code == event.RelX {
		m.dx += int(moved.Value)
		return
	}
	m.dy += int(moved.Value)
}

func (m *movement) take() movement {
	taken := *m
	*m = movement{}
	return taken
}

type arrival func(desktop edge.Bounds) (x, y int)

func atTheCentre(desktop edge.Bounds) (x, y int) {
	return (desktop.MinX + desktop.MaxX) / 2, (desktop.MinY + desktop.MaxY) / 2
}

func acrossFrom(crossing edge.Crossing) arrival {
	return func(desktop edge.Bounds) (x, y int) {
		alongX := desktop.MinX + int(math.Round(crossing.Proportion*float64(desktop.MaxX-desktop.MinX)))
		alongY := desktop.MinY + int(math.Round(crossing.Proportion*float64(desktop.MaxY-desktop.MinY)))
		switch crossing.Side {
		case edge.Left:
			return desktop.MaxX, alongY
		case edge.Right:
			return desktop.MinX, alongY
		case edge.Top:
			return alongX, desktop.MaxY
		default:
			return alongX, desktop.MinY
		}
	}
}

func onTheDesk(side edge.Side) layout.Side {
	switch side {
	case edge.Left:
		return layout.Left
	case edge.Right:
		return layout.Right
	case edge.Top:
		return layout.Above
	default:
		return layout.Below
	}
}

type cursor struct {
	x, y float64
}

func cursorArriving(arrival arrival) *cursor {
	x, y := arrival(remoteDesktop)
	return &cursor{x: float64(x), y: float64(y)}
}

func (c *cursor) move(by movement, speed Speed) {
	c.x = withinRemoteDesktop(c.x + float64(by.dx)*speed.X)
	c.y = withinRemoteDesktop(c.y + float64(by.dy)*speed.Y)
}

func withinRemoteDesktop(position float64) float64 {
	return min(max(position, 0), float64(event.AbsoluteMax))
}

func (c *cursor) at() (x, y int) {
	return int(math.Round(c.x)), int(math.Round(c.y))
}

func (c *cursor) position() []event.Event {
	x, y := c.at()
	return []event.Event{event.Absolute(event.AbsX, int32(x)), event.Absolute(event.AbsY, int32(y))}
}

func (s *Sharer) observeKeysAndButtons(captured event.Event) {
	if captured.Type != event.TypeKey || captured.Value == event.KeyRepeated {
		return
	}
	s.heldHere.Observe(captured)
	if captured.Code < event.ButtonFirst || captured.Code > event.ButtonLast {
		return
	}
	pressed := captured.Value == event.KeyPressed
	s.ownEdges.Button(captured.Code, pressed)
	s.remoteEdges.Button(captured.Code, pressed)
}

func (s *Sharer) watchOwnScreen(captured event.Event) error {
	if isMovement(captured) {
		s.moved.add(captured)
		return nil
	}
	if !isEndOfReport(captured) {
		return nil
	}
	moved := s.moved.take()
	screen, tellsTheCursor := s.capture.(CursorCapture)
	if moved == (movement{}) || !tellsTheCursor || s.layout() == nil {
		return nil
	}
	x, y, desktop, known := screen.Cursor()
	if !known {
		return nil
	}
	s.ownEdges.SetBounds(desktop)
	if crossing, pushedThrough := s.ownEdges.Move(s.now(), x, y, moved.dx, moved.dy); pushedThrough {
		return s.cross(crossing)
	}
	return nil
}

func (s *Sharer) endReport() error {
	moved := s.moved.take()
	if s.remoteCursor == nil || moved == (movement{}) {
		return s.sendUnsent()
	}
	s.remoteCursor.move(moved, s.edges.Speed)
	s.unsent = append(s.remoteCursor.position(), s.unsent...)
	if err := s.sendUnsent(); err != nil || s.remoteCursor == nil {
		return err
	}
	x, y := s.remoteCursor.at()
	if crossing, pushedThrough := s.remoteEdges.Move(s.now(), x, y, moved.dx, moved.dy); pushedThrough {
		return s.cross(crossing)
	}
	return nil
}

func (s *Sharer) cross(crossing edge.Crossing) error {
	desk := s.layout()
	if desk == nil || s.focusedIsANamesakeOfThisMachine() {
		return nil
	}
	neighbour, found := desk.Neighbour(s.nameOfFocused(), onTheDesk(crossing.Side))
	if !found {
		return nil
	}
	if neighbour == s.edges.Name {
		return s.comeBack(acrossFrom(crossing))
	}
	receiver, connected := s.receiverNamed(neighbour)
	if !connected || s.wouldLeaveAKeyHeldHere() {
		return nil
	}
	return s.enter(receiver, acrossFrom(crossing))
}

func (s *Sharer) wouldLeaveAKeyHeldHere() bool {
	return s.focused == nil && len(s.heldHere.Held()) > 0
}

func (s *Sharer) focusedIsANamesakeOfThisMachine() bool {
	return s.focused != nil && s.focused.Peer().Name == s.edges.Name
}

func (s *Sharer) nameOfFocused() string {
	if s.focused == nil {
		return s.edges.Name
	}
	return s.focused.Peer().Name
}

func (s *Sharer) receiverNamed(name string) (Conn, bool) {
	for _, receiver := range s.receivers {
		if receiver.Peer().Name == name {
			return receiver, true
		}
	}
	return nil, false
}

func (s *Sharer) comeBack(arrival arrival) error {
	if s.focused == nil {
		return nil
	}
	s.bringFocusBack()
	if err := s.giveBack(); err != nil {
		return err
	}
	if screen, tellsTheCursor := s.capture.(CursorCapture); tellsTheCursor {
		if _, _, desktop, known := screen.Cursor(); known {
			screen.PlaceCursor(arrival(desktop))
		}
	}
	return nil
}
