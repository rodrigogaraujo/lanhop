package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
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
	config.DataPace = data.DataPace
	config.now = data.now
	config.drawTransferNumber = data.drawTransferNumber
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
}

func TestADataConnectionCarriesFilesTheOtherWayToo(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop")
	awaited := d.awaitDataFrom(context.Background(), "laptop")
	sideData := d.mustDialData("laptop", awaited.transfer)
	sendAndReceive(t, sideData, awaited.arrived(), []byte("files from the laptop"))
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

func timeToMove(t *testing.T, conns map[*DataConn]*DataConn, eachWrites int) time.Duration {
	t.Helper()
	started := time.Now()
	failures := make(chan error, 2*len(conns))
	for writer, reader := range conns {
		go func() {
			_, err := writer.Write(make([]byte, eachWrites))
			failures <- err
		}()
		go func() {
			_, err := io.CopyN(io.Discard, reader, int64(eachWrites))
			failures <- err
		}()
	}
	for range 2 * len(conns) {
		if err := <-failures; err != nil {
			t.Fatalf("moving the bytes failed: %v", err)
		}
	}
	return time.Since(started)
}

func leastTimeAtThePace(bytes, bytesPerSecond int) time.Duration {
	return time.Duration(float64(bytes-paceBurst) / float64(bytesPerSecond) * float64(time.Second))
}

func TestADataConnectionWritesNoFasterThanItsPace(t *testing.T) {
	const (
		bytesPerSecond = 2_000_000
		written        = 1_000_000
	)
	d := newDeskWithDataConnections(t, Config{DataPace: NewPace(bytesPerSecond)}, "laptop")

	awaited := d.awaitDataFrom(context.Background(), "laptop")
	sideData := d.mustDialData("laptop", awaited.transfer)
	took := timeToMove(t, map[*DataConn]*DataConn{awaited.arrived(): sideData}, written)

	if least := leastTimeAtThePace(written, bytesPerSecond); took < least {
		t.Errorf("%d bytes took %s at a pace of %d bytes per second, want at least %s", written, took, bytesPerSecond, least)
	}
}

func TestTwoDataConnectionsOfOneMachineShareItsPace(t *testing.T) {
	const (
		bytesPerSecond = 4_000_000
		eachWrites     = 1_000_000
	)
	d := newDeskWithDataConnections(t, Config{DataPace: NewPace(bytesPerSecond)}, "laptop", "tablet")

	toTheLaptop := d.awaitDataFrom(context.Background(), "laptop")
	laptopData := d.mustDialData("laptop", toTheLaptop.transfer)
	toTheTablet := d.awaitDataFrom(context.Background(), "tablet")
	tabletData := d.mustDialData("tablet", toTheTablet.transfer)
	took := timeToMove(t, map[*DataConn]*DataConn{toTheLaptop.arrived(): laptopData, toTheTablet.arrived(): tabletData}, eachWrites)

	if least := leastTimeAtThePace(2*eachWrites, bytesPerSecond); took < least {
		t.Errorf("two connections wrote %d bytes each in %s, want at least %s for both together at %d bytes per second",
			eachWrites, took, least, bytesPerSecond)
	}
}

func TestEveryDataConnectionWithoutAPaceOfItsOwnSharesThePaceOfThisMachine(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop")

	first := d.awaitDataFrom(context.Background(), "laptop")
	firstOnTheSide := d.mustDialData("laptop", first.transfer)
	second := d.awaitDataFrom(context.Background(), "laptop")
	secondOnTheSide := d.mustDialData("laptop", second.transfer)

	for name, conn := range map[string]*DataConn{
		"the first on the central":       first.arrived(),
		"the second on the central":      second.arrived(),
		"the first on the side machine":  firstOnTheSide,
		"the second on the side machine": secondOnTheSide,
	} {
		if conn.pace != paceOfThisMachine {
			t.Errorf("%s has a pace of its own, want the pace of this machine", name)
		}
	}
}

func TestDataConnectionsWaitTenSecondsAndThisMachineFollowsTheDelayUpTo80MegabitsUnlessConfigured(t *testing.T) {
	defaults := Config{}.withDefaults()
	if defaults.DataWindow != 10*time.Second {
		t.Errorf("DataWindow = %s, want 10s", defaults.DataWindow)
	}
	if defaults.DataPace != paceOfThisMachine {
		t.Error("a Config without a pace does not use the pace of this machine")
	}
	if !paceOfThisMachine.followsTheDelay || paceOfThisMachine.bytesPerSecond != 10_000_000 || paceOfThisMachine.unpaced {
		t.Errorf("this machine follows the delay %t up to %.0f bytes per second, unpaced %t, want it to follow the delay up to 10000000", paceOfThisMachine.followsTheDelay, paceOfThisMachine.bytesPerSecond, paceOfThisMachine.unpaced)
	}
	if zero := NewPace(0); !zero.followsTheDelay || zero.bytesPerSecond != 10_000_000 || zero.unpaced {
		t.Errorf("a pace of 0 follows the delay %t up to %.0f bytes per second, unpaced %t, want it to follow the delay up to the default of 10000000", zero.followsTheDelay, zero.bytesPerSecond, zero.unpaced)
	}
	if fixed := NewPace(2_000_000); fixed.followsTheDelay {
		t.Error("a pace of 2000000 bytes per second follows the delay, want it fixed")
	}
}

func TestADataConnectionGivesUpAtItsDeadlines(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop")
	awaited := d.awaitDataFrom(context.Background(), "laptop")
	sideData := d.mustDialData("laptop", awaited.transfer)
	centralData := awaited.arrived()
	buffer := make([]byte, 1)

	if err := sideData.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := sideData.Read(buffer); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("Read after the read deadline = %v, want os.ErrDeadlineExceeded", err)
	}
	if err := centralData.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	if _, err := centralData.Write(buffer); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("Write after the write deadline = %v, want os.ErrDeadlineExceeded", err)
	}
	if err := sideData.SetDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	if _, err := sideData.Read(buffer); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("Read after the deadline = %v, want os.ErrDeadlineExceeded", err)
	}
}

func TestAwaitingDataAnnouncesNothingWhenItsContextHasEnded(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop")
	ended, cancel := context.WithCancel(context.Background())
	cancel()

	conn, err := d.listener.AwaitData(ended, d.sides["laptop"].Fingerprint, func(transfer int32) error {
		t.Errorf("transfer %d was announced after the context ended", transfer)
		return nil
	})
	if err == nil {
		conn.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("AwaitData = %v, want context.Canceled", err)
	}
}

func TestAwaitingDataAnnouncesNothingOnceTheListenerIsClosed(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience}, "laptop")
	d.listener.Close()

	conn, err := d.listener.AwaitData(context.Background(), d.sides["laptop"].Fingerprint, func(transfer int32) error {
		t.Errorf("transfer %d was announced by a closed listener", transfer)
		return nil
	})
	if err == nil {
		conn.Close()
	}
	if !errors.Is(err, net.ErrClosed) {
		t.Errorf("AwaitData = %v, want net.ErrClosed", err)
	}
}

func (d *deskWithDataConnections) waitUntilTheWindowStarts(transfer int32) {
	d.t.Helper()
	deadline := time.Now().Add(testPatience)
	for {
		d.listener.heldMutex.Lock()
		held, found := d.listener.held[transfer]
		started := found && !held.deadline.IsZero()
		d.listener.heldMutex.Unlock()
		if started {
			return
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("the window of transfer %d never started", transfer)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestADataConnectionThatArrivesAfterTheWindowIsRefusedBeforeTheCentralGivesUp(t *testing.T) {
	var ahead atomic.Int64
	clockThatJumps := func() time.Time { return time.Now().Add(time.Duration(ahead.Load())) }
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience, now: clockThatJumps}, "laptop")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	awaited := d.awaitDataFrom(ctx, "laptop")
	d.waitUntilTheWindowStarts(awaited.transfer)
	ahead.Store(int64(testPatience + time.Second))

	d.expectRefused("laptop", awaited.transfer, ErrDataNotAwaited)
	awaited.stillWaiting()
}

func scriptedNumbers(numbers ...int32) func() (int32, error) {
	var mutex sync.Mutex
	drawn := 0
	return func() (int32, error) {
		mutex.Lock()
		defer mutex.Unlock()
		number := numbers[drawn%len(numbers)]
		drawn++
		return number, nil
	}
}

func TestATransferNumberNamesOneTransferUntilItsDataConnectionCloses(t *testing.T) {
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience, drawTransferNumber: scriptedNumbers(5, 0, 5, 7, 5)}, "laptop", "tablet")

	first := d.awaitDataFrom(context.Background(), "laptop")
	d.mustDialData("laptop", first.transfer)
	open := first.arrived()
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	second := d.awaitDataFrom(secondCtx, "tablet")
	if first.transfer != 5 || second.transfer != 7 {
		t.Fatalf("transfers %d and %d, want 5, then 7 because 0 is never used and 5 is open", first.transfer, second.transfer)
	}

	open.Close()
	cancelSecond()
	second.failed()
	third := d.awaitDataFrom(context.Background(), "tablet")
	if third.transfer != 5 {
		t.Errorf("after its data connection closed, transfer number 5 was skipped for %d", third.transfer)
	}
	d.mustDialData("tablet", third.transfer)
	third.arrived()
}

func connectedForData(t *testing.T) (writer, reader *DataConn) {
	t.Helper()
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience, DataPace: NewPace(Unpaced)}, "laptop")
	awaited := d.awaitDataFrom(context.Background(), "laptop")
	reader = d.mustDialData("laptop", awaited.transfer)
	return awaited.arrived(), reader
}

func writeInTheBackground(t *testing.T, writer *DataConn, size int) <-chan error {
	t.Helper()
	written := make(chan error, 1)
	go func() {
		_, err := writer.Write(make([]byte, size))
		written <- err
	}()
	return written
}

func readExactly(t *testing.T, reader *DataConn, size int) {
	t.Helper()
	if _, err := io.ReadFull(reader, make([]byte, size)); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
}

func eventuallyAcknowledged(t *testing.T, writer *DataConn, blocks int32) {
	t.Helper()
	for deadline := time.Now().Add(testPatience); writer.acknowledged.Load() != blocks; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the writer saw %d blocks acknowledged, want %d", writer.acknowledged.Load(), blocks)
		}
	}
}

func TestTheReaderAcknowledgesEvery32KiBItReads(t *testing.T) {
	writer, reader := connectedForData(t)
	written := writeInTheBackground(t, writer, 100<<10)
	readExactly(t, reader, 100<<10)
	if err := <-written; err != nil {
		t.Fatalf("Write: %v", err)
	}
	eventuallyAcknowledged(t, writer, 3)
}

func TestTheWriterLearnsThatEveryFileArrived(t *testing.T) {
	writer, reader := connectedForData(t)
	written := writeInTheBackground(t, writer, 64<<10)
	readExactly(t, reader, 64<<10)
	if err := <-written; err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := reader.TellAllArrived(); err != nil {
		t.Fatalf("TellAllArrived: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testPatience)
	defer cancel()
	if err := writer.AwaitAllArrived(ctx); err != nil {
		t.Fatalf("AwaitAllArrived = %v, want nil", err)
	}
	if got := writer.acknowledged.Load(); got != 2 {
		t.Errorf("the writer saw %d blocks acknowledged, want 2", got)
	}
}

func TestNoAcknowledgementFollowsTheLastAnswer(t *testing.T) {
	writer, reader := connectedForData(t)
	written := writeInTheBackground(t, writer, 96<<10)
	readExactly(t, reader, 32<<10)
	eventuallyAcknowledged(t, writer, 1)
	if err := reader.TellAllArrived(); err != nil {
		t.Fatalf("TellAllArrived: %v", err)
	}
	readExactly(t, reader, 64<<10)
	if err := <-written; err != nil {
		t.Fatalf("Write: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testPatience)
	defer cancel()
	if err := writer.AwaitAllArrived(ctx); err != nil {
		t.Fatalf("AwaitAllArrived = %v, want nil", err)
	}
	reader.acking.Lock()
	sent := reader.blocksRead
	reader.acking.Unlock()
	if sent != 1 {
		t.Errorf("the reader acknowledged %d blocks, want 1: nothing after the last answer", sent)
	}
}

func TestAReadDeadlineSetBeforeTheFirstWriteDoesNotStopTheAcknowledgements(t *testing.T) {
	writer, reader := connectedForData(t)
	if err := writer.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	written := writeInTheBackground(t, writer, 32<<10)
	readExactly(t, reader, 32<<10)
	if err := <-written; err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := reader.TellAllArrived(); err != nil {
		t.Fatalf("TellAllArrived: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testPatience)
	defer cancel()
	if err := writer.AwaitAllArrived(ctx); err != nil {
		t.Fatalf("AwaitAllArrived after a read deadline set before writing = %v, want nil", err)
	}
}

func TestAWriterStopsWhenSomethingElseThanAnAcknowledgementComesBack(t *testing.T) {
	writer, reader := connectedForData(t)
	written := writeInTheBackground(t, writer, 8<<10)
	readExactly(t, reader, 8<<10)
	if err := <-written; err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := wire.Write(reader.secured, event.Hello()); err != nil {
		t.Fatalf("writing a stray frame: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testPatience)
	defer cancel()
	if err := writer.AwaitAllArrived(ctx); !errors.Is(err, ErrDataNotAnAck) {
		t.Fatalf("AwaitAllArrived after a stray frame = %v, want ErrDataNotAnAck", err)
	}
	if _, err := writer.Write(make([]byte, 1)); err == nil {
		t.Errorf("Write after a stray frame came back succeeded, want the closed connection to refuse it")
	}
}

func TestAConnectionThatEndsWithoutTheAnswerIsNotAnArrival(t *testing.T) {
	writer, reader := connectedForData(t)
	written := writeInTheBackground(t, writer, 8<<10)
	readExactly(t, reader, 8<<10)
	if err := <-written; err != nil {
		t.Fatalf("Write: %v", err)
	}
	reader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testPatience)
	defer cancel()
	if err := writer.AwaitAllArrived(ctx); !errors.Is(err, ErrDataEndedEarly) {
		t.Fatalf("AwaitAllArrived after the reader closed = %v, want ErrDataEndedEarly", err)
	}
}

func TestAwaitingTheAnswerStopsWithItsContext(t *testing.T) {
	writer, _ := connectedForData(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := writer.AwaitAllArrived(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AwaitAllArrived with a short context = %v, want context.DeadlineExceeded", err)
	}
}

func TestADeadlineOnTheWriterDoesNotStopItsAcknowledgements(t *testing.T) {
	writer, reader := connectedForData(t)
	written := writeInTheBackground(t, writer, 32<<10)
	readExactly(t, reader, 32<<10)
	if err := <-written; err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.SetDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	if err := writer.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if err := reader.TellAllArrived(); err != nil {
		t.Fatalf("TellAllArrived: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testPatience)
	defer cancel()
	if err := writer.AwaitAllArrived(ctx); err != nil {
		t.Fatalf("AwaitAllArrived after a deadline on the writer = %v, want nil", err)
	}
}

func TestADataConnectionUsedBothWaysFailsLoudly(t *testing.T) {
	writer, reader := connectedForData(t)
	written := writeInTheBackground(t, writer, 1)
	readExactly(t, reader, 1)
	if err := <-written; err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := writer.Read(make([]byte, 1)); !errors.Is(err, ErrDataUsedBothWays) {
		t.Errorf("Read on the writing side = %v, want ErrDataUsedBothWays", err)
	}
	if _, err := reader.Write([]byte{1}); !errors.Is(err, ErrDataUsedBothWays) {
		t.Errorf("Write on the reading side = %v, want ErrDataUsedBothWays", err)
	}
	if err := writer.TellAllArrived(); !errors.Is(err, ErrDataUsedBothWays) {
		t.Errorf("TellAllArrived on the writing side = %v, want ErrDataUsedBothWays", err)
	}
	if err := reader.AwaitAllArrived(context.Background()); !errors.Is(err, ErrDataUsedBothWays) {
		t.Errorf("AwaitAllArrived on the reading side = %v, want ErrDataUsedBothWays", err)
	}
}

func TestTheValueOfAnAcknowledgementCarriesTheBlocksAndTheLastAnswer(t *testing.T) {
	if blocks, all := event.SplitDataAck(event.DataAck(1234).Value); blocks != 1234 || all {
		t.Errorf("DataAck(1234) reads as %d, %t, want 1234, false", blocks, all)
	}
	if blocks, all := event.SplitDataAck(event.AllArrived(7).Value); blocks != 7 || !all {
		t.Errorf("AllArrived(7) reads as %d, %t, want 7, true", blocks, all)
	}
	pastTheLargest := event.TransferNumberMax
	pastTheLargest++
	if blocks, all := event.SplitDataAck(event.DataAck(pastTheLargest).Value); blocks != 0 || all {
		t.Errorf("a count past the largest reads as %d, %t, want 0 and not the last answer", blocks, all)
	}
}
