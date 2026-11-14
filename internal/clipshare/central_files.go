package clipshare

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/rodrigogaraujo/lanhop/internal/arrival"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/filestream"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
)

type dataHalf struct {
	side *link
	wire int32
}

type centralTransfer struct {
	id      int
	from    *link
	to      *link
	paths   []string
	arrival *arrival.Arrival
	written bool
	ctx     context.Context
	cancel  context.CancelCauseFunc

	mutex    sync.Mutex
	ended    bool
	stopSaid bool
	halves   []dataHalf
	conns    []*transport.DataConn
}

func (c *Central) start(t *centralTransfer) {
	if t.from == nil {
		t.paths = c.ownFiles
	}
	t.ctx, t.cancel = context.WithCancelCause(c.ctx)
	c.transfers[t.id] = t
	c.moving.Add(1)
	go func() {
		defer c.moving.Done()
		err := c.carry(t)
		t.close(nil)
		c.tell(func() { c.transferDone(t, err) })
	}()
}

func (c *Central) carry(t *centralTransfer) error {
	switch {
	case t.from != nil && t.to != nil:
		to, err := c.openHalf(t, t.to, false)
		if err != nil {
			return err
		}
		from, err := c.openHalf(t, t.from, true)
		if err != nil {
			return err
		}
		return relay(t.ctx, c.watch(from), c.watch(to))
	case t.from != nil:
		from, err := c.openHalf(t, t.from, true)
		if err != nil {
			return err
		}
		t.arrival = c.config.Arrivals.Expecting(t.id)
		return filestream.Receive(t.ctx, c.watch(from), arrivals{t.arrival})
	}
	to, err := c.openHalf(t, t.to, false)
	if err != nil {
		return err
	}
	_, err = filestream.Send(t.ctx, c.watch(to), t.paths)
	return err
}

func (c *Central) openHalf(t *centralTransfer, side *link, sideSends bool) (*transport.DataConn, error) {
	conn, err := c.config.AwaitData(t.ctx, side.fingerprint, func(wire int32) error {
		return t.announce(side, wire, sideSends)
	})
	if err != nil {
		return nil, err
	}
	if !t.hold(conn) {
		return nil, errTransferStopped
	}
	return conn, nil
}

func (c *Central) watch(conn *transport.DataConn) *watchedData {
	return watch(conn, c.config.DataSilenceLimit)
}

func (c *Central) transferDone(t *centralTransfer, err error) {
	if c.transfers[t.id] != t {
		if err == nil {
			t.leaveNothing()
		}
		return
	}
	if err != nil {
		delete(c.transfers, t.id)
		t.sayStop()
		fmt.Fprintf(c.config.Log, "%s stopped: %v\n", t.describe(), t.because(err))
		c.desk.TransferEnded(t.id)
		return
	}
	c.do(c.desk.FilesArrived(t.id))
	delete(c.transfers, t.id)
	if !t.written {
		t.leaveNothing()
	}
}

func (c *Central) writeArrived(transfer int) {
	t := c.transfers[transfer]
	if t == nil || t.arrival == nil {
		return
	}
	t.written = true
	c.keeper.writeFiles(t.arrival)
}

func (c *Central) stopTransfer(transfer int) {
	t := c.transfers[transfer]
	if t == nil {
		return
	}
	delete(c.transfers, transfer)
	t.sayStop()
	t.close(errTransferStopped)
}

func (c *Central) dataStopFrom(l *link, wire int32) {
	for _, t := range c.transfers {
		if t.hasHalf(l, wire) {
			t.close(fmt.Errorf("%s stopped it", l.name))
			return
		}
	}
}

func (c *Central) endTransfersWith(l *link) {
	for _, t := range c.transfers {
		if t.from == l || t.to == l {
			t.close(fmt.Errorf("the connection to %s was lost", l.name))
		}
	}
}

func (c *Central) endEveryTransfer() {
	for transfer, t := range c.transfers {
		delete(c.transfers, transfer)
		t.close(context.Canceled)
	}
	waitAtMost(&c.moving, c.config.SilenceLimit)
}

func (t *centralTransfer) announce(side *link, wire int32, sideSends bool) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.ended {
		return errTransferStopped
	}
	if !side.sendFrame(event.OpenData(wire, sideSends)) {
		return errConnectionClosed
	}
	t.halves = append(t.halves, dataHalf{side: side, wire: wire})
	return nil
}

func (t *centralTransfer) hold(conn *transport.DataConn) bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.ended {
		conn.Close()
		return false
	}
	t.conns = append(t.conns, conn)
	return true
}

func (t *centralTransfer) hasHalf(side *link, wire int32) bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for _, half := range t.halves {
		if half.side == side && half.wire == wire {
			return true
		}
	}
	return false
}

func (t *centralTransfer) sayStop() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.ended = true
	if t.stopSaid {
		return
	}
	t.stopSaid = true
	for _, half := range t.halves {
		half.side.sendFrame(event.DataStop(half.wire))
	}
}

func (t *centralTransfer) close(cause error) {
	t.cancel(cause)
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.ended = true
	for _, conn := range t.conns {
		conn.Close()
	}
}

func (t *centralTransfer) leaveNothing() {
	if t.arrival != nil {
		t.arrival.GiveUp()
	}
}

func (t *centralTransfer) because(err error) error {
	if cause := context.Cause(t.ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return err
}

func (t *centralTransfer) describe() string {
	switch {
	case t.from != nil && t.to != nil:
		return fmt.Sprintf("The file copy from %s to %s", t.from.name, t.to.name)
	case t.from != nil:
		return fmt.Sprintf("The file copy from %s", t.from.name)
	}
	return fmt.Sprintf("The file copy to %s", t.to.name)
}
