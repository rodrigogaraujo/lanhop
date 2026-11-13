package clipshare

import (
	"bytes"
	"context"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/hotkey"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/receiver"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	testPingInterval        = 50 * time.Millisecond
	testSilenceLimit        = time.Second
	testPatience            = 15 * time.Second
	settling                = 300 * time.Millisecond
	twentyMegabitsPerSecond = 20e6 / 8
	thisMachine             = 1
	keyLeftCtrl             = uint16(29)
	keyLeftAlt              = uint16(56)
)

var notInput = event.Event{Type: 0x04, Code: 4, Value: 458756}

func press(code uint16) event.Event   { return event.Key(code, event.KeyPressed) }
func release(code uint16) event.Event { return event.Key(code, event.KeyReleased) }

type timedWrite struct {
	text string
	at   time.Time
}

type fakeClipboard struct {
	mutex      sync.Mutex
	text       string
	refusal    error
	mark       int64
	marksTaken int
	reads      int
	writes     []timedWrite
	readGate   chan struct{}
	markGate   chan struct{}
}

func holding(text string) *fakeClipboard {
	return &fakeClipboard{text: text}
}

func (f *fakeClipboard) Mark() int64 {
	f.mutex.Lock()
	gate := f.markGate
	f.mutex.Unlock()
	if gate != nil {
		<-gate
	}
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.marksTaken++
	return f.mark
}

func (f *fakeClipboard) holdMarksUntilReleased() (release func()) {
	gate := make(chan struct{})
	f.mutex.Lock()
	f.markGate = gate
	f.mutex.Unlock()
	return func() {
		f.mutex.Lock()
		f.markGate = nil
		f.mutex.Unlock()
		close(gate)
	}
}

func (f *fakeClipboard) Read() (string, error) {
	f.mutex.Lock()
	gate := f.readGate
	f.mutex.Unlock()
	if gate != nil {
		<-gate
	}
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.reads++
	if f.refusal != nil {
		return "", f.refusal
	}
	return f.text, nil
}

func (f *fakeClipboard) Write(text string) (int64, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.text, f.refusal = text, nil
	f.mark++
	f.writes = append(f.writes, timedWrite{text: text, at: time.Now()})
	return f.mark, nil
}

func (f *fakeClipboard) personCopies(text string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.text, f.refusal = text, nil
	f.mark++
}

func (f *fakeClipboard) personCopiesWhatLanhopDoesNotCarry(refusal error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.text, f.refusal = "", refusal
	f.mark++
}

func (f *fakeClipboard) holds() string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.text
}

func (f *fakeClipboard) written() []timedWrite {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return slices.Clone(f.writes)
}

func (f *fakeClipboard) timesMarked() int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.marksTaken
}

func (f *fakeClipboard) timesRead() int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.reads
}

func (f *fakeClipboard) holdReadsUntilReleased() (release func()) {
	gate := make(chan struct{})
	f.mutex.Lock()
	f.readGate = gate
	f.mutex.Unlock()
	var releasing sync.Once
	return func() {
		releasing.Do(func() {
			f.mutex.Lock()
			f.readGate = nil
			f.mutex.Unlock()
			close(gate)
		})
	}
}

func opening(board Clipboard) func() (Clipboard, error) {
	return func() (Clipboard, error) { return board, nil }
}

type logBook struct {
	mutex sync.Mutex
	text  bytes.Buffer
}

func (l *logBook) Write(written []byte) (int, error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.text.Write(written)
}

func (l *logBook) String() string {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.text.String()
}

func (l *logBook) waitFor(t *testing.T, wanted string) {
	t.Helper()
	eventually(t, "the log line "+wanted, func() bool { return strings.Contains(l.String(), wanted) })
}

type timedEvent struct {
	event event.Event
	at    time.Time
}

type timedReplay struct {
	mutex   sync.Mutex
	emitted []timedEvent
}

func (r *timedReplay) Emit(events ...event.Event) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	now := time.Now()
	for _, e := range events {
		r.emitted = append(r.emitted, timedEvent{event: e, at: now})
	}
	return nil
}

func (r *timedReplay) Close() error {
	return nil
}

func (r *timedReplay) pressesOf(code uint16) []time.Time {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	var presses []time.Time
	for _, e := range r.emitted {
		if e.event == press(code) {
			presses = append(presses, e.at)
		}
	}
	return presses
}

type fakeCapture struct {
	events chan event.Event
}

func (c *fakeCapture) Events() <-chan event.Event { return c.events }
func (c *fakeCapture) Take() error                { return nil }
func (c *fakeCapture) GiveBack() error            { return nil }

type pairedMachines []trust.Peer

func (p pairedMachines) Peers() []trust.Peer {
	return slices.Clone(p)
}

func (p pairedMachines) Find(fingerprint identity.Fingerprint) (trust.Peer, bool) {
	for _, peer := range p {
		if peer.Fingerprint == fingerprint {
			return peer, true
		}
	}
	return trust.Peer{}, false
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

func eventually(t *testing.T, what string, happened func() bool) {
	t.Helper()
	for deadline := time.Now().Add(testPatience); !happened(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s never happened", what)
		}
	}
}

func stopsWithinPatience(t *testing.T, running *sync.WaitGroup, what string) {
	t.Helper()
	stopped := make(chan struct{})
	go func() {
		running.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(testPatience):
		t.Errorf("%s did not stop", what)
	}
}

type sideUnderTest struct {
	name       string
	number     int
	identity   *identity.Identity
	clipboard  *fakeClipboard
	switchedOn atomic.Bool
	log        *logBook
	replay     *timedReplay
	runtime    *Side
}

type deskUnderTest struct {
	t          *testing.T
	identity   *identity.Identity
	peers      []trust.Peer
	sides      map[string]*sideUnderTest
	clipboard  *fakeClipboard
	switchedOn atomic.Bool
	log        *logBook
	capture    *fakeCapture
	runtime    *Central
	address    string
	mutex      sync.Mutex
	connected  []string
}

func newDesk(t *testing.T, sideNames ...string) *deskUnderTest {
	t.Helper()
	board := holding("what the central held before")
	return newDeskWithAClipboard(t, board, opening(board), sideNames...)
}

func newDeskWithAClipboard(t *testing.T, board *fakeClipboard, open func() (Clipboard, error), sideNames ...string) *deskUnderTest {
	t.Helper()
	d := &deskUnderTest{
		t:         t,
		identity:  newIdentity(t),
		sides:     make(map[string]*sideUnderTest),
		clipboard: board,
		log:       &logBook{},
		capture:   &fakeCapture{events: make(chan event.Event)},
	}
	d.switchedOn.Store(true)
	for i, name := range sideNames {
		side := &sideUnderTest{
			name:      name,
			number:    thisMachine + 1 + i,
			identity:  newIdentity(t),
			clipboard: holding("what " + name + " held before"),
			log:       &logBook{},
			replay:    &timedReplay{},
		}
		side.switchedOn.Store(true)
		d.sides[name] = side
		d.peers = append(d.peers, trust.Peer{Name: name, Fingerprint: side.identity.Fingerprint})
	}
	listener, err := transport.Listen("127.0.0.1:0", transportFor(d.identity), pairedMachines(d.peers))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	d.address = listener.Addr().String()
	detector, err := hotkey.New(hotkey.DefaultModifiers)
	if err != nil {
		t.Fatalf("hotkey.New: %v", err)
	}
	machine := sharer.New(d.capture, pairedMachines(d.peers), detector, sharer.EdgeSwitching{
		Name:  "desk",
		Speed: sharer.DefaultSpeed(),
		Watch: edge.DefaultSettings(),
	})
	machine.OnReceivers(d.noteConnected)
	d.runtime = NewCentral(Config{Open: open, SwitchedOn: d.switchedOn.Load, SilenceLimit: testSilenceLimit, Log: d.log})
	machine.ShareClipboard(d.runtime)

	ctx, stop := context.WithCancel(context.Background())
	var running sync.WaitGroup
	running.Add(3)
	go func() {
		defer running.Done()
		d.runtime.Run(ctx)
	}()
	go func() {
		defer running.Done()
		machine.Run(ctx)
	}()
	go func() {
		defer running.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			machine.Connect(conn)
		}
	}()
	t.Cleanup(func() {
		stop()
		listener.Close()
		stopsWithinPatience(t, &running, "the central")
	})
	return d
}

func (d *deskUnderTest) noteConnected(names []string) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.connected = names
}

func (d *deskUnderTest) waitUntilConnected(name string) {
	d.t.Helper()
	eventually(d.t, name+" connecting to the central", func() bool {
		d.mutex.Lock()
		defer d.mutex.Unlock()
		return slices.Contains(d.connected, name)
	})
}

func (d *deskUnderTest) sideJoins(name string) *sideUnderTest {
	d.t.Helper()
	side := d.sides[name]
	d.startSide(side, d.address, opening(side.clipboard))
	return side
}

func (d *deskUnderTest) sideJoinsOverASlowLink(name string) *sideUnderTest {
	d.t.Helper()
	side := d.sides[name]
	d.startSide(side, throttledProxyTo(d.t, d.address, twentyMegabitsPerSecond), opening(side.clipboard))
	return side
}

func (d *deskUnderTest) startSide(side *sideUnderTest, address string, open func() (Clipboard, error)) {
	d.t.Helper()
	side.runtime = NewSide(Config{Open: open, SwitchedOn: side.switchedOn.Load, SilenceLimit: testSilenceLimit, Log: side.log})
	ctx, stop := context.WithCancel(context.Background())
	var running sync.WaitGroup
	running.Add(2)
	go func() {
		defer running.Done()
		side.runtime.Run(ctx)
	}()
	go func() {
		defer running.Done()
		receiver.Run(ctx, receiver.Config{
			Transport: transportFor(side.identity),
			Sharer:    trust.Peer{Name: "desk", Fingerprint: d.identity.Fingerprint, Address: address},
			Replay:    side.replay,
			Clipboard: side.runtime,
		})
	}()
	d.t.Cleanup(func() {
		stop()
		stopsWithinPatience(d.t, &running, side.name)
	})
	d.waitUntilConnected(side.name)
}

func (d *deskUnderTest) waitUntilTakingPart(sides ...*sideUnderTest) {
	d.t.Helper()
	for _, side := range sides {
		eventually(d.t, side.name+" hearing that the central takes copies", func() bool { return side.clipboard.timesMarked() >= 2 })
		eventually(d.t, "the central seeing "+side.name+" take part", func() bool {
			return slices.Contains(namesTakingPart(d.t, d.runtime), side.name)
		})
	}
	eventually(d.t, "the central taking a fresh mark", func() bool { return d.clipboard.timesMarked() >= 2 })
}

func (d *deskUnderTest) waitUntilTheCentralSeesTakingPart(names ...string) {
	d.t.Helper()
	slices.Sort(names)
	eventually(d.t, "the central seeing "+strings.Join(names, ", ")+" take part", func() bool {
		return slices.Equal(namesTakingPart(d.t, d.runtime), names)
	})
}

func (d *deskUnderTest) waitUntilTheCentralIs(on bool) {
	d.t.Helper()
	eventually(d.t, "the central following its switch", func() bool {
		answer := make(chan bool, 1)
		d.runtime.tell(func() { answer <- d.runtime.on })
		select {
		case got := <-answer:
			return got == on
		case <-time.After(testPatience):
			return false
		}
	})
}

func namesTakingPart(t *testing.T, c *Central) []string {
	t.Helper()
	answer := make(chan []string, 1)
	c.tell(func() {
		var names []string
		for l, machine := range c.sides {
			if machine.takes {
				names = append(names, l.name)
			}
		}
		slices.Sort(names)
		answer <- names
	})
	select {
	case names := <-answer:
		return names
	case <-time.After(testPatience):
		t.Fatal("the central runtime did not answer")
		return nil
	}
}

func (d *deskUnderTest) captures(events ...event.Event) {
	d.t.Helper()
	for _, e := range events {
		select {
		case d.capture.events <- e:
		case <-time.After(testPatience):
			d.t.Fatalf("the sharer did not read %+v", e)
		}
	}
}

func (d *deskUnderTest) focusOn(machine int) (digitPressed time.Time) {
	d.t.Helper()
	digit := uint16(machine) + 1
	d.captures(press(keyLeftCtrl), event.Sync(), press(keyLeftAlt), event.Sync())
	digitPressed = time.Now()
	d.captures(
		press(digit), event.Sync(), release(digit), event.Sync(),
		release(keyLeftAlt), event.Sync(), release(keyLeftCtrl), event.Sync(),
		notInput,
	)
	return digitPressed
}

func (d *deskUnderTest) types() (pressed time.Time) {
	d.t.Helper()
	pressed = time.Now()
	d.captures(press(event.KeyA), event.Sync(), release(event.KeyA), event.Sync(), notInput)
	return pressed
}

func waitUntilItHolds(t *testing.T, name string, board *fakeClipboard, text string) {
	t.Helper()
	eventually(t, name+" holding a copy of "+describe(text), func() bool { return board.holds() == text })
}

func describe(text string) string {
	if len(text) > 40 {
		return strconv.Quote(text[:12]) + "... (" + strconv.Itoa(len(text)) + " bytes)"
	}
	return strconv.Quote(text)
}

func expectUnwritten(t *testing.T, name string, board *fakeClipboard, held string) {
	t.Helper()
	if writes := board.written(); len(writes) > 0 || board.holds() != held {
		t.Errorf("%s was written %d times and holds %s, want it untouched with %s", name, len(writes), describe(board.holds()), describe(held))
	}
}

type oldSide struct {
	conn     *transport.Conn
	mutex    sync.Mutex
	received []event.Event
}

func (d *deskUnderTest) oldSideJoins(name string) *oldSide {
	d.t.Helper()
	conn := d.dialAs(name)
	old := &oldSide{conn: conn}
	go func() {
		for {
			frame, err := conn.Receive()
			if err != nil {
				return
			}
			old.mutex.Lock()
			old.received = append(old.received, frame)
			old.mutex.Unlock()
		}
	}()
	d.waitUntilConnected(name)
	return old
}

func (d *deskUnderTest) dialAs(name string) *transport.Conn {
	d.t.Helper()
	conn, err := transport.Dial(context.Background(), transportFor(d.sides[name].identity), trust.Peer{Name: "desk", Fingerprint: d.identity.Fingerprint, Address: d.address})
	if err != nil {
		d.t.Fatalf("Dial: %v", err)
	}
	d.t.Cleanup(func() { conn.Close() })
	return conn
}

type rawSide struct {
	conn         *transport.Conn
	acknowledges bool
	mutex        sync.Mutex
	copies       []string
	closed       atomic.Bool
}

func (d *deskUnderTest) rawSideTakesCopies(name string, acknowledges bool) *rawSide {
	d.t.Helper()
	raw := &rawSide{conn: d.dialAs(name), acknowledges: acknowledges}
	if err := raw.conn.Send(event.Features(event.FeatureClipboard)); err != nil {
		d.t.Fatalf("Send: %v", err)
	}
	go raw.read()
	d.waitUntilConnected(name)
	return raw
}

func (r *rawSide) read() {
	defer r.closed.Store(true)
	var assembler clipboard.Assembler
	for {
		frame, err := r.conn.Receive()
		if err != nil {
			return
		}
		text, progress, _ := assembler.Take(frame)
		if ack, due := assembler.Ack(); due && r.acknowledges && r.conn.Send(ack) != nil {
			return
		}
		if progress == clipboard.Arrived {
			r.mutex.Lock()
			r.copies = append(r.copies, text)
			r.mutex.Unlock()
		}
	}
}

func (r *rawSide) ended() bool {
	return r.closed.Load()
}

func (r *rawSide) received() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return slices.Clone(r.copies)
}

func (r *rawSide) answers(t *testing.T, frames ...event.Event) {
	t.Helper()
	if err := r.conn.Send(frames...); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func (o *oldSide) frames() []event.Event {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	return slices.Clone(o.received)
}

func throttledProxyTo(t *testing.T, target string, bytesPerSecond float64) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			near, err := listener.Accept()
			if err != nil {
				return
			}
			far, err := net.Dial("tcp", target)
			if err != nil {
				near.Close()
				continue
			}
			go pace(near, far, bytesPerSecond)
			go pace(far, near, bytesPerSecond)
		}
	}()
	return listener.Addr().String()
}

func pace(to, from net.Conn, bytesPerSecond float64) {
	defer to.Close()
	defer from.Close()
	buffer := make([]byte, 4096)
	release := time.Now()
	for {
		n, err := from.Read(buffer)
		if n > 0 {
			release = later(release, time.Now()).Add(time.Duration(float64(n) / bytesPerSecond * float64(time.Second)))
			time.Sleep(time.Until(release))
			if _, err := to.Write(buffer[:n]); err != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func sendCopy(t *testing.T, conn *transport.Conn, text string) {
	t.Helper()
	parts, err := clipboard.Slices(text)
	if err != nil {
		t.Fatalf("Slices: %v", err)
	}
	for _, part := range parts {
		if err := conn.Send(part...); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
}
