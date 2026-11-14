package transport

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
	"github.com/rodrigogaraujo/lanhop/internal/wire"
)

var (
	ErrDataNotAwaited         = errors.New("transport: no data connection is awaited with that transfer number")
	ErrDataFromAnotherMachine = errors.New("transport: that transfer number was handed to another machine")
	ErrDataNotOpened          = errors.New("transport: the other machine did not open the data connection in time")
	ErrDataRefused            = errors.New("transport: the other machine refused the data connection")
	ErrDataUsedBothWays       = errors.New("transport: a data connection is read on one side and written on the other, never both")
	ErrDataEndedEarly         = errors.New("transport: the data connection ended before the other machine said that every file arrived")
	ErrDataNotAnAck           = errors.New("transport: the other machine sent something else than an acknowledgement back on the data connection")
)

const ackedBlock = 32 << 10

type dataRole int

const (
	undecided dataRole = iota
	reading
	writing
)

type DataConn struct {
	secured   *tls.Conn
	peer      trust.Peer
	transfer  int32
	pace      *Pace
	writing   sync.Mutex
	release   func()
	releasing sync.Once

	roleMutex sync.Mutex
	role      dataRole

	acking     sync.Mutex
	unacked    int
	blocksRead int32
	told       bool

	acknowledged atomic.Int32
	arrived      chan struct{}
	ended        chan struct{}
	endedBy      error
}

func newDataConn(secured *tls.Conn, peer trust.Peer, transfer int32, pace *Pace, release func()) *DataConn {
	return &DataConn{
		secured:  secured,
		peer:     peer,
		transfer: transfer,
		pace:     pace,
		release:  release,
		arrived:  make(chan struct{}),
		ended:    make(chan struct{}),
	}
}

func (d *DataConn) Peer() trust.Peer {
	return d.peer
}

func (d *DataConn) Transfer() int32 {
	return d.transfer
}

func (d *DataConn) Read(buffer []byte) (int, error) {
	if err := d.take(reading); err != nil {
		return 0, err
	}
	n, err := d.secured.Read(buffer)
	if n > 0 {
		d.acknowledge(n)
	}
	return n, err
}

func (d *DataConn) take(wanted dataRole) error {
	d.roleMutex.Lock()
	defer d.roleMutex.Unlock()
	switch d.role {
	case wanted:
		return nil
	case undecided:
		d.role = wanted
		if wanted == writing {
			d.secured.SetReadDeadline(time.Time{})
			go d.readAcks()
		}
		return nil
	}
	return ErrDataUsedBothWays
}

func (d *DataConn) acknowledge(read int) {
	d.acking.Lock()
	defer d.acking.Unlock()
	if d.told {
		return
	}
	d.unacked += read
	for d.unacked >= ackedBlock {
		d.unacked -= ackedBlock
		d.blocksRead = (d.blocksRead + 1) & event.TransferNumberMax
		if wire.Write(d.secured, event.DataAck(d.blocksRead)) != nil {
			return
		}
	}
}

func (d *DataConn) readAcks() {
	for {
		frame, err := wire.Read(d.secured)
		if err == nil && (!frame.IsControl() || frame.Code != event.ControlDataAck) {
			err = ErrDataNotAnAck
		}
		if err != nil {
			d.endedBy = err
			d.secured.Close()
			close(d.ended)
			return
		}
		blocks, all := event.SplitDataAck(frame.Value)
		if all {
			close(d.arrived)
			return
		}
		d.acknowledged.Store(blocks)
	}
}

func (d *DataConn) TellAllArrived() error {
	if err := d.take(reading); err != nil {
		return err
	}
	d.acking.Lock()
	defer d.acking.Unlock()
	if d.told {
		return nil
	}
	d.told = true
	return wire.Write(d.secured, event.AllArrived(d.blocksRead))
}

func (d *DataConn) AwaitAllArrived(ctx context.Context) error {
	if err := d.take(writing); err != nil {
		return err
	}
	select {
	case <-d.arrived:
		return nil
	case <-d.ended:
		return fmt.Errorf("%w: %w", ErrDataEndedEarly, d.endedBy)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *DataConn) Write(data []byte) (int, error) {
	if err := d.take(writing); err != nil {
		return 0, err
	}
	d.writing.Lock()
	defer d.writing.Unlock()
	written := 0
	for written < len(data) {
		slice := data[written:min(len(data), written+pacedSlice)]
		d.pace.wait(len(slice))
		n, err := d.secured.Write(slice)
		written += n
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

func (d *DataConn) SetDeadline(deadline time.Time) error {
	d.roleMutex.Lock()
	defer d.roleMutex.Unlock()
	if d.role == writing {
		return d.secured.SetWriteDeadline(deadline)
	}
	return d.secured.SetDeadline(deadline)
}

func (d *DataConn) SetReadDeadline(deadline time.Time) error {
	d.roleMutex.Lock()
	defer d.roleMutex.Unlock()
	if d.role == writing {
		return nil
	}
	return d.secured.SetReadDeadline(deadline)
}

func (d *DataConn) SetWriteDeadline(deadline time.Time) error {
	return d.secured.SetWriteDeadline(deadline)
}

func (d *DataConn) Close() error {
	err := d.secured.Close()
	if d.release != nil {
		d.releasing.Do(d.release)
	}
	return err
}

type heldTransfer struct {
	peer      identity.Fingerprint
	deadline  time.Time
	arrival   chan dataArrival
	opened    bool
	abandoned bool
}

type dataArrival struct {
	conn *DataConn
	err  error
}

func (l *Listener) AwaitData(ctx context.Context, peer identity.Fingerprint, announce func(transfer int32) error) (*DataConn, error) {
	if err := l.stillListening(ctx); err != nil {
		return nil, err
	}
	transfer, held, err := l.hold(peer)
	if err != nil {
		return nil, err
	}
	defer l.forget(transfer, held)
	if err := announce(transfer); err != nil {
		return nil, err
	}
	window := time.NewTimer(l.startWindow(held))
	defer window.Stop()
	for {
		select {
		case arrived := <-held.arrival:
			return arrived.conn, arrived.err
		case <-window.C:
			if l.withdrawUnopened(transfer, held) {
				return nil, ErrDataNotOpened
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-l.closed:
			return nil, net.ErrClosed
		}
	}
}

func (l *Listener) stillListening(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-l.closed:
		return net.ErrClosed
	default:
		return nil
	}
}

func drawTransferNumber() (int32, error) {
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(random[:]) & uint32(event.TransferNumberMax)), nil
}

func (l *Listener) hold(peer identity.Fingerprint) (int32, *heldTransfer, error) {
	held := &heldTransfer{peer: peer, arrival: make(chan dataArrival, 1)}
	l.heldMutex.Lock()
	defer l.heldMutex.Unlock()
	for {
		transfer, err := l.config.drawTransferNumber()
		if err != nil {
			return 0, nil, err
		}
		if _, taken := l.held[transfer]; transfer != 0 && !taken {
			l.held[transfer] = held
			return transfer, held, nil
		}
	}
}

func (l *Listener) startWindow(held *heldTransfer) time.Duration {
	l.heldMutex.Lock()
	defer l.heldMutex.Unlock()
	held.deadline = l.config.now().Add(l.config.DataWindow)
	return l.config.DataWindow
}

func (l *Listener) withdrawUnopened(transfer int32, held *heldTransfer) bool {
	l.heldMutex.Lock()
	defer l.heldMutex.Unlock()
	if l.held[transfer] != held || held.opened {
		return false
	}
	delete(l.held, transfer)
	return true
}

func (l *Listener) forget(transfer int32, held *heldTransfer) {
	l.heldMutex.Lock()
	if l.held[transfer] == held && !held.opened {
		delete(l.held, transfer)
	}
	held.abandoned = true
	l.heldMutex.Unlock()
	select {
	case arrived := <-held.arrival:
		if arrived.conn != nil {
			arrived.conn.Close()
		}
	default:
	}
}

func (l *Listener) release(transfer int32, held *heldTransfer) {
	l.heldMutex.Lock()
	defer l.heldMutex.Unlock()
	if l.held[transfer] == held {
		delete(l.held, transfer)
	}
}

func (l *Listener) claim(transfer int32, from identity.Fingerprint) (*heldTransfer, error) {
	l.heldMutex.Lock()
	defer l.heldMutex.Unlock()
	held, found := l.held[transfer]
	if !found || held.opened || held.windowEnded(l.config.now()) {
		return nil, ErrDataNotAwaited
	}
	if held.peer != from {
		return nil, ErrDataFromAnotherMachine
	}
	held.opened = true
	return held, nil
}

func (h *heldTransfer) windowEnded(now time.Time) bool {
	return !h.deadline.IsZero() && now.After(h.deadline)
}

func (l *Listener) handOver(held *heldTransfer, arrived dataArrival) {
	l.heldMutex.Lock()
	abandoned := held.abandoned
	if !abandoned {
		held.arrival <- arrived
	}
	l.heldMutex.Unlock()
	if abandoned && arrived.conn != nil {
		arrived.conn.Close()
	}
}

func (l *Listener) openData(secured *tls.Conn, peer trust.Peer, transfer int32) error {
	held, err := l.claim(transfer, peer.Fingerprint)
	if err != nil {
		return err
	}
	if err := answerDataHello(secured, transfer); err != nil {
		l.release(transfer, held)
		l.handOver(held, dataArrival{err: err})
		return err
	}
	conn := newDataConn(secured, peer, transfer, l.config.DataPace, func() { l.release(transfer, held) })
	l.handOver(held, dataArrival{conn: conn})
	return nil
}

func answerDataHello(secured *tls.Conn, transfer int32) error {
	if err := wire.Write(secured, event.DataHello(transfer)); err != nil {
		return err
	}
	return secured.SetDeadline(time.Time{})
}

func exchangeDataHello(secured *tls.Conn, transfer int32) error {
	if err := wire.Write(secured, event.DataHello(transfer)); err != nil {
		return err
	}
	reply, err := wire.Read(secured)
	if errors.Is(err, io.EOF) {
		return ErrDataRefused
	}
	if err != nil {
		return err
	}
	if reply != event.DataHello(transfer) {
		return ErrDataRefused
	}
	return nil
}
