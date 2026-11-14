package main

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
)

type xdndDrag interface {
	FilesArrived(paths []string) error
	Cancel()
	Accepted() <-chan struct{}
	Done() <-chan struct{}
	Wait() error
}

type startXdndDrag func() (xdndDrag, error)

type dropHappened func(err error, accepted, filesGiven bool) bool

type xdndDrops struct {
	start    startXdndDrag
	hand     *dropReplay
	happened dropHappened
	log      io.Writer
	starting sync.Mutex
	mutex    sync.Mutex
	open     map[*xdndSession]struct{}
	closed   bool
}

func newXdndDrops(start startXdndDrag, hand *dropReplay, happened dropHappened, log io.Writer) *xdndDrops {
	return &xdndDrops{start: start, hand: hand, happened: happened, log: log, open: make(map[*xdndSession]struct{})}
}

func (x *xdndDrops) StartDrop(ctx context.Context, files []clipshare.DroppedFile) (clipshare.DropSession, error) {
	x.starting.Lock()
	defer x.starting.Unlock()
	for _, earlier := range x.sessions() {
		if !earlier.wasAccepted() {
			earlier.Cancel()
		}
	}
	if x.isClosed() {
		return nil, errDropsClosed
	}
	x.hand.dropStarts()
	held := x.hand.holdBack()
	drag, err := x.start()
	if err != nil {
		held.resume()
		x.hand.dropSettled()
		return nil, err
	}
	if held.resumeHoldingTheLeftButton(true) {
		fmt.Fprintln(x.log, "The button was released before the drop on this machine began, so it drops where the cursor is.")
	}
	session := &xdndSession{drag: drag, hand: x.hand, happened: x.happened, log: x.log, forget: x.forget, taken: make(chan struct{}), refused: make(chan struct{})}
	if !x.keep(session) {
		session.Cancel()
		return nil, errDropsClosed
	}
	go session.follow()
	return session, nil
}

func (x *xdndDrops) sessions() []*xdndSession {
	x.mutex.Lock()
	defer x.mutex.Unlock()
	open := make([]*xdndSession, 0, len(x.open))
	for session := range x.open {
		open = append(open, session)
	}
	return open
}

func (x *xdndDrops) isClosed() bool {
	x.mutex.Lock()
	defer x.mutex.Unlock()
	return x.closed
}

func (x *xdndDrops) keep(session *xdndSession) bool {
	x.mutex.Lock()
	defer x.mutex.Unlock()
	if x.closed {
		return false
	}
	x.open[session] = struct{}{}
	return true
}

func (x *xdndDrops) forget(session *xdndSession) {
	x.mutex.Lock()
	defer x.mutex.Unlock()
	delete(x.open, session)
}

func (x *xdndDrops) close() {
	x.mutex.Lock()
	x.closed = true
	x.mutex.Unlock()
	for _, session := range x.sessions() {
		session.Cancel()
	}
}

type xdndSession struct {
	drag       xdndDrag
	hand       *dropReplay
	happened   dropHappened
	log        io.Writer
	forget     func(*xdndSession)
	taken      chan struct{}
	refused    chan struct{}
	state      sync.Mutex
	accepted   bool
	filesGiven bool
}

func (s *xdndSession) Arrived(paths []string) {
	if err := s.drag.FilesArrived(paths); err != nil {
		fmt.Fprintf(s.log, "The dragged files arrived, and the drop on this machine cannot take them: %s\n", forTheLog(err))
		return
	}
	s.state.Lock()
	s.filesGiven = true
	s.state.Unlock()
}

func (s *xdndSession) Cancel() {
	s.drag.Cancel()
	s.hand.letGoOfTheDropButton()
}

func (s *xdndSession) Taken() <-chan struct{} {
	return s.taken
}

func (s *xdndSession) EndedWithoutDrop() <-chan struct{} {
	return s.refused
}

func (s *xdndSession) wasAccepted() bool {
	s.state.Lock()
	defer s.state.Unlock()
	return s.accepted
}

func (s *xdndSession) take() {
	s.state.Lock()
	s.accepted = true
	s.state.Unlock()
	s.hand.dropSettled()
	close(s.taken)
}

func (s *xdndSession) follow() {
	defer s.forget(s)
	select {
	case <-s.drag.Accepted():
		s.take()
		<-s.drag.Done()
	case <-s.drag.Done():
		select {
		case <-s.drag.Accepted():
			s.take()
		default:
		}
	}
	s.hand.letGoOfTheDropButton()
	s.hand.dropSettled()
	err := s.drag.Wait()
	s.state.Lock()
	accepted, filesGiven := s.accepted, s.filesGiven
	s.state.Unlock()
	if s.happened(err, accepted, filesGiven) {
		if err != nil {
			fmt.Fprintf(s.log, "The files were dropped on this machine, and the window that took them said: %s\n", forTheLog(err))
		}
		return
	}
	fmt.Fprintf(s.log, "The dragged files were not dropped on this machine: %s\n", forTheLog(err))
	close(s.refused)
}
