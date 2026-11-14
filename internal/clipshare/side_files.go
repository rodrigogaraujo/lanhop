package clipshare

import (
	"context"
	"fmt"
	"sync"

	"github.com/rodrigogaraujo/lanhop/internal/arrival"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/filestream"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
)

type sideTransfer struct {
	wire      int
	link      *link
	receiving bool
	drop      bool
	run       *dropRun
	paths     []string
	arrival   *arrival.Arrival
	written   bool
	ctx       context.Context
	cancel    context.CancelCauseFunc

	mutex  sync.Mutex
	closed bool
	conn   *transport.DataConn
}

func (s *Side) startTransfer(wire int, receiving, drop bool) {
	t := &sideTransfer{wire: wire, link: s.current, receiving: receiving, drop: drop}
	switch {
	case receiving && drop:
		t.run = s.drops[wire]
	case drop:
		t.paths = s.dragged
	case !receiving:
		t.paths = s.described
	}
	t.ctx, t.cancel = context.WithCancelCause(s.ctx)
	s.endTransfer(wire, errTransferStopped)
	s.transfers[wire] = t
	s.moving.Add(1)
	go func() {
		defer s.moving.Done()
		err := s.carry(t)
		if err != nil && context.Cause(t.ctx) == nil && t.link != nil {
			t.link.sendFrame(event.DataStop(int32(t.wire)))
		}
		t.close(nil)
		s.inbox.put(func() { s.transferDone(t, err) })
	}()
}

func (s *Side) carry(t *sideTransfer) error {
	conn, err := s.config.DialData(t.ctx, int32(t.wire))
	if err != nil {
		return err
	}
	if !t.hold(conn) {
		return errTransferStopped
	}
	data := watch(conn, s.config.DataSilenceLimit)
	if !t.receiving {
		send := filestream.Send
		if t.drop {
			send = filestream.SendDrop
		}
		_, err := send(t.ctx, data, t.paths)
		return err
	}
	t.arrival = s.config.Arrivals.Expecting(t.wire)
	if t.drop {
		return filestream.ReceiveDrop(t.ctx, data, dropArrivals{arrivals: arrivals{t.arrival}, begin: s.beginDrop(t)})
	}
	return filestream.Receive(t.ctx, data, arrivals{t.arrival})
}

func (s *Side) transferDone(t *sideTransfer, err error) {
	if s.transfers[t.wire] != t {
		if err == nil {
			t.leaveNothing()
		}
		return
	}
	if err != nil || !t.receiving {
		delete(s.transfers, t.wire)
		s.machine.TransferEnded(t.wire)
		if err != nil {
			fmt.Fprintf(s.config.Log, "%s stopped: %s\n", t.describe(), forTheLog(whyItStopped(err)))
			s.do(s.machine.DropStopped(t.wire))
		}
		return
	}
	s.keepForTheDrop(t)
	s.do(s.machine.FilesArrived(t.wire))
	delete(s.transfers, t.wire)
	if !t.written {
		t.leaveNothing()
	}
}

func (s *Side) writeArrived(wire int) {
	t := s.transfers[wire]
	if t == nil || t.arrival == nil {
		return
	}
	t.written = true
	s.keeper.writeFiles(t.arrival)
}

func (s *Side) dataStop(l *link, wire int) {
	if l != s.current {
		return
	}
	s.endTransfer(wire, errStoppedByCentral)
	s.machine.TransferEnded(wire)
	s.do(s.machine.DropStopped(wire))
}

func (s *Side) endTransfer(wire int, cause error) {
	if t := s.transfers[wire]; t != nil {
		delete(s.transfers, wire)
		t.close(cause)
	}
}

func (s *Side) endEveryTransfer(cause error) {
	for wire := range s.transfers {
		s.endTransfer(wire, cause)
	}
}

func (t *sideTransfer) hold(conn *transport.DataConn) bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.closed {
		conn.Close()
		return false
	}
	t.conn = conn
	return true
}

func (t *sideTransfer) close(cause error) {
	t.cancel(cause)
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.closed = true
	if t.conn != nil {
		closeInTheBackground(t.conn)
	}
}

func (t *sideTransfer) leaveNothing() {
	if t.arrival != nil {
		t.arrival.GiveUp()
	}
}

func whyItStopped(err error) error {
	if endedByTheOtherEnd(err) {
		return errEndedByTheCentral
	}
	return err
}

func (t *sideTransfer) describe() string {
	what := "The file copy"
	if t.drop {
		what = "The dragged files"
	}
	if t.receiving {
		return what + " from " + theCentral
	}
	return what + " to " + theCentral
}
