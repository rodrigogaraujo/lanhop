package pairing

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	JoinAttempts             = 3
	defaultFirstMessageLimit = 10 * time.Second
	defaultJoinsAtOnce       = 8
)

const (
	tagCentral byte = 0x01
	tagJoin    byte = 0x02
	tagWelcome byte = 0x03
	tagRefused byte = 0x04
)

const (
	refusedToken   byte = 1
	refusedOwnKey  byte = 2
	refusedTrusted byte = 3
	refusedName    byte = 4
	refusedStore   byte = 5
)

var (
	ErrWrongCentral    = errors.New("pairing: the machine at that address is not the central of this join code")
	ErrAlreadyJoined   = errors.New("pairing: this machine already trusts the central of this join code")
	ErrOtherCentral    = errors.New("pairing: this machine already trusts another central")
	ErrNotWaiting      = errors.New("pairing: the central is not waiting for a machine to join")
	ErrWrongToken      = errors.New("pairing: the central refused the join code")
	ErrTooManyAttempts = errors.New("pairing: three wrong join codes ended the wait")
	ErrWaitOver        = errors.New("pairing: the wait for a machine to join is over")
	ErrKnownToCentral  = errors.New("pairing: the central already trusts this machine")
	ErrTooManyAtOnce   = errors.New("pairing: too many connections at once")
)

type WaitConfig struct {
	Identity          *identity.Identity
	Store             *trust.Store
	OwnName           string
	Name              string
	Code              JoinCode
	Rejected          func(address string, reason error)
	FirstMessageLimit time.Duration
	AtOnce            int
}

type JoinConfig struct {
	Identity *identity.Identity
	Store    *trust.Store
	Timeout  time.Duration
}

type joinResult struct {
	peer trust.Peer
	err  error
}

type joinWait struct {
	config   WaitConfig
	mutex    sync.Mutex
	attempts int
	finished bool
	open     map[net.Conn]struct{}
	done     chan joinResult
	slots    chan struct{}
}

func WaitForJoin(ctx context.Context, listener net.Listener, config WaitConfig) (trust.Peer, error) {
	wait := &joinWait{
		config: config,
		open:   map[net.Conn]struct{}{},
		done:   make(chan joinResult, 1),
		slots:  make(chan struct{}, config.atOnce()),
	}
	accepting := make(chan error, 1)
	go func() { accepting <- wait.accept(listener) }()

	var result joinResult
	select {
	case result = <-wait.done:
	case <-ctx.Done():
		result = joinResult{err: ErrWaitOver}
	case err := <-accepting:
		result = joinResult{err: err}
	}
	wait.closeEverything(listener)
	if late, ended := wait.firstResult(); ended {
		result = late
	}
	return result.peer, result.err
}

func (w *joinWait) firstResult() (joinResult, bool) {
	select {
	case result := <-w.done:
		return result, true
	default:
		return joinResult{}, false
	}
}

func (w *joinWait) closeEverything(listener net.Listener) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.finished = true
	listener.Close()
	for conn := range w.open {
		conn.Close()
	}
}

func (w *joinWait) accept(listener net.Listener) error {
	for {
		raw, err := listener.Accept()
		if err != nil {
			return err
		}
		select {
		case w.slots <- struct{}{}:
		default:
			w.reject(raw, ErrTooManyAtOnce)
			raw.Close()
			continue
		}
		if !w.track(raw) {
			raw.Close()
			<-w.slots
			continue
		}
		go func() {
			defer func() { <-w.slots }()
			defer w.untrack(raw)
			w.serve(raw)
		}()
	}
}

func (w *joinWait) track(conn net.Conn) bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.finished {
		return false
	}
	w.open[conn] = struct{}{}
	return true
}

func (w *joinWait) untrack(conn net.Conn) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	delete(w.open, conn)
	conn.Close()
}

func (w *joinWait) serve(raw net.Conn) {
	if err := raw.SetDeadline(time.Now().Add(w.config.firstMessageLimit())); err != nil {
		return
	}
	secured := tls.Server(raw, anyCertificate(w.config.Identity))
	if err := secured.Handshake(); err != nil {
		w.reject(raw, err)
		return
	}
	if err := writeMessage(secured, slices.Concat([]byte{tagCentral}, []byte(w.config.OwnName))); err != nil {
		w.reject(raw, err)
		return
	}
	message, err := readMessage(secured, 1+tokenBytes)
	if err != nil {
		w.reject(raw, err)
		return
	}
	if len(message) != 1+tokenBytes || message[0] != tagJoin {
		w.reject(raw, ErrMalformed)
		return
	}
	presented := secured.ConnectionState().PeerCertificates
	if len(presented) == 0 {
		w.reject(raw, ErrMalformed)
		return
	}
	w.decide(secured, raw, identity.FingerprintOf(presented[0]), message[1:])
}

func (w *joinWait) decide(secured *tls.Conn, raw net.Conn, fingerprint identity.Fingerprint, token []byte) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.finished {
		return
	}
	if subtle.ConstantTimeCompare(token, w.config.Code.token[:]) != 1 {
		w.attempts++
		writeMessage(secured, []byte{tagRefused, refusedToken})
		w.reject(raw, ErrWrongToken)
		if w.attempts >= JoinAttempts {
			w.end(joinResult{err: ErrTooManyAttempts})
		}
		return
	}
	candidate := trust.Peer{Name: w.config.Name, Fingerprint: fingerprint}
	if candidate.Fingerprint == w.config.Identity.Fingerprint {
		writeMessage(secured, []byte{tagRefused, refusedOwnKey})
		w.end(joinResult{err: ErrSelf})
		return
	}
	if err := w.config.Store.Add(candidate); err != nil {
		reason, refusal := refusalFor(err, candidate.Name)
		writeMessage(secured, []byte{tagRefused, reason})
		w.end(joinResult{err: refusal})
		return
	}
	if err := writeMessage(secured, []byte{tagWelcome}); err != nil {
		w.end(joinResult{peer: candidate, err: fmt.Errorf("pairing: %q is stored here, but the welcome did not reach it. To start over, run: lanhop forget %s: %w", candidate.Name, candidate.Name, err)})
		return
	}
	w.end(joinResult{peer: candidate})
}

func refusalFor(err error, name string) (byte, error) {
	switch {
	case errors.Is(err, trust.ErrKeyInUse):
		return refusedTrusted, fmt.Errorf("%w: %w", ErrKnownToCentral, err)
	case errors.Is(err, trust.ErrNameInUse):
		return refusedName, fmt.Errorf("%w: %q", ErrNameTaken, name)
	}
	return refusedStore, err
}

func (w *joinWait) end(result joinResult) {
	w.finished = true
	select {
	case w.done <- result:
	default:
	}
}

func (w *joinWait) reject(conn net.Conn, reason error) {
	if w.config.Rejected == nil {
		return
	}
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		host = conn.RemoteAddr().String()
	}
	w.config.Rejected(host, reason)
}

func (c WaitConfig) firstMessageLimit() time.Duration {
	if c.FirstMessageLimit == 0 {
		return defaultFirstMessageLimit
	}
	return c.FirstMessageLimit
}

func (c WaitConfig) atOnce() int {
	if c.AtOnce == 0 {
		return defaultJoinsAtOnce
	}
	return c.AtOnce
}

func Join(address string, code JoinCode, config JoinConfig) (trust.Peer, error) {
	if code.names(config.Identity.Fingerprint) {
		return trust.Peer{}, ErrSelf
	}
	for _, peer := range config.Store.Peers() {
		if code.names(peer.Fingerprint) {
			return peer, ErrAlreadyJoined
		}
	}
	for _, peer := range config.Store.Peers() {
		if peer.Address != "" {
			return trust.Peer{}, fmt.Errorf("%w, %q. To join this one instead, first run: lanhop forget %s", ErrOtherCentral, peer.Name, peer.Name)
		}
	}
	return exchange(address, code, config)
}

func exchange(address string, code JoinCode, config JoinConfig) (trust.Peer, error) {
	host, port := splitAddress(address)
	deadline := time.Now().Add(config.timeout())
	dialer := net.Dialer{Deadline: deadline}
	raw, err := dialer.Dial("tcp", net.JoinHostPort(host, port))
	if errors.Is(err, syscall.ECONNREFUSED) {
		return trust.Peer{}, ErrNotWaiting
	}
	if err != nil {
		return trust.Peer{}, fmt.Errorf("pairing: cannot connect to %s: %w", address, err)
	}
	settings := anyCertificate(config.Identity)
	settings.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 || !code.names(identity.FingerprintOf(state.PeerCertificates[0])) {
			return ErrWrongCentral
		}
		return nil
	}
	secured := tls.Client(raw, settings)
	defer secured.Close()
	if err := secured.SetDeadline(deadline); err != nil {
		return trust.Peer{}, err
	}
	if err := secured.Handshake(); err != nil {
		if errors.Is(err, ErrWrongCentral) {
			return trust.Peer{}, ErrWrongCentral
		}
		return trust.Peer{}, interrupted(err)
	}
	central, err := meetCentral(secured, config.Store)
	if err != nil {
		return trust.Peer{}, err
	}
	if err := writeMessage(secured, slices.Concat([]byte{tagJoin}, code.token[:])); err != nil {
		return trust.Peer{}, interrupted(err)
	}
	answer, err := readMessage(secured, 2)
	if err != nil {
		return trust.Peer{}, interrupted(err)
	}
	if err := welcomed(answer, central.Name); err != nil {
		return trust.Peer{}, err
	}
	central.Address = net.JoinHostPort(host, strconv.Itoa(transport.DefaultPort))
	if err := config.Store.Add(central); err != nil {
		return trust.Peer{}, err
	}
	return central, nil
}

func meetCentral(secured *tls.Conn, store *trust.Store) (trust.Peer, error) {
	message, err := readMessage(secured, 1+trust.MaxNameLength)
	if err != nil {
		return trust.Peer{}, interrupted(err)
	}
	if message[0] != tagCentral || !trust.ValidName(string(message[1:])) {
		return trust.Peer{}, ErrMalformed
	}
	name := string(message[1:])
	for _, peer := range store.Peers() {
		if peer.Name == name {
			return trust.Peer{}, fmt.Errorf("%w here: %q. To join this central, first run: lanhop forget %s", ErrNameTaken, name, name)
		}
	}
	presented := secured.ConnectionState().PeerCertificates
	return trust.Peer{Name: name, Fingerprint: identity.FingerprintOf(presented[0])}, nil
}

func welcomed(answer []byte, central string) error {
	switch {
	case len(answer) == 1 && answer[0] == tagWelcome:
		return nil
	case len(answer) == 2 && answer[0] == tagRefused:
		switch answer[1] {
		case refusedToken:
			return ErrWrongToken
		case refusedOwnKey:
			return ErrSelf
		case refusedTrusted:
			return fmt.Errorf("%w. On %s, run lanhop forget with the name of this machine, then lanhop add again", ErrKnownToCentral, central)
		case refusedName:
			return fmt.Errorf("%w on %s. Run lanhop add there again with another name", ErrNameTaken, central)
		case refusedStore:
			return fmt.Errorf("pairing: %s could not store this machine. Its terminal says why", central)
		}
	}
	return ErrMalformed
}

func (c JoinConfig) timeout() time.Duration {
	if c.Timeout == 0 {
		return defaultTimeout
	}
	return c.Timeout
}
