package sharer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/hotkey"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/keystate"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	sharerNumber        = 1
	firstReceiverNumber = 2
	trustCheckInterval  = time.Second
)

var errCaptureStopped = errors.New("sharer: the capture of the keyboard and mouse stopped")

type Capture interface {
	Events() <-chan event.Event
	Take() error
	GiveBack() error
}

type Conn interface {
	Send(events ...event.Event) error
	Receive() (event.Event, error)
	Close() error
	Peer() trust.Peer
}

type Trusted interface {
	Peers() []trust.Peer
}

type Sharer struct {
	capture            Capture
	trusted            Trusted
	detector           *hotkey.Detector
	trustCheckInterval time.Duration
	arrivals           chan Conn
	losses             chan Conn
	stopped            chan struct{}
	receivers          map[identity.Fingerprint]Conn
	focused            Conn
	heldOnFocused      keystate.Tracker
	unsent             []event.Event
	edges              EdgeSwitching
	now                func() time.Time
	ownEdges           *edge.Watcher
	remoteEdges        *edge.Watcher
	remoteCursor       *cursor
	moved              movement
	heldHere           keystate.Tracker
}

func New(capture Capture, trusted Trusted, detector *hotkey.Detector, edges EdgeSwitching) *Sharer {
	return &Sharer{
		capture:            capture,
		trusted:            trusted,
		detector:           detector,
		trustCheckInterval: trustCheckInterval,
		arrivals:           make(chan Conn),
		losses:             make(chan Conn),
		stopped:            make(chan struct{}),
		receivers:          make(map[identity.Fingerprint]Conn),
		edges:              edges,
		now:                time.Now,
		ownEdges:           edge.New(edges.Watch),
		remoteEdges:        watcherOfRemoteDesktops(edges.Watch),
	}
}

func (s *Sharer) Connect(conn Conn) {
	select {
	case s.arrivals <- conn:
	case <-s.stopped:
		conn.Close()
	}
}

func (s *Sharer) Run(ctx context.Context) (err error) {
	defer close(s.stopped)
	defer s.closeReceivers()
	defer func() { err = errors.Join(err, s.giveBack()) }()

	trustCheck := time.NewTicker(s.trustCheckInterval)
	defer trustCheck.Stop()
	for {
		select {
		case <-ctx.Done():
			s.leaveFocused()
			return nil
		case captured, capturing := <-s.capture.Events():
			if !capturing {
				return errCaptureStopped
			}
			err = s.handle(captured)
		case conn := <-s.arrivals:
			err = s.admit(conn)
		case conn := <-s.losses:
			err = s.drop(conn)
		case <-trustCheck.C:
			err = s.dropUntrusted()
		}
		if err != nil {
			return err
		}
	}
}

func (s *Sharer) handle(captured event.Event) error {
	verdict := s.detector.Feed(captured)
	s.observeKeysAndButtons(captured)
	if !verdict.Swallow {
		if err := s.follow(captured); err != nil {
			return err
		}
	}
	if verdict.Switch {
		return s.switchTo(verdict.Target)
	}
	return nil
}

func (s *Sharer) follow(captured event.Event) error {
	if s.focused == nil {
		return s.watchOwnScreen(captured)
	}
	return s.forward(captured)
}

func (s *Sharer) forward(captured event.Event) error {
	switch {
	case isEndOfReport(captured):
		return s.endReport()
	case s.remoteCursor != nil && isMovement(captured):
		s.moved.add(captured)
	case captured.Type == event.TypeRelative, captured.Type == event.TypeKey && captured.Value != event.KeyRepeated:
		s.heldOnFocused.Observe(captured)
		s.unsent = append(s.unsent, captured)
	}
	return nil
}

func (s *Sharer) sendUnsent() error {
	if len(s.unsent) == 0 {
		return nil
	}
	batch := append(s.unsent, event.Sync())
	s.unsent = s.unsent[:0]
	if s.focused.Send(batch...) != nil {
		return s.drop(s.focused)
	}
	return nil
}

func (s *Sharer) switchTo(target int) error {
	if target == sharerNumber {
		return s.focusOnSharer()
	}
	receiver, connected := s.receiverNumbered(target)
	if !connected || receiver == s.focused {
		return nil
	}
	return s.enter(receiver, atTheCentre)
}

func (s *Sharer) enter(receiver Conn, arrival arrival) error {
	if s.focused != nil {
		s.leaveFocused()
	} else if err := s.capture.Take(); err != nil {
		return fmt.Errorf("sharer: cannot take the keyboard and mouse away from this machine: %w", err)
	}
	s.focused, s.moved = receiver, movement{}
	greeting := []event.Event{event.Enter()}
	if s.layout() != nil {
		s.remoteCursor = cursorArriving(arrival)
		greeting = append(greeting, s.remoteCursor.position()...)
		greeting = append(greeting, event.Sync())
	}
	if receiver.Send(greeting...) != nil {
		return s.drop(receiver)
	}
	return nil
}

func (s *Sharer) receiverNumbered(number int) (Conn, bool) {
	peers := s.trusted.Peers()
	position := number - firstReceiverNumber
	if position < 0 || position >= len(peers) {
		return nil, false
	}
	receiver, connected := s.receivers[peers[position].Fingerprint]
	return receiver, connected
}

func (s *Sharer) focusOnSharer() error {
	if s.focused == nil {
		return nil
	}
	s.leaveFocused()
	return s.giveBack()
}

func (s *Sharer) leaveFocused() {
	left := s.focused
	if left == nil {
		return
	}
	farewell := s.unsent
	if releases := s.heldOnFocused.ReleaseAll(); releases != nil {
		farewell = append(farewell, releases...)
	} else if len(farewell) > 0 {
		farewell = append(farewell, event.Sync())
	}
	s.focused, s.unsent, s.remoteCursor, s.moved = nil, nil, nil, movement{}
	if left.Send(append(farewell, event.Leave())...) != nil {
		s.forget(left)
	}
}

func (s *Sharer) giveBack() error {
	if err := s.capture.GiveBack(); err != nil {
		return fmt.Errorf("sharer: cannot give the keyboard and mouse back to this machine: %w", err)
	}
	return nil
}

func (s *Sharer) admit(conn Conn) error {
	fingerprint := conn.Peer().Fingerprint
	previous, reconnecting := s.receivers[fingerprint]
	s.receivers[fingerprint] = conn
	go s.watch(conn)
	if !reconnecting {
		return nil
	}
	return s.giveInputBackThenClose(previous)
}

func (s *Sharer) watch(conn Conn) {
	for {
		if _, err := conn.Receive(); err != nil {
			conn.Close()
			select {
			case s.losses <- conn:
			case <-s.stopped:
			}
			return
		}
	}
}

func (s *Sharer) drop(conn Conn) error {
	fingerprint := conn.Peer().Fingerprint
	if s.receivers[fingerprint] != conn {
		return nil
	}
	delete(s.receivers, fingerprint)
	return s.giveInputBackThenClose(conn)
}

func (s *Sharer) giveInputBackThenClose(conn Conn) error {
	err := s.takeFocusFrom(conn)
	conn.Close()
	return err
}

func (s *Sharer) forget(conn Conn) {
	delete(s.receivers, conn.Peer().Fingerprint)
	conn.Close()
}

func (s *Sharer) takeFocusFrom(conn Conn) error {
	if s.focused != conn {
		return nil
	}
	s.focused, s.unsent, s.heldOnFocused = nil, nil, keystate.Tracker{}
	s.remoteCursor, s.moved = nil, movement{}
	return s.giveBack()
}

func (s *Sharer) dropUntrusted() error {
	trusted := make(map[identity.Fingerprint]bool)
	for _, peer := range s.trusted.Peers() {
		trusted[peer.Fingerprint] = true
	}
	for fingerprint, conn := range s.receivers {
		if trusted[fingerprint] {
			continue
		}
		if err := s.drop(conn); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sharer) closeReceivers() {
	for _, conn := range s.receivers {
		conn.Close()
	}
}
