package receiver

import (
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	testPingInterval = 20 * time.Millisecond
	testSilenceLimit = 300 * time.Millisecond
	testPatience     = 5 * time.Second
	promptly         = 500 * time.Millisecond
	loopback         = "127.0.0.1:0"

	keyB      uint16 = 48
	leftShift uint16 = 42
	pressed          = event.KeyPressed
	released         = event.KeyReleased
)

var quickRetries = retryDelays{first: 10 * time.Millisecond, longest: 50 * time.Millisecond}

type pairedWith map[identity.Fingerprint]trust.Peer

func (p pairedWith) Find(fingerprint identity.Fingerprint) (trust.Peer, bool) {
	peer, found := p[fingerprint]
	return peer, found
}

func newIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	machine, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	return machine
}

func transportFor(machine *identity.Identity) transport.Config {
	return transport.Config{Identity: machine, PingInterval: testPingInterval, SilenceLimit: testSilenceLimit}
}

type recordedReplay struct {
	mutex   sync.Mutex
	emitted []event.Event
	closes  int
	failure error
}

func (r *recordedReplay) Emit(events ...event.Event) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.failure != nil {
		return r.failure
	}
	r.emitted = append(r.emitted, events...)
	return nil
}

func (r *recordedReplay) Close() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.closes++
	return nil
}

func (r *recordedReplay) replayed() []event.Event {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return slices.Clone(r.emitted)
}

func (r *recordedReplay) timesClosed() int {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.closes
}

func (r *recordedReplay) expect(t *testing.T, want ...event.Event) {
	t.Helper()
	for deadline := time.Now().Add(testPatience); ; time.Sleep(2 * time.Millisecond) {
		got := r.replayed()
		if slices.Equal(got, want) {
			return
		}
		stillArriving := len(got) < len(want) && slices.Equal(got, want[:len(got)])
		if !stillArriving || time.Now().After(deadline) {
			t.Fatalf("replayed\n%+v\nwant\n%+v", got, want)
		}
	}
}

type failure struct {
	at     time.Time
	reason error
}

type desk struct {
	sharer      *identity.Identity
	receiver    *identity.Identity
	replay      *recordedReplay
	clipboard   Clipboard
	stop        context.CancelFunc
	finished    chan error
	mutex       sync.Mutex
	connections int
	failures    []failure
}

func newDesk(t *testing.T) *desk {
	t.Helper()
	return &desk{sharer: newIdentity(t), receiver: newIdentity(t), replay: &recordedReplay{}, finished: make(chan error, 1)}
}

func (d *desk) sharerListens(t *testing.T, address string) *transport.Listener {
	t.Helper()
	trusted := pairedWith{d.receiver.Fingerprint: {Name: "laptop", Fingerprint: d.receiver.Fingerprint}}
	listener, err := transport.Listen(address, transportFor(d.sharer), trusted)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener
}

func (d *desk) config(sharerAddress string) Config {
	return Config{
		Transport: transportFor(d.receiver),
		Sharer:    trust.Peer{Name: "desk", Fingerprint: d.sharer.Fingerprint, Address: sharerAddress},
		Replay:    d.replay,
		Clipboard: d.clipboard,
		OnConnect: func() {
			d.mutex.Lock()
			defer d.mutex.Unlock()
			d.connections++
		},
		OnFailure: func(reason error) {
			d.mutex.Lock()
			defer d.mutex.Unlock()
			d.failures = append(d.failures, failure{time.Now(), reason})
		},
	}
}

func (d *desk) start(t *testing.T, receive func(ctx context.Context) error) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	d.stop = stop
	go func() { d.finished <- receive(ctx) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-d.finished:
		case <-time.After(testPatience):
		}
	})
}

func (d *desk) receiverRuns(t *testing.T, sharerAddress string) {
	t.Helper()
	d.receiverRunsRetrying(t, sharerAddress, quickRetries)
}

func (d *desk) receiverRunsRetrying(t *testing.T, sharerAddress string, delays retryDelays) {
	t.Helper()
	config := d.config(sharerAddress)
	d.start(t, func(ctx context.Context) error { return run(ctx, config, delays) })
}

func (d *desk) timesConnected() int {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.connections
}

func (d *desk) timesFailed() int {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return len(d.failures)
}

func (d *desk) failuresAfter(t *testing.T, count int) []failure {
	t.Helper()
	for deadline := time.Now().Add(testPatience); time.Now().Before(deadline); time.Sleep(2 * time.Millisecond) {
		d.mutex.Lock()
		seen := slices.Clone(d.failures)
		d.mutex.Unlock()
		if len(seen) >= count {
			return seen[:count]
		}
	}
	t.Fatalf("the receiver did not report %d failed connections", count)
	return nil
}

func (d *desk) stopsWithin(t *testing.T, limit time.Duration) error {
	t.Helper()
	asked := time.Now()
	d.stop()
	select {
	case err := <-d.finished:
		d.finished <- err
		return err
	case <-time.After(limit):
		t.Fatalf("the receiver was still running %s after it was told to stop", time.Since(asked))
		return nil
	}
}

func accept(t *testing.T, listener *transport.Listener) *transport.Conn {
	t.Helper()
	accepted := make(chan *transport.Conn, 1)
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
		t.Fatal("the receiver did not connect to the sharer")
		return nil
	}
}

func send(t *testing.T, sharerSide *transport.Conn, events ...event.Event) {
	t.Helper()
	if err := sharerSide.Send(events...); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func connectedDesk(t *testing.T) (*desk, *transport.Listener, *transport.Conn) {
	t.Helper()
	d := newDesk(t)
	listener := d.sharerListens(t, loopback)
	d.receiverRuns(t, listener.Addr().String())
	return d, listener, accept(t, listener)
}

type heldAddress struct {
	listener net.Listener
	mutex    sync.Mutex
	sharer   string
	piped    []net.Conn
}

func holdAddress(t *testing.T) *heldAddress {
	t.Helper()
	listener, err := net.Listen("tcp", loopback)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	held := &heldAddress{listener: listener}
	t.Cleanup(held.close)
	go held.serve()
	return held
}

func (h *heldAddress) String() string {
	return h.listener.Addr().String()
}

func (h *heldAddress) passTo(t *testing.T, sharer *transport.Listener) {
	t.Helper()
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.sharer = sharer.Addr().String()
	t.Cleanup(h.refuse)
}

func (h *heldAddress) refuse() {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.sharer = ""
	for _, conn := range h.piped {
		conn.Close()
	}
	h.piped = nil
}

func (h *heldAddress) close() {
	h.listener.Close()
	h.refuse()
}

func (h *heldAddress) serve() {
	for {
		receiver, err := h.listener.Accept()
		if err != nil {
			return
		}
		go h.pass(receiver)
	}
}

func (h *heldAddress) pass(receiver net.Conn) {
	sharer, reached := h.reachTheSharer(receiver)
	if !reached {
		receiver.Close()
		return
	}
	go pipe(sharer, receiver)
	pipe(receiver, sharer)
}

func (h *heldAddress) reachTheSharer(receiver net.Conn) (net.Conn, bool) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if h.sharer == "" {
		return nil, false
	}
	sharer, err := net.Dial("tcp", h.sharer)
	if err != nil {
		return nil, false
	}
	h.piped = append(h.piped, receiver, sharer)
	return sharer, true
}

func pipe(to, from net.Conn) {
	io.Copy(to, from)
	to.Close()
	from.Close()
}

func TestInputIsReplayedOnlyBetweenEnterAndLeave(t *testing.T) {
	d, _, sharerSide := connectedDesk(t)

	send(t, sharerSide,
		event.Key(keyB, pressed), event.Sync(),
		event.Enter(),
		event.Key(event.KeyA, pressed), event.Sync(),
		event.Relative(event.RelX, 7), event.Relative(event.RelY, -3), event.Sync(),
		event.Relative(event.RelWheel, -1), event.Sync(),
		event.Key(event.KeyA, released), event.Sync(),
		event.Leave(),
		event.Key(keyB, pressed), event.Relative(event.RelX, 100), event.Sync(),
		event.Enter(),
		event.Key(event.ButtonLeft, pressed), event.Sync(),
	)

	d.replay.expect(t,
		event.Key(event.KeyA, pressed), event.Sync(),
		event.Relative(event.RelX, 7), event.Relative(event.RelY, -3), event.Sync(),
		event.Relative(event.RelWheel, -1), event.Sync(),
		event.Key(event.KeyA, released), event.Sync(),
		event.Key(event.ButtonLeft, pressed), event.Sync(),
	)
}

func TestAbsolutePositionsAreReplayedOnlyBetweenEnterAndLeave(t *testing.T) {
	d, _, sharerSide := connectedDesk(t)

	send(t, sharerSide,
		event.Absolute(event.AbsX, 5), event.Absolute(event.AbsY, 6), event.Sync(),
		event.Enter(),
		event.Absolute(event.AbsX, 0), event.Absolute(event.AbsY, event.AbsoluteMax), event.Sync(),
		event.Absolute(event.AbsX, event.AbsoluteMax), event.Key(event.ButtonLeft, pressed), event.Sync(),
		event.Key(event.ButtonLeft, released), event.Sync(),
		event.Leave(),
		event.Absolute(event.AbsX, 7), event.Absolute(event.AbsY, 8), event.Sync(),
		event.Enter(),
		event.Absolute(event.AbsY, 32768), event.Sync(),
	)

	d.replay.expect(t,
		event.Absolute(event.AbsX, 0), event.Absolute(event.AbsY, event.AbsoluteMax), event.Sync(),
		event.Absolute(event.AbsX, event.AbsoluteMax), event.Key(event.ButtonLeft, pressed), event.Sync(),
		event.Key(event.ButtonLeft, released), event.Sync(),
		event.Absolute(event.AbsY, 32768), event.Sync(),
	)
}

func TestLosingFocusAfterAPositionWithoutASyncReleasesWhatIsHeldAndSyncsOnce(t *testing.T) {
	leaves := func(t *testing.T, sharerSide *transport.Conn) { send(t, sharerSide, event.Leave()) }
	isKilled := func(_ *testing.T, sharerSide *transport.Conn) { sharerSide.Close() }
	cases := []struct {
		name      string
		loseFocus func(t *testing.T, sharerSide *transport.Conn)
	}{
		{"leave", leaves},
		{"a killed connection", isKilled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, _, sharerSide := connectedDesk(t)
			dragging := []event.Event{
				event.Key(leftShift, pressed), event.Key(event.ButtonLeft, pressed), event.Sync(),
				event.Absolute(event.AbsX, 100), event.Absolute(event.AbsY, 200),
			}
			send(t, sharerSide, event.Enter())
			send(t, sharerSide, dragging...)
			d.replay.expect(t, dragging...)

			c.loseFocus(t, sharerSide)

			d.replay.expect(t, append(dragging,
				event.Key(leftShift, released), event.Key(event.ButtonLeft, released), event.Sync(),
			)...)
		})
	}
}

func TestLeaveReleasesEveryKeyAndButtonStillHeldAndNothingElse(t *testing.T) {
	d, _, sharerSide := connectedDesk(t)

	send(t, sharerSide,
		event.Enter(),
		event.Key(leftShift, pressed), event.Key(event.KeyA, pressed), event.Key(event.ButtonLeft, pressed), event.Sync(),
		event.Key(event.KeyA, released), event.Sync(),
		event.Leave(),
		event.Enter(),
		event.Key(keyB, pressed), event.Sync(),
	)

	d.replay.expect(t,
		event.Key(leftShift, pressed), event.Key(event.KeyA, pressed), event.Key(event.ButtonLeft, pressed), event.Sync(),
		event.Key(event.KeyA, released), event.Sync(),
		event.Key(leftShift, released), event.Key(event.ButtonLeft, released), event.Sync(),
		event.Key(keyB, pressed), event.Sync(),
	)
}

func TestUnknownControlEventsAreIgnored(t *testing.T) {
	unknown := event.Event{Type: event.TypeControl, Code: 99, Value: 5}
	d, _, sharerSide := connectedDesk(t)

	send(t, sharerSide,
		unknown,
		event.Key(keyB, pressed), event.Sync(),
		event.Enter(),
		unknown,
		event.Key(event.KeyA, pressed), event.Sync(),
		event.Hello(),
		event.Key(event.KeyA, released), event.Sync(),
	)

	d.replay.expect(t,
		event.Key(event.KeyA, pressed), event.Sync(),
		event.Key(event.KeyA, released), event.Sync(),
	)
}

func TestAKilledConnectionReleasesWhatIsHeld(t *testing.T) {
	d, _, sharerSide := connectedDesk(t)
	held := []event.Event{
		event.Key(event.KeyA, pressed), event.Key(event.ButtonLeft, pressed), event.Sync(),
	}
	send(t, sharerSide, event.Enter())
	send(t, sharerSide, held...)
	d.replay.expect(t, held...)

	sharerSide.Close()

	d.replay.expect(t, append(held,
		event.Key(event.KeyA, released), event.Key(event.ButtonLeft, released), event.Sync(),
	)...)
}

func TestTheFramesOfACopyAreNotReplayed(t *testing.T) {
	parts, err := clipboard.Slices("a copy that this receiver does not take")
	if err != nil {
		t.Fatalf("Slices: %v", err)
	}
	d, _, sharerSide := connectedDesk(t)

	send(t, sharerSide, event.Enter(), event.Features(event.FeatureClipboard))
	for _, part := range parts {
		send(t, sharerSide, part...)
	}
	send(t, sharerSide, event.Key(event.KeyA, pressed), event.Sync())

	d.replay.expect(t, event.Key(event.KeyA, pressed), event.Sync())
}

func TestLosingFocusSyncsInputThatWasReplayedWithoutASync(t *testing.T) {
	leaves := func(t *testing.T, sharerSide *transport.Conn) { send(t, sharerSide, event.Leave()) }
	isKilled := func(_ *testing.T, sharerSide *transport.Conn) { sharerSide.Close() }
	tapWithoutSync := []event.Event{event.Key(event.KeyA, pressed), event.Sync(), event.Key(event.KeyA, released)}
	movementWithoutSync := []event.Event{event.Relative(event.RelX, 7)}
	positionWithoutSync := []event.Event{event.Absolute(event.AbsX, 100), event.Absolute(event.AbsY, 200)}
	cases := []struct {
		name      string
		unsynced  []event.Event
		loseFocus func(t *testing.T, sharerSide *transport.Conn)
	}{
		{"a release, then leave", tapWithoutSync, leaves},
		{"a release, then a killed connection", tapWithoutSync, isKilled},
		{"a movement, then leave", movementWithoutSync, leaves},
		{"a movement, then a killed connection", movementWithoutSync, isKilled},
		{"a position, then leave", positionWithoutSync, leaves},
		{"a position, then a killed connection", positionWithoutSync, isKilled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, _, sharerSide := connectedDesk(t)
			send(t, sharerSide, event.Enter())
			send(t, sharerSide, c.unsynced...)
			d.replay.expect(t, c.unsynced...)

			c.loseFocus(t, sharerSide)

			d.replay.expect(t, append(slices.Clone(c.unsynced), event.Sync())...)
		})
	}
}

func TestInputSyncedOnLeaveIsNotSyncedAgainWhenTheConnectionIsLost(t *testing.T) {
	d, _, sharerSide := connectedDesk(t)
	send(t, sharerSide, event.Enter(), event.Relative(event.RelX, 7), event.Leave())
	syncedOnLeave := []event.Event{event.Relative(event.RelX, 7), event.Sync()}
	d.replay.expect(t, syncedOnLeave...)

	sharerSide.Close()
	d.failuresAfter(t, 1)

	if got := d.replay.replayed(); !slices.Equal(got, syncedOnLeave) {
		t.Errorf("replayed %+v, want only %+v", got, syncedOnLeave)
	}
}

func TestTheReceiverReconnectsWhenTheSharerComesBackAndKeepsItsReplay(t *testing.T) {
	d := newDesk(t)
	address := holdAddress(t)
	listener := d.sharerListens(t, loopback)
	address.passTo(t, listener)
	d.receiverRuns(t, address.String())
	sharerSide := accept(t, listener)
	send(t, sharerSide, event.Enter(), event.Key(event.KeyA, pressed), event.Sync())
	d.replay.expect(t, event.Key(event.KeyA, pressed), event.Sync())

	address.refuse()
	listener.Close()
	sharerSide.Close()
	lost := d.failuresAfter(t, 2)
	listener = d.sharerListens(t, loopback)
	address.passTo(t, listener)
	sharerSide = accept(t, listener)
	send(t, sharerSide,
		event.Key(keyB, pressed), event.Sync(),
		event.Enter(),
		event.Key(event.ButtonLeft, pressed), event.Sync(),
	)

	d.replay.expect(t,
		event.Key(event.KeyA, pressed), event.Sync(),
		event.Key(event.KeyA, released), event.Sync(),
		event.Key(event.ButtonLeft, pressed), event.Sync(),
	)
	for i, failed := range lost {
		if failed.reason == nil {
			t.Errorf("failed connection %d was reported without a reason", i)
		}
	}
	if got := d.timesConnected(); got != 2 {
		t.Errorf("the receiver reported %d connections, want 2", got)
	}
	if got := d.replay.timesClosed(); got != 0 {
		t.Errorf("the replay was closed %d times while the receiver runs, want it kept", got)
	}
}

func TestStoppingIsPromptReleasesWhatIsHeldAndClosesTheReplay(t *testing.T) {
	d, _, sharerSide := connectedDesk(t)
	held := []event.Event{event.Key(leftShift, pressed), event.Key(event.ButtonRight, pressed), event.Sync()}
	send(t, sharerSide, event.Enter())
	send(t, sharerSide, held...)
	d.replay.expect(t, held...)

	if err := d.stopsWithin(t, promptly); err != nil {
		t.Errorf("the stopped receiver returned %v, want no error", err)
	}

	d.replay.expect(t, append(held,
		event.Key(leftShift, released), event.Key(event.ButtonRight, released), event.Sync(),
	)...)
	if got := d.replay.timesClosed(); got != 1 {
		t.Errorf("the replay was closed %d times, want once", got)
	}
	if got := d.timesFailed(); got != 0 {
		t.Errorf("stopping was reported as %d failed connections, want none", got)
	}
}

func TestStoppingDoesNotWaitForTheReconnectDelay(t *testing.T) {
	d := newDesk(t)
	config := d.config(holdAddress(t).String())
	d.start(t, func(ctx context.Context) error { return Run(ctx, config) })
	d.failuresAfter(t, 1)

	if err := d.stopsWithin(t, promptly); err != nil {
		t.Errorf("the stopped receiver returned %v, want no error", err)
	}
	if got := d.replay.timesClosed(); got != 1 {
		t.Errorf("the replay was closed %d times, want once", got)
	}
}

func TestAnUnreachableSharerIsRetriedWithPausesThatGrow(t *testing.T) {
	d := newDesk(t)
	d.receiverRuns(t, holdAddress(t).String())

	attempts := d.failuresAfter(t, 5)

	shortestPauses := []time.Duration{
		quickRetries.first, 2 * quickRetries.first, 4 * quickRetries.first, quickRetries.longest,
	}
	for i, shortest := range shortestPauses {
		if paused := attempts[i+1].at.Sub(attempts[i].at); paused < shortest {
			t.Errorf("attempt %d came %s after the one before, want at least %s", i+2, paused, shortest)
		}
	}
	if got := d.replay.replayed(); len(got) != 0 {
		t.Errorf("replayed %+v with no sharer, want nothing", got)
	}
	if got := d.timesConnected(); got != 0 {
		t.Errorf("the receiver reported %d connections with no sharer, want none", got)
	}
}

func TestTheRetryDelayStartsOverAfterAConnection(t *testing.T) {
	growing := retryDelays{first: 10 * time.Millisecond, longest: time.Minute}
	delayWithoutStartingOver := 640 * time.Millisecond
	d := newDesk(t)
	address := holdAddress(t)
	d.receiverRunsRetrying(t, address.String(), growing)
	d.failuresAfter(t, 6)
	listener := d.sharerListens(t, loopback)
	address.passTo(t, listener)

	accept(t, listener).Close()
	lost := time.Now()
	accept(t, listener)

	if waited := time.Since(lost); waited > delayWithoutStartingOver/2 {
		t.Errorf("the receiver came back %s after losing a connection, want about %s", waited, growing.first)
	}
}

func TestTheRetryDelayStartsAtOneSecondAndStopsGrowingAtFive(t *testing.T) {
	if defaultRetryDelays.first != time.Second {
		t.Errorf("the first delay is %s, want 1s", defaultRetryDelays.first)
	}
	cases := []struct{ previous, want time.Duration }{
		{time.Second, 2 * time.Second},
		{2 * time.Second, 4 * time.Second},
		{4 * time.Second, 5 * time.Second},
		{5 * time.Second, 5 * time.Second},
	}
	for _, c := range cases {
		if got := defaultRetryDelays.after(c.previous); got != c.want {
			t.Errorf("the delay after %s is %s, want %s", c.previous, got, c.want)
		}
	}
}

func TestAReplayThatFailsStopsTheReceiverWithItsError(t *testing.T) {
	broken := errors.New("the virtual device is gone")
	d := newDesk(t)
	d.replay.failure = broken
	listener := d.sharerListens(t, loopback)
	d.receiverRuns(t, listener.Addr().String())

	send(t, accept(t, listener), event.Enter(), event.Key(event.KeyA, pressed), event.Sync())

	select {
	case err := <-d.finished:
		d.finished <- err
		if !errors.Is(err, broken) {
			t.Errorf("the receiver returned %v, want the error of the replay", err)
		}
	case <-time.After(testPatience):
		t.Fatal("the receiver kept running with a replay that fails")
	}
	if got := d.replay.timesClosed(); got != 1 {
		t.Errorf("the replay was closed %d times, want once", got)
	}
}
