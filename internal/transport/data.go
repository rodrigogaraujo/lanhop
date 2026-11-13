package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"sync"
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
)

type DataConn struct {
	secured  *tls.Conn
	peer     trust.Peer
	transfer int32
	writing  sync.Mutex
	pace     pace
}

func newDataConn(secured *tls.Conn, peer trust.Peer, transfer int32, config Config) *DataConn {
	return &DataConn{
		secured:  secured,
		peer:     peer,
		transfer: transfer,
		pace:     pace{bytesPerSecond: float64(config.DataBytesPerSecond)},
	}
}

func (d *DataConn) Peer() trust.Peer {
	return d.peer
}

func (d *DataConn) Transfer() int32 {
	return d.transfer
}

func (d *DataConn) Read(buffer []byte) (int, error) {
	return d.secured.Read(buffer)
}

func (d *DataConn) Write(data []byte) (int, error) {
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

func (d *DataConn) Close() error {
	return d.secured.Close()
}

type awaitedData struct {
	peer      identity.Fingerprint
	deadline  time.Time
	arrival   chan dataArrival
	abandoned bool
}

type dataArrival struct {
	conn *DataConn
	err  error
}

func (l *Listener) AwaitData(ctx context.Context, peer identity.Fingerprint, announce func(transfer int32) error) (*DataConn, error) {
	transfer, awaited := l.expectData(peer)
	defer l.forget(transfer, awaited)
	if err := announce(transfer); err != nil {
		return nil, err
	}
	window := time.NewTimer(l.startWindow(awaited))
	defer window.Stop()
	for {
		select {
		case arrived := <-awaited.arrival:
			return arrived.conn, arrived.err
		case <-window.C:
			if l.withdrawUnclaimed(transfer, awaited) {
				return nil, ErrDataNotOpened
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-l.closed:
			return nil, net.ErrClosed
		}
	}
}

func (l *Listener) expectData(peer identity.Fingerprint) (int32, *awaitedData) {
	awaited := &awaitedData{peer: peer, arrival: make(chan dataArrival, 1)}
	l.awaitingMutex.Lock()
	defer l.awaitingMutex.Unlock()
	for {
		transfer := rand.Int32N(event.TransferNumberMax) + 1
		if _, taken := l.awaiting[transfer]; !taken {
			l.awaiting[transfer] = awaited
			return transfer, awaited
		}
	}
}

func (l *Listener) startWindow(awaited *awaitedData) time.Duration {
	l.awaitingMutex.Lock()
	defer l.awaitingMutex.Unlock()
	awaited.deadline = time.Now().Add(l.config.DataWindow)
	return l.config.DataWindow
}

func (l *Listener) withdrawUnclaimed(transfer int32, awaited *awaitedData) bool {
	l.awaitingMutex.Lock()
	defer l.awaitingMutex.Unlock()
	if l.awaiting[transfer] != awaited {
		return false
	}
	delete(l.awaiting, transfer)
	return true
}

func (l *Listener) forget(transfer int32, awaited *awaitedData) {
	l.awaitingMutex.Lock()
	if l.awaiting[transfer] == awaited {
		delete(l.awaiting, transfer)
	}
	awaited.abandoned = true
	l.awaitingMutex.Unlock()
	select {
	case arrived := <-awaited.arrival:
		if arrived.conn != nil {
			arrived.conn.Close()
		}
	default:
	}
}

func (l *Listener) claim(transfer int32, from identity.Fingerprint) (*awaitedData, error) {
	l.awaitingMutex.Lock()
	defer l.awaitingMutex.Unlock()
	awaited, found := l.awaiting[transfer]
	if !found || awaited.windowEnded(time.Now()) {
		return nil, ErrDataNotAwaited
	}
	if awaited.peer != from {
		return nil, ErrDataFromAnotherMachine
	}
	delete(l.awaiting, transfer)
	return awaited, nil
}

func (a *awaitedData) windowEnded(now time.Time) bool {
	return !a.deadline.IsZero() && now.After(a.deadline)
}

func (l *Listener) handOver(awaited *awaitedData, arrived dataArrival) {
	l.awaitingMutex.Lock()
	abandoned := awaited.abandoned
	if !abandoned {
		awaited.arrival <- arrived
	}
	l.awaitingMutex.Unlock()
	if abandoned && arrived.conn != nil {
		arrived.conn.Close()
	}
}

func (l *Listener) openData(secured *tls.Conn, peer trust.Peer, transfer int32) error {
	awaited, err := l.claim(transfer, peer.Fingerprint)
	if err != nil {
		return err
	}
	if err := answerDataHello(secured, transfer); err != nil {
		l.handOver(awaited, dataArrival{err: err})
		return err
	}
	l.handOver(awaited, dataArrival{conn: newDataConn(secured, peer, transfer, l.config)})
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

const (
	pacedSlice = 16 << 10
	paceBurst  = 2 * pacedSlice
)

type pace struct {
	bytesPerSecond float64
	allowance      float64
	refilled       time.Time
}

func (p *pace) wait(bytes int) {
	p.refill()
	if short := float64(bytes) - p.allowance; short > 0 {
		time.Sleep(time.Duration(short / p.bytesPerSecond * float64(time.Second)))
		p.refill()
	}
	p.allowance -= float64(bytes)
}

func (p *pace) refill() {
	now := time.Now()
	if p.refilled.IsZero() {
		p.allowance = paceBurst
	} else {
		p.allowance = min(paceBurst, p.allowance+now.Sub(p.refilled).Seconds()*p.bytesPerSecond)
	}
	p.refilled = now
}
