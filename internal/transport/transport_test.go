package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
	"github.com/rodrigogaraujo/lanhop/internal/wire"
)

const (
	testPingInterval = 20 * time.Millisecond
	testSilenceLimit = 300 * time.Millisecond
	testPatience     = 5 * time.Second
)

type pairedWith map[identity.Fingerprint]trust.Peer

func (p pairedWith) Find(fingerprint identity.Fingerprint) (trust.Peer, bool) {
	peer, found := p[fingerprint]
	return peer, found
}

func pairedWithMachine(name string, machine *identity.Identity) pairedWith {
	return pairedWith{machine.Fingerprint: {Name: name, Fingerprint: machine.Fingerprint}}
}

func newIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	machine, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	return machine
}

func configFor(machine *identity.Identity) Config {
	return Config{Identity: machine, PingInterval: testPingInterval, SilenceLimit: testSilenceLimit}
}

type rejections chan error

func (r rejections) record(_ net.Addr, reason error) { r <- reason }

func (r rejections) next(t *testing.T) error {
	t.Helper()
	select {
	case reason := <-r:
		return reason
	case <-time.After(testPatience):
		t.Fatal("the listener rejected nothing")
		return nil
	}
}

func listen(t *testing.T, machine *identity.Identity, trusted Trusted) (*Listener, rejections) {
	t.Helper()
	rejected := make(rejections, 8)
	config := configFor(machine)
	config.OnReject = rejected.record
	listener, err := Listen("127.0.0.1:0", config, trusted)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener, rejected
}

func reach(name string, machine *identity.Identity, listener *Listener) trust.Peer {
	return trust.Peer{Name: name, Fingerprint: machine.Fingerprint, Address: listener.Addr().String()}
}

func dial(t *testing.T, machine *identity.Identity, peer trust.Peer) *Conn {
	t.Helper()
	conn, err := Dial(context.Background(), configFor(machine), peer)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func accept(t *testing.T, listener *Listener) *Conn {
	t.Helper()
	accepted := make(chan *Conn, 1)
	go func() {
		if conn, err := listener.Accept(); err == nil {
			accepted <- conn
		}
	}()
	select {
	case conn := <-accepted:
		t.Cleanup(func() { conn.Close() })
		return conn
	case <-time.After(testPatience):
		t.Fatal("the listener accepted nothing")
		return nil
	}
}

func expectNothingAccepted(t *testing.T, listener *Listener) {
	t.Helper()
	accepted := make(chan *Conn, 1)
	go func() {
		if conn, err := listener.Accept(); err == nil {
			accepted <- conn
		}
	}()
	select {
	case conn := <-accepted:
		conn.Close()
		t.Error("the listener accepted a connection it had to refuse")
	case <-time.After(testSilenceLimit):
	}
}

func rawClient(t *testing.T, machine *identity.Identity, peer trust.Peer) *tls.Conn {
	t.Helper()
	raw, err := tls.Dial("tcp", peer.Address, connectOnlyTo(machine, peer.Fingerprint))
	if err != nil {
		t.Fatalf("tls.Dial: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	return raw
}

func receive(t *testing.T, conn *Conn) event.Event {
	t.Helper()
	received, err := conn.Receive()
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	return received
}

func pairedMachines(t *testing.T) (sharerSide, receiverSide *Conn) {
	t.Helper()
	sharer, receiver := newIdentity(t), newIdentity(t)
	listener, _ := listen(t, sharer, pairedWithMachine("laptop", receiver))
	receiverSide = dial(t, receiver, reach("desktop", sharer, listener))
	return accept(t, listener), receiverSide
}

func TestPairedMachinesExchangeEventsInBothDirections(t *testing.T) {
	sharerSide, receiverSide := pairedMachines(t)

	typed := []event.Event{event.Key(event.KeyA, event.KeyPressed), event.Sync()}
	if err := sharerSide.Send(typed...); err != nil {
		t.Fatalf("Send: %v", err)
	}
	for i, want := range typed {
		if got := receive(t, receiverSide); got != want {
			t.Errorf("receiver got event %d = %+v, want %+v", i, got, want)
		}
	}

	if err := receiverSide.Send(event.Leave()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := receive(t, sharerSide); got != event.Leave() {
		t.Errorf("sharer got %+v, want %+v", got, event.Leave())
	}
}

func TestEachSideKnowsTheNameOfTheOther(t *testing.T) {
	sharerSide, receiverSide := pairedMachines(t)

	if got := sharerSide.Peer().Name; got != "laptop" {
		t.Errorf("sharer sees %q, want laptop", got)
	}
	if got := receiverSide.Peer().Name; got != "desktop" {
		t.Errorf("receiver sees %q, want desktop", got)
	}
}

func TestConnectionUsesTLS13(t *testing.T) {
	sharerSide, receiverSide := pairedMachines(t)

	for name, conn := range map[string]*Conn{"sharer": sharerSide, "receiver": receiverSide} {
		if version := conn.secured.ConnectionState().Version; version != tls.VersionTLS13 {
			t.Errorf("%s negotiated TLS version %#x, want TLS 1.3", name, version)
		}
	}
}

func TestListenerRefusesAMachineItIsNotPairedWith(t *testing.T) {
	sharer, stranger := newIdentity(t), newIdentity(t)
	listener, rejected := listen(t, sharer, pairedWith{})

	conn, err := Dial(context.Background(), configFor(stranger), reach("desktop", sharer, listener))
	if err == nil {
		conn.Close()
		t.Fatal("an unpaired machine connected")
	}
	if reason := rejected.next(t); !errors.Is(reason, ErrUntrusted) {
		t.Errorf("rejection = %v, want ErrUntrusted", reason)
	}
	expectNothingAccepted(t, listener)
}

func TestDialRefusesAListenerWithAnotherIdentity(t *testing.T) {
	impostor, receiver, expected := newIdentity(t), newIdentity(t), newIdentity(t)
	listener, _ := listen(t, impostor, pairedWithMachine("laptop", receiver))

	conn, err := Dial(context.Background(), configFor(receiver), reach("desktop", expected, listener))
	if err == nil {
		conn.Close()
		t.Fatal("the receiver connected to a machine that is not its sharer")
	}
	if !errors.Is(err, ErrUntrusted) {
		t.Errorf("Dial = %v, want ErrUntrusted", err)
	}
}

func TestListenerRefusesOlderTLSVersions(t *testing.T) {
	sharer, receiver := newIdentity(t), newIdentity(t)
	listener, _ := listen(t, sharer, pairedWithMachine("laptop", receiver))

	outdated := connectOnlyTo(receiver, sharer.Fingerprint)
	outdated.MinVersion = tls.VersionTLS12
	outdated.MaxVersion = tls.VersionTLS12
	if raw, err := tls.Dial("tcp", listener.Addr().String(), outdated); err == nil {
		raw.Close()
		t.Error("a TLS 1.2 client connected")
	}
}

func TestListenerRefusesWrongGreetings(t *testing.T) {
	cases := []struct {
		name     string
		greeting event.Event
		want     error
	}{
		{"another protocol version", event.Event{Type: event.TypeControl, Code: event.ControlHello, Value: event.ProtocolVersion + 1}, ErrVersion},
		{"an input event instead of hello", event.Key(event.KeyA, event.KeyPressed), ErrNotLanhop},
		{"another control event instead of hello", event.Ping(), ErrNotLanhop},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sharer, receiver := newIdentity(t), newIdentity(t)
			listener, rejected := listen(t, sharer, pairedWithMachine("laptop", receiver))

			raw := rawClient(t, receiver, reach("desktop", sharer, listener))
			if err := wire.Write(raw, c.greeting); err != nil {
				t.Fatalf("Write: %v", err)
			}

			if reason := rejected.next(t); !errors.Is(reason, c.want) {
				t.Errorf("rejection = %v, want %v", reason, c.want)
			}
			expectNothingAccepted(t, listener)
		})
	}
}

func TestSilentPeerIsDroppedWithinTheSilenceLimit(t *testing.T) {
	sharer, receiver := newIdentity(t), newIdentity(t)
	listener, _ := listen(t, sharer, pairedWithMachine("laptop", receiver))

	silent := rawClient(t, receiver, reach("desktop", sharer, listener))
	if err := exchangeHello(silent); err != nil {
		t.Fatalf("exchangeHello: %v", err)
	}
	conn := accept(t, listener)

	started := time.Now()
	_, err := conn.Receive()
	waited := time.Since(started)

	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("Receive = %v, want a deadline error", err)
	}
	if waited > 2*testSilenceLimit {
		t.Errorf("the silent peer was dropped after %s, want at most %s", waited, 2*testSilenceLimit)
	}
}

func TestIdleConnectionStaysAliveAndPingsAreNotDelivered(t *testing.T) {
	sharerSide, receiverSide := pairedMachines(t)

	type outcome struct {
		received event.Event
		err      error
	}
	fromReceiver := make(chan outcome, 1)
	fromSharer := make(chan outcome, 1)
	go func() {
		received, err := receiverSide.Receive()
		fromReceiver <- outcome{received, err}
	}()
	go func() {
		received, err := sharerSide.Receive()
		fromSharer <- outcome{received, err}
	}()

	time.Sleep(3 * testSilenceLimit)
	typed := event.Key(event.KeyA, event.KeyPressed)
	if err := sharerSide.Send(typed); err != nil {
		t.Fatalf("Send after idling: %v", err)
	}
	if err := receiverSide.Send(event.Leave()); err != nil {
		t.Fatalf("Send after idling: %v", err)
	}

	if got := <-fromReceiver; got.err != nil || got.received != typed {
		t.Errorf("receiver got %+v, %v, want the key with no error", got.received, got.err)
	}
	if got := <-fromSharer; got.err != nil || got.received != event.Leave() {
		t.Errorf("sharer got %+v, %v, want leave with no error", got.received, got.err)
	}
}

func TestASlowHandshakeDoesNotBlockOtherMachines(t *testing.T) {
	sharer, receiver := newIdentity(t), newIdentity(t)
	listener, _ := listen(t, sharer, pairedWithMachine("laptop", receiver))

	stalled, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial: %v", err)
	}
	defer stalled.Close()

	dial(t, receiver, reach("desktop", sharer, listener))
	if got := accept(t, listener).Peer().Name; got != "laptop" {
		t.Errorf("accepted %q, want laptop", got)
	}
}

func TestAcceptReturnsWhenTheListenerIsClosed(t *testing.T) {
	listener, _ := listen(t, newIdentity(t), pairedWith{})

	result := make(chan error, 1)
	go func() {
		_, err := listener.Accept()
		result <- err
	}()
	listener.Close()

	select {
	case err := <-result:
		if !errors.Is(err, net.ErrClosed) {
			t.Errorf("Accept = %v, want net.ErrClosed", err)
		}
	case <-time.After(testPatience):
		t.Fatal("Accept did not return after Close")
	}
}

func TestClosingAConnectionEndsBothSides(t *testing.T) {
	sharerSide, receiverSide := pairedMachines(t)

	if err := sharerSide.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sharerSide.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if err := sharerSide.Send(event.Sync()); err == nil {
		t.Error("Send on a closed connection succeeded")
	}
	if _, err := receiverSide.Receive(); err == nil {
		t.Error("Receive succeeded after the other side closed")
	}
}

type recordingRelay struct {
	listener net.Listener
	mu       sync.Mutex
	seen     bytes.Buffer
}

func relayTo(t *testing.T, target string) *recordingRelay {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	relay := &recordingRelay{listener: listener}
	go func() {
		incoming, err := listener.Accept()
		if err != nil {
			return
		}
		outgoing, err := net.Dial("tcp", target)
		if err != nil {
			incoming.Close()
			return
		}
		go relay.copyAndRecord(outgoing, incoming)
		relay.copyAndRecord(incoming, outgoing)
	}()
	return relay
}

func (r *recordingRelay) copyAndRecord(to, from net.Conn) {
	defer to.Close()
	buffer := make([]byte, 4096)
	for {
		n, err := from.Read(buffer)
		if n > 0 {
			r.mu.Lock()
			r.seen.Write(buffer[:n])
			r.mu.Unlock()
			if _, writeErr := to.Write(buffer[:n]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (r *recordingRelay) recorded() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return bytes.Clone(r.seen.Bytes())
}

func TestEventsCannotBeReadOnTheNetwork(t *testing.T) {
	sharer, receiver := newIdentity(t), newIdentity(t)
	listener, _ := listen(t, sharer, pairedWithMachine("laptop", receiver))
	relay := relayTo(t, listener.Addr().String())

	throughRelay := trust.Peer{Name: "desktop", Fingerprint: sharer.Fingerprint, Address: relay.listener.Addr().String()}
	receiverSide := dial(t, receiver, throughRelay)
	sharerSide := accept(t, listener)

	secret := event.Event{Type: 0x4C41, Code: 0x4E48, Value: 0x4F502121}
	for range 20 {
		if err := sharerSide.Send(secret); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	for range 20 {
		if got := receive(t, receiverSide); got != secret {
			t.Fatalf("receiver got %+v, want %+v", got, secret)
		}
	}

	captured := relay.recorded()
	if len(captured) == 0 {
		t.Fatal("the relay captured nothing")
	}
	if bytes.Contains(captured, wire.Append(nil, secret)) {
		t.Error("the event travelled in clear text")
	}
	if bytes.Contains(captured, wire.Append(nil, event.Hello())) {
		t.Error("the greeting travelled in clear text")
	}
}
