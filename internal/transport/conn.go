package transport

import (
	"crypto/tls"
	"fmt"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
	"github.com/rodrigogaraujo/lanhop/internal/wire"
)

type Conn struct {
	secured      *tls.Conn
	peer         trust.Peer
	silenceLimit time.Duration
	sending      sync.Mutex
	closing      sync.Once
	closed       chan struct{}
	closeErr     error
}

func newConn(secured *tls.Conn, peer trust.Peer, config Config) *Conn {
	conn := &Conn{
		secured:      secured,
		peer:         peer,
		silenceLimit: config.SilenceLimit,
		closed:       make(chan struct{}),
	}
	go conn.keepAlive(config.PingInterval)
	return conn
}

func (c *Conn) Peer() trust.Peer {
	return c.peer
}

func (c *Conn) Send(events ...event.Event) error {
	c.sending.Lock()
	defer c.sending.Unlock()
	if err := c.secured.SetWriteDeadline(time.Now().Add(c.silenceLimit)); err != nil {
		return err
	}
	return wire.Write(c.secured, events...)
}

func (c *Conn) Receive() (event.Event, error) {
	for {
		if err := c.secured.SetReadDeadline(time.Now().Add(c.silenceLimit)); err != nil {
			return event.Event{}, err
		}
		received, err := wire.Read(c.secured)
		if err != nil {
			return event.Event{}, fmt.Errorf("transport: connection to %s lost: %w", c.peer.Name, err)
		}
		if received.IsControl() && received.Code == event.ControlPing {
			continue
		}
		return received, nil
	}
}

func (c *Conn) Close() error {
	c.closing.Do(func() {
		close(c.closed)
		c.closeErr = c.secured.Close()
	})
	return c.closeErr
}

func (c *Conn) keepAlive(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-ticker.C:
			if c.Send(event.Ping()) != nil {
				return
			}
		}
	}
}

func exchangeHello(secured *tls.Conn) error {
	if err := wire.Write(secured, event.Hello()); err != nil {
		return err
	}
	reply, err := wire.Read(secured)
	if err != nil {
		return err
	}
	return checkHello(reply)
}

func checkHello(greeting event.Event) error {
	if !greeting.IsControl() || greeting.Code != event.ControlHello {
		return ErrNotLanhop
	}
	if greeting.Value != event.ProtocolVersion {
		return fmt.Errorf("%w: it speaks version %d, this machine speaks version %d", ErrVersion, greeting.Value, event.ProtocolVersion)
	}
	return nil
}
