package transport

import (
	"crypto/tls"
	"net"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
	"github.com/rodrigogaraujo/lanhop/internal/wire"
)

type Listener struct {
	listener  net.Listener
	config    Config
	trusted   Trusted
	tlsConfig *tls.Config
	ready     chan *Conn
	pending   chan struct{}
	closing   sync.Once
	closed    chan struct{}

	awaitingMutex sync.Mutex
	awaiting      map[int32]*awaitedData
}

func Listen(address string, config Config, trusted Trusted) (*Listener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	config = config.withDefaults()
	l := &Listener{
		listener:  listener,
		config:    config,
		trusted:   trusted,
		tlsConfig: acceptOnlyPaired(config.Identity, trusted),
		ready:     make(chan *Conn),
		pending:   make(chan struct{}, pendingHandshakes),
		closed:    make(chan struct{}),
		awaiting:  map[int32]*awaitedData{},
	}
	go l.acceptLoop()
	return l, nil
}

func (l *Listener) Addr() net.Addr {
	return l.listener.Addr()
}

func (l *Listener) Accept() (*Conn, error) {
	select {
	case conn := <-l.ready:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *Listener) Close() error {
	var err error
	l.closing.Do(func() {
		close(l.closed)
		err = l.listener.Close()
	})
	return err
}

func (l *Listener) acceptLoop() {
	for {
		raw, err := l.listener.Accept()
		if err != nil {
			return
		}
		select {
		case l.pending <- struct{}{}:
			go l.welcome(raw)
		case <-l.closed:
			raw.Close()
			return
		}
	}
}

func (l *Listener) welcome(raw net.Conn) {
	defer func() { <-l.pending }()

	secured := tls.Server(raw, l.tlsConfig)
	if err := l.open(secured); err != nil {
		secured.Close()
		if l.config.OnReject != nil {
			l.config.OnReject(raw.RemoteAddr(), err)
		}
	}
}

func (l *Listener) open(secured *tls.Conn) error {
	peer, first, err := l.authenticate(secured)
	if err != nil {
		return err
	}
	if first.IsControl() && first.Code == event.ControlDataHello {
		return l.openData(secured, peer, first.Value)
	}
	return l.openControl(secured, peer, first)
}

func (l *Listener) authenticate(secured *tls.Conn) (trust.Peer, event.Event, error) {
	if err := secured.SetDeadline(time.Now().Add(l.config.HandshakeTimeout)); err != nil {
		return trust.Peer{}, event.Event{}, err
	}
	if err := secured.Handshake(); err != nil {
		return trust.Peer{}, event.Event{}, err
	}
	first, err := wire.Read(secured)
	if err != nil {
		return trust.Peer{}, event.Event{}, err
	}
	fingerprint, presented := fingerprintOfPeer(secured.ConnectionState())
	if !presented {
		return trust.Peer{}, event.Event{}, ErrUntrusted
	}
	peer, paired := l.trusted.Find(fingerprint)
	if !paired {
		return trust.Peer{}, event.Event{}, ErrUntrusted
	}
	return peer, first, nil
}

func (l *Listener) openControl(secured *tls.Conn, peer trust.Peer, greeting event.Event) error {
	if err := wire.Write(secured, event.Hello()); err != nil {
		return err
	}
	if err := checkHello(greeting); err != nil {
		return err
	}
	if err := secured.SetDeadline(time.Time{}); err != nil {
		return err
	}
	conn := newConn(secured, peer, l.config)
	select {
	case l.ready <- conn:
	case <-l.closed:
		conn.Close()
	}
	return nil
}
