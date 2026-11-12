package transport

import (
	"crypto/tls"
	"net"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/trust"
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
	peer, err := l.authenticate(secured)
	if err != nil {
		secured.Close()
		if l.config.OnReject != nil {
			l.config.OnReject(raw.RemoteAddr(), err)
		}
		return
	}

	conn := newConn(secured, peer, l.config)
	select {
	case l.ready <- conn:
	case <-l.closed:
		conn.Close()
	}
}

func (l *Listener) authenticate(secured *tls.Conn) (trust.Peer, error) {
	if err := secured.SetDeadline(time.Now().Add(l.config.HandshakeTimeout)); err != nil {
		return trust.Peer{}, err
	}
	if err := secured.Handshake(); err != nil {
		return trust.Peer{}, err
	}
	if err := exchangeHello(secured); err != nil {
		return trust.Peer{}, err
	}
	fingerprint, presented := fingerprintOfPeer(secured.ConnectionState())
	if !presented {
		return trust.Peer{}, ErrUntrusted
	}
	peer, paired := l.trusted.Find(fingerprint)
	if !paired {
		return trust.Peer{}, ErrUntrusted
	}
	return peer, secured.SetDeadline(time.Time{})
}
