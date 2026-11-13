package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
	"github.com/rodrigogaraujo/lanhop/internal/wire"
)

const shortDataWindow = 150 * time.Millisecond

var _ io.ReadWriteCloser = (*DataConn)(nil)

type deskWithDataConnections struct {
	t        *testing.T
	central  *identity.Identity
	sides    map[string]*identity.Identity
	listener *Listener
	rejected rejections
}

func newDeskWithDataConnections(t *testing.T, data Config, sideNames ...string) *deskWithDataConnections {
	t.Helper()
	d := &deskWithDataConnections{t: t, central: newIdentity(t), sides: map[string]*identity.Identity{}, rejected: make(rejections, 8)}
	paired := pairedWith{}
	for _, name := range sideNames {
		side := newIdentity(t)
		d.sides[name] = side
		paired[side.Fingerprint] = trust.Peer{Name: name, Fingerprint: side.Fingerprint}
	}
	config := configFor(d.central)
	config.DataWindow = data.DataWindow
	config.DataBytesPerSecond = data.DataBytesPerSecond
	config.OnReject = d.rejected.record
	listener, err := Listen("127.0.0.1:0", config, paired)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	d.listener = listener
	return d
}

func (d *deskWithDataConnections) centralAsSeenFromTheSides() trust.Peer {
	return reach("desk", d.central, d.listener)
}

func (d *deskWithDataConnections) dialData(name string, transfer int32) (*DataConn, error) {
	d.t.Helper()
	conn, err := DialData(context.Background(), configFor(d.sides[name]), d.centralAsSeenFromTheSides(), transfer)
	if err == nil {
		d.t.Cleanup(func() { conn.Close() })
	}
	return conn, err
}

func (d *deskWithDataConnections) mustDialData(name string, transfer int32) *DataConn {
	d.t.Helper()
	conn, err := d.dialData(name, transfer)
	if err != nil {
		d.t.Fatalf("%s could not open the data connection of transfer %d: %v", name, transfer, err)
	}
	return conn
}

func (d *deskWithDataConnections) expectRefused(name string, transfer int32, reason error) {
	d.t.Helper()
	conn, err := d.dialData(name, transfer)
	if err == nil {
		conn.Close()
		d.t.Fatalf("%s opened a data connection with transfer %d, which had to be refused", name, transfer)
	}
	if !errors.Is(err, ErrDataRefused) {
		d.t.Errorf("DialData = %v, want ErrDataRefused", err)
	}
	if got := d.rejected.next(d.t); !errors.Is(got, reason) {
		d.t.Errorf("the central rejected it with %v, want %v", got, reason)
	}
}

type awaitedResult struct {
	conn *DataConn
	err  error
}

type awaiting struct {
	t        *testing.T
	transfer int32
	result   chan awaitedResult
}

func (d *deskWithDataConnections) awaitDataFrom(ctx context.Context, name string) awaiting {
	d.t.Helper()
	numbers := make(chan int32, 1)
	result := make(chan awaitedResult, 1)
	go func() {
		conn, err := d.listener.AwaitData(ctx, d.sides[name].Fingerprint, func(transfer int32) error {
			numbers <- transfer
			return nil
		})
		result <- awaitedResult{conn, err}
	}()
	select {
	case transfer := <-numbers:
		return awaiting{t: d.t, transfer: transfer, result: result}
	case <-time.After(testPatience):
		d.t.Fatal("the central handed out no transfer number")
		return awaiting{}
	}
}

func (a awaiting) arrived() *DataConn {
	a.t.Helper()
	select {
	case got := <-a.result:
		if got.err != nil {
			a.t.Fatalf("AwaitData: %v", got.err)
		}
		a.t.Cleanup(func() { got.conn.Close() })
		return got.conn
	case <-time.After(testPatience):
		a.t.Fatal("the central never got its data connection")
		return nil
	}
}

func (a awaiting) failed() error {
	a.t.Helper()
	select {
	case got := <-a.result:
		if got.err == nil {
			got.conn.Close()
			a.t.Fatal("AwaitData returned a data connection, want an error")
		}
		return got.err
	case <-time.After(testPatience):
		a.t.Fatal("AwaitData never returned")
		return nil
	}
}

func (a awaiting) stillWaiting() {
	a.t.Helper()
	select {
	case got := <-a.result:
		a.result <- got
		a.t.Fatalf("AwaitData returned %v, %v while its machine had not connected", got.conn, got.err)
	default:
	}
}

func anotherNumberThan(transfer int32) int32 {
	if transfer == event.TransferNumberMax {
		return 1
	}
	return transfer + 1
}

func sendAndReceive(t *testing.T, from io.Writer, to io.Reader, content []byte) {
	t.Helper()
	sent := make(chan error, 1)
	go func() {
		_, err := from.Write(content)
		sent <- err
	}()
	received := make([]byte, len(content))
	if _, err := io.ReadFull(to, received); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if err := <-sent; err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !bytes.Equal(received, content) {
		t.Errorf("received %q, want %q", received, content)
	}
}

func TestADataConnectionOpensForTheTransferNumberTheCentralHandedOut(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop")

	awaited := d.awaitDataFrom(context.Background(), "laptop")
	sideData := d.mustDialData("laptop", awaited.transfer)
	centralData := awaited.arrived()

	if centralData.Transfer() != awaited.transfer || sideData.Transfer() != awaited.transfer {
		t.Errorf("transfers = %d and %d, want %d on both sides", centralData.Transfer(), sideData.Transfer(), awaited.transfer)
	}
	if got := centralData.Peer().Name; got != "laptop" {
		t.Errorf("the central sees %q, want laptop", got)
	}
	if got := sideData.Peer().Name; got != "desk" {
		t.Errorf("the side machine sees %q, want desk", got)
	}
	sendAndReceive(t, centralData, sideData, []byte("files from the desk"))
	sendAndReceive(t, sideData, centralData, []byte("files from the laptop"))
}

func TestTransferNumbersAreHandedOutBetween1AndTheLargestNumber(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seen := map[int32]bool{}
	for range 20 {
		awaited := d.awaitDataFrom(ctx, "laptop")
		if awaited.transfer < 1 || awaited.transfer > event.TransferNumberMax {
			t.Errorf("transfer number %d is outside 1 to %d", awaited.transfer, event.TransferNumberMax)
		}
		if seen[awaited.transfer] {
			t.Errorf("transfer number %d was handed out twice", awaited.transfer)
		}
		seen[awaited.transfer] = true
	}
}

func TestEachDataConnectionAwaitedAtTheSameTimeGetsItsOwnNumber(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop")

	first := d.awaitDataFrom(context.Background(), "laptop")
	second := d.awaitDataFrom(context.Background(), "laptop")
	if first.transfer == second.transfer {
		t.Fatalf("both transfers got number %d", first.transfer)
	}
	sideSecond := d.mustDialData("laptop", second.transfer)
	sideFirst := d.mustDialData("laptop", first.transfer)

	sendAndReceive(t, second.arrived(), sideSecond, []byte("the second transfer"))
	sendAndReceive(t, first.arrived(), sideFirst, []byte("the first transfer"))
}

func TestADataConnectionIsRefusedForANumberTheCentralNeverHandedOut(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop")

	awaited := d.awaitDataFrom(context.Background(), "laptop")
	d.expectRefused("laptop", anotherNumberThan(awaited.transfer), ErrDataNotAwaited)
	awaited.stillWaiting()

	d.mustDialData("laptop", awaited.transfer)
	awaited.arrived()
	expectNothingAccepted(t, d.listener)
}

func TestADataConnectionIsRefusedWhenNothingIsAwaited(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: shortDataWindow}, "laptop")

	d.expectRefused("laptop", 12345, ErrDataNotAwaited)
	expectNothingAccepted(t, d.listener)
}

func TestADataConnectionIsRefusedOnceTheWindowHasPassed(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: shortDataWindow}, "laptop")

	started := time.Now()
	awaited := d.awaitDataFrom(context.Background(), "laptop")
	if err := awaited.failed(); !errors.Is(err, ErrDataNotOpened) {
		t.Errorf("AwaitData = %v, want ErrDataNotOpened", err)
	}
	if waited := time.Since(started); waited < shortDataWindow {
		t.Errorf("AwaitData gave up after %s, before the window of %s", waited, shortDataWindow)
	}

	d.expectRefused("laptop", awaited.transfer, ErrDataNotAwaited)
}

func TestADataConnectionIsRefusedFromAMachineTheNumberWasNotHandedTo(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop", "tablet")

	awaited := d.awaitDataFrom(context.Background(), "laptop")
	d.expectRefused("tablet", awaited.transfer, ErrDataFromAnotherMachine)
	awaited.stillWaiting()

	sideData := d.mustDialData("laptop", awaited.transfer)
	centralData := awaited.arrived()
	if got := centralData.Peer().Name; got != "laptop" {
		t.Errorf("the data connection came from %q, want laptop", got)
	}
	sendAndReceive(t, centralData, sideData, []byte("only for the laptop"))
}

func TestADataConnectionIsAcceptedOnlyOnceForANumber(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop")

	awaited := d.awaitDataFrom(context.Background(), "laptop")
	d.mustDialData("laptop", awaited.transfer)
	awaited.arrived()

	d.expectRefused("laptop", awaited.transfer, ErrDataNotAwaited)
}

func TestAMachineThatIsNotPairedCannotOpenADataConnection(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop")
	stranger := newIdentity(t)

	awaited := d.awaitDataFrom(context.Background(), "laptop")
	conn, err := DialData(context.Background(), configFor(stranger), d.centralAsSeenFromTheSides(), awaited.transfer)
	if err == nil {
		conn.Close()
		t.Fatal("a machine that is not paired opened a data connection")
	}
	if got := d.rejected.next(t); !errors.Is(got, ErrUntrusted) {
		t.Errorf("the central rejected it with %v, want ErrUntrusted", got)
	}
	awaited.stillWaiting()
}

func TestAwaitingDataStopsWhenItsContextEndsAndTheNumberIsRefusedAfterwards(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop")
	ctx, cancel := context.WithCancel(context.Background())

	awaited := d.awaitDataFrom(ctx, "laptop")
	cancel()
	if err := awaited.failed(); !errors.Is(err, context.Canceled) {
		t.Errorf("AwaitData = %v, want context.Canceled", err)
	}

	d.expectRefused("laptop", awaited.transfer, ErrDataNotAwaited)
}

func TestAwaitingDataStopsWhenTheListenerCloses(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop")

	awaited := d.awaitDataFrom(context.Background(), "laptop")
	d.listener.Close()

	if err := awaited.failed(); !errors.Is(err, net.ErrClosed) {
		t.Errorf("AwaitData = %v, want net.ErrClosed", err)
	}
}

func TestAwaitingDataGivesBackTheErrorOfTheAnnouncementAndForgetsTheNumber(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop")
	lost := errors.New("the control connection was lost")

	var handedOut int32
	conn, err := d.listener.AwaitData(context.Background(), d.sides["laptop"].Fingerprint, func(transfer int32) error {
		handedOut = transfer
		return lost
	})
	if err == nil {
		conn.Close()
	}
	if !errors.Is(err, lost) {
		t.Fatalf("AwaitData = %v, want the error of the announcement", err)
	}

	d.expectRefused("laptop", handedOut, ErrDataNotAwaited)
}

func TestAControlConnectionWorksNextToDataConnections(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop", "tablet")

	laptopControl := dial(t, d.sides["laptop"], d.centralAsSeenFromTheSides())
	centralControl := accept(t, d.listener)
	awaited := d.awaitDataFrom(context.Background(), "laptop")
	sideData := d.mustDialData("laptop", awaited.transfer)
	centralData := awaited.arrived()

	fileBytes := bytes.Repeat([]byte("file bytes "), 100_000)
	written := make(chan error, 1)
	go func() {
		_, err := centralData.Write(fileBytes)
		written <- err
	}()
	typed := []event.Event{event.Key(event.KeyA, event.KeyPressed), event.Sync()}
	if err := centralControl.Send(typed...); err != nil {
		t.Fatalf("Send: %v", err)
	}
	for i, want := range typed {
		if got := receive(t, laptopControl); got != want {
			t.Errorf("the laptop got event %d = %+v, want %+v while the file bytes waited", i, got, want)
		}
	}
	received := make([]byte, len(fileBytes))
	if _, err := io.ReadFull(sideData, received); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if err := <-written; err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !bytes.Equal(received, fileBytes) {
		t.Error("the file bytes changed on the way")
	}

	tabletControl := dial(t, d.sides["tablet"], d.centralAsSeenFromTheSides())
	if got := accept(t, d.listener).Peer().Name; got != "tablet" {
		t.Errorf("accepted %q, want tablet", got)
	}
	if err := tabletControl.Send(event.Leave()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	expectNothingAccepted(t, d.listener)
}

func TestAHelloOfVersion01StillConnects(t *testing.T) {
	central, side := newIdentity(t), newIdentity(t)
	listener, _ := listen(t, central, pairedWithMachine("laptop", side))
	helloOfVersion1 := []byte{0xFF, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01}

	raw := rawClient(t, side, reach("desk", central, listener))
	if _, err := raw.Write(helloOfVersion1); err != nil {
		t.Fatalf("Write: %v", err)
	}
	answer := make([]byte, len(helloOfVersion1))
	if _, err := io.ReadFull(raw, answer); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}

	if !bytes.Equal(answer, helloOfVersion1) {
		t.Errorf("the central answered % x, want % x", answer, helloOfVersion1)
	}
	if got := accept(t, listener).Peer().Name; got != "laptop" {
		t.Errorf("accepted %q, want laptop", got)
	}
}

func centralOnVersion01(t *testing.T, machine *identity.Identity, trusted Trusted) string {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", acceptOnlyPaired(machine, trusted))
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		if wire.Write(raw, event.Hello()) != nil {
			return
		}
		greeting, err := wire.Read(raw)
		if err != nil || greeting.Code != event.ControlHello {
			return
		}
		io.Copy(io.Discard, raw)
	}()
	return listener.Addr().String()
}

func TestACentralOnVersion01RefusesADataConnection(t *testing.T) {
	central, side := newIdentity(t), newIdentity(t)
	address := centralOnVersion01(t, central, pairedWithMachine("laptop", side))

	conn, err := DialData(context.Background(), configFor(side), trust.Peer{Name: "desk", Fingerprint: central.Fingerprint, Address: address}, 7)
	if err == nil {
		conn.Close()
		t.Fatal("a central on 0.1.x took a data connection")
	}
	if !errors.Is(err, ErrDataRefused) {
		t.Errorf("DialData = %v, want ErrDataRefused", err)
	}
}

func TestFileBytesCannotBeReadOnTheNetwork(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop")
	relay := relayTo(t, d.listener.Addr().String())
	throughRelay := trust.Peer{Name: "desk", Fingerprint: d.central.Fingerprint, Address: relay.listener.Addr().String()}

	awaited := d.awaitDataFrom(context.Background(), "laptop")
	sideData, err := DialData(context.Background(), configFor(d.sides["laptop"]), throughRelay, awaited.transfer)
	if err != nil {
		t.Fatalf("DialData: %v", err)
	}
	defer sideData.Close()
	secret := bytes.Repeat([]byte("the content of a private file. "), 64)
	sendAndReceive(t, awaited.arrived(), sideData, secret)

	captured := relay.recorded()
	if len(captured) == 0 {
		t.Fatal("the relay captured nothing")
	}
	if bytes.Contains(captured, []byte("the content of a private file")) {
		t.Error("the file bytes travelled in clear text")
	}
	if bytes.Contains(captured, wire.Append(nil, event.DataHello(awaited.transfer))) {
		t.Error("the data hello travelled in clear text")
	}
}

func TestADataConnectionWritesNoFasterThanItsPace(t *testing.T) {
	const (
		bytesPerSecond = 2_000_000
		written        = 1_000_000
	)
	d := newDeskWithDataConnections(t, Config{DataBytesPerSecond: bytesPerSecond}, "laptop")

	awaited := d.awaitDataFrom(context.Background(), "laptop")
	sideData := d.mustDialData("laptop", awaited.transfer)
	centralData := awaited.arrived()

	started := time.Now()
	sendAndReceive(t, centralData, sideData, make([]byte, written))
	took := time.Since(started)

	slowest := time.Duration(float64(written-paceBurst) / bytesPerSecond * float64(time.Second))
	if took < slowest {
		t.Errorf("%d bytes took %s at a pace of %d bytes per second, want at least %s", written, took, bytesPerSecond, slowest)
	}
}

func TestDataConnectionsWaitTenSecondsAndArePacedAt80MegabitsUnlessConfigured(t *testing.T) {
	defaults := Config{}.withDefaults()
	if defaults.DataWindow != 10*time.Second {
		t.Errorf("DataWindow = %s, want 10s", defaults.DataWindow)
	}
	if defaults.DataBytesPerSecond != 10_000_000 {
		t.Errorf("DataBytesPerSecond = %d, want 10000000", defaults.DataBytesPerSecond)
	}
}
