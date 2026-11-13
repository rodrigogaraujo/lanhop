package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

const (
	packetPayloadBytes     = 1460
	headerBytesPerPacket   = 78
	linkCatchUp            = time.Millisecond
	sc002TransferBytes     = 100 << 20
	unpacedTransferBytes   = 20 << 20
	keyLateByAtMost        = 50 * time.Millisecond
	betweenKeys            = 10 * time.Millisecond
	keysWithoutTheTransfer = 30
	keysDuringTheTransfer  = 100
	transferPatience       = time.Minute
)

type shapedLink struct {
	bytesPerSecond float64
	packetsAtMost  int
}

var (
	routerOf100Megabits = shapedLink{bytesPerSecond: 100_000_000 / 8, packetsAtMost: 1000}
	routerOf50Megabits  = shapedLink{bytesPerSecond: 50_000_000 / 8, packetsAtMost: 1000}
)

const (
	sc001TransferBytes = 10 << 20
	sc001AtMost        = 1300 * time.Millisecond
)

func (l shapedLink) timeToSend(payload int) time.Duration {
	return time.Duration(float64(payload+headerBytesPerPacket) / l.bytesPerSecond * float64(time.Second))
}

func (l shapedLink) payloadBytesPerSecond() float64 {
	return l.bytesPerSecond * packetPayloadBytes / (packetPayloadBytes + headerBytesPerPacket)
}

func (l shapedLink) timeToDrainAFullQueue() time.Duration {
	return time.Duration(l.packetsAtMost) * l.timeToSend(packetPayloadBytes)
}

func measuredWithoutTheRaceDetector(t *testing.T) {
	t.Helper()
	if raceDetectorOn {
		t.Skip("the race detector slows every byte down, so the timings of SC-002 are measured without it")
	}
}

type packet struct {
	payload []byte
	to      net.Conn
	last    bool
}

type sharedQueue struct {
	link    shapedLink
	packets chan packet
	done    <-chan struct{}
}

func newSharedQueue(link shapedLink, done <-chan struct{}) *sharedQueue {
	q := &sharedQueue{link: link, packets: make(chan packet, link.packetsAtMost), done: done}
	go q.transmit()
	return q
}

func (q *sharedQueue) enqueue(p packet) bool {
	select {
	case q.packets <- p:
		return true
	case <-q.done:
		return false
	}
}

func (q *sharedQueue) carry(to, from net.Conn) {
	for {
		buffer := make([]byte, packetPayloadBytes)
		n, err := from.Read(buffer)
		if n > 0 && !q.enqueue(packet{payload: buffer[:n], to: to}) {
			return
		}
		if err != nil {
			q.enqueue(packet{to: to, last: true})
			return
		}
	}
}

func (q *sharedQueue) transmit() {
	var free time.Time
	for {
		var p packet
		select {
		case p = <-q.packets:
		case <-q.done:
			return
		}
		if p.last {
			p.to.Close()
			continue
		}
		free = later(free, time.Now().Add(-linkCatchUp)).Add(q.link.timeToSend(len(p.payload)))
		if wait := time.Until(free); wait > 0 {
			time.Sleep(wait)
		}
		p.to.Write(p.payload)
	}
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func linkThroughOneQueue(t *testing.T, target string, link shapedLink) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	done := make(chan struct{})
	toTheCentral := newSharedQueue(link, done)
	toTheSide := newSharedQueue(link, done)
	var open sync.Map
	t.Cleanup(func() {
		close(done)
		listener.Close()
		open.Range(func(conn, _ any) bool {
			conn.(net.Conn).Close()
			return true
		})
	})
	go func() {
		for {
			side, err := listener.Accept()
			if err != nil {
				return
			}
			central, err := net.Dial("tcp", target)
			if err != nil {
				side.Close()
				continue
			}
			open.Store(side, nil)
			open.Store(central, nil)
			go toTheCentral.carry(central, side)
			go toTheSide.carry(side, central)
		}
	}()
	return listener.Addr().String()
}

type sideAtTheEndOfTheLink struct {
	mutex   sync.Mutex
	presses []time.Time
	opened  chan *DataConn
	failed  chan error
}

func (s *sideAtTheEndOfTheLink) follow(control *Conn, config Config, central trust.Peer) {
	for {
		received, err := control.Receive()
		if err != nil {
			return
		}
		switch {
		case received.IsControl() && received.Code == event.ControlOpenData:
			transfer, _ := event.SplitOpenData(received.Value)
			go func() {
				data, err := DialData(context.Background(), config, central, transfer)
				if err != nil {
					s.failed <- err
					return
				}
				s.opened <- data
			}()
		case received.Type == event.TypeKey && received.Value == event.KeyPressed:
			s.mutex.Lock()
			s.presses = append(s.presses, time.Now())
			s.mutex.Unlock()
		}
	}
}

func (s *sideAtTheEndOfTheLink) pressesSoFar() []time.Time {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return slices.Clone(s.presses)
}

func (s *sideAtTheEndOfTheLink) delaysOf(t *testing.T, typed []time.Time, pressedBefore int) []time.Duration {
	t.Helper()
	deadline := time.Now().Add(testPatience)
	for len(s.pressesSoFar()) < pressedBefore+len(typed) {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d keys reached the side machine", len(s.pressesSoFar())-pressedBefore, len(typed))
		}
		time.Sleep(betweenKeys)
	}
	presses := s.pressesSoFar()
	delays := make([]time.Duration, len(typed))
	for i, at := range typed {
		delays[i] = presses[pressedBefore+i].Sub(at)
	}
	return delays
}

func typeKey(t *testing.T, control *Conn) time.Time {
	t.Helper()
	at := time.Now()
	if err := control.Send(event.Key(event.KeyA, event.KeyPressed), event.Sync()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	return at
}

func median(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	sorted := slices.Clone(durations)
	slices.Sort(sorted)
	return sorted[len(sorted)/2]
}

func streamZeroes(to io.Writer, total int) error {
	chunk := make([]byte, 64<<10)
	for sent := 0; sent < total; sent += len(chunk) {
		if _, err := to.Write(chunk[:min(len(chunk), total-sent)]); err != nil {
			return err
		}
	}
	return nil
}

type keysBehindATransfer struct {
	usual      time.Duration
	during     []time.Duration
	transfer   time.Duration
	bytesMoved int64
}

func (k keysBehindATransfer) mustHaveTypedEnough(t *testing.T) {
	t.Helper()
	if len(k.during) < keysDuringTheTransfer {
		t.Fatalf("only %d keys were typed while the transfer moved, want at least %d", len(k.during), keysDuringTheTransfer)
	}
}

func (k keysBehindATransfer) worstLaterThanUsual() time.Duration {
	if len(k.during) == 0 {
		return 0
	}
	return slices.Max(k.during) - k.usual
}

func (k keysBehindATransfer) payloadBytesPerSecond() float64 {
	return float64(k.bytesMoved) / k.transfer.Seconds()
}

func (k keysBehindATransfer) String() string {
	return fmt.Sprintf("usual key delay %s; while %d MiB moved in %s (%.1f Mbit/s), %d keys, median %s, worst %s later than usual",
		k.usual, k.bytesMoved>>20, k.transfer.Round(time.Millisecond), megabits(k.payloadBytesPerSecond()),
		len(k.during), median(k.during), k.worstLaterThanUsual())
}

func megabits(bytesPerSecond float64) float64 {
	return bytesPerSecond * 8 / 1e6
}

func timeKeysBehindATransfer(t *testing.T, centralConfig Config, link shapedLink, transferBytes int) keysBehindATransfer {
	t.Helper()
	central, side := newIdentity(t), newIdentity(t)
	centralConfig.Identity = central
	listener, err := Listen("127.0.0.1:0", centralConfig, pairedWithMachine("laptop", side))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	centralBehindTheLink := trust.Peer{Name: "desk", Fingerprint: central.Fingerprint, Address: linkThroughOneQueue(t, listener.Addr().String(), link)}
	sideConfig := Config{Identity: side}

	sideControl, err := Dial(context.Background(), sideConfig, centralBehindTheLink)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { sideControl.Close() })
	centralControl := accept(t, listener)
	sideMachine := &sideAtTheEndOfTheLink{opened: make(chan *DataConn, 1), failed: make(chan error, 1)}
	go sideMachine.follow(sideControl, sideConfig, centralBehindTheLink)

	var typedAlone []time.Time
	for range keysWithoutTheTransfer {
		typedAlone = append(typedAlone, typeKey(t, centralControl))
		time.Sleep(betweenKeys)
	}
	usual := median(sideMachine.delaysOf(t, typedAlone, 0))

	centralData, err := listener.AwaitData(context.Background(), side.Fingerprint, func(transfer int32) error {
		return centralControl.Send(event.OpenData(transfer, false))
	})
	if err != nil {
		t.Fatalf("AwaitData: %v", err)
	}
	t.Cleanup(func() { centralData.Close() })
	var sideData *DataConn
	select {
	case sideData = <-sideMachine.opened:
		t.Cleanup(func() { sideData.Close() })
	case err := <-sideMachine.failed:
		t.Fatalf("DialData: %v", err)
	}

	started := time.Now()
	streamed := make(chan error, 1)
	go func() { streamed <- streamZeroes(centralData, transferBytes) }()
	type arrival struct {
		bytes int64
		at    time.Time
		err   error
	}
	arrived := make(chan arrival, 1)
	go func() {
		moved, err := io.CopyN(io.Discard, sideData, int64(transferBytes))
		arrived <- arrival{moved, time.Now(), err}
	}()

	var typedDuring []time.Time
	var whole arrival
	for waiting := true; waiting; {
		select {
		case whole = <-arrived:
			waiting = false
		default:
			if time.Since(started) > transferPatience {
				t.Fatalf("%d bytes did not arrive within %s", transferBytes, transferPatience)
			}
			typedDuring = append(typedDuring, typeKey(t, centralControl))
			time.Sleep(betweenKeys)
		}
	}
	if whole.err != nil {
		t.Fatalf("the side machine read %d bytes, then: %v", whole.bytes, whole.err)
	}
	if err := <-streamed; err != nil {
		t.Fatalf("the central wrote the transfer with an error: %v", err)
	}
	delays := sideMachine.delaysOf(t, typedDuring, keysWithoutTheTransfer)
	return keysBehindATransfer{usual: usual, during: delays, transfer: whole.at.Sub(started), bytesMoved: whole.bytes}
}

func TestKeysArriveAtMost50MillisecondsLaterWhile100MiBMoveThroughOneQueueOf100Megabits(t *testing.T) {
	measuredWithoutTheRaceDetector(t)

	measured := timeKeysBehindATransfer(t, Config{DataPace: newPaceThatFollowsTheDelay(defaultDataBytesPerSecond)}, routerOf100Megabits, sc002TransferBytes)

	measured.mustHaveTypedEnough(t)
	t.Logf("SC-002, paced by the delay, 100 Mbit/s: %s", measured)
	if late := measured.worstLaterThanUsual(); late > keyLateByAtMost {
		t.Errorf("a key arrived %s later than usual while the transfer moved, want at most %s", late, keyLateByAtMost)
	}
	if moved := measured.payloadBytesPerSecond(); moved < 0.9*defaultDataBytesPerSecond {
		t.Errorf("the transfer moved at %.1f Mbit/s, want at least 90%% of the pace of %.0f Mbit/s", megabits(moved), megabits(defaultDataBytesPerSecond))
	}
}

func TestKeysArriveMoreThan50MillisecondsLateBehindAnUnpacedTransferThroughTheSameQueue(t *testing.T) {
	measuredWithoutTheRaceDetector(t)

	measured := timeKeysBehindATransfer(t, Config{DataPace: NewPace(Unpaced)}, routerOf100Megabits, unpacedTransferBytes)

	carried := routerOf100Megabits.payloadBytesPerSecond()
	t.Logf("SC-002, unpaced: %s; the link carries %.1f Mbit/s and drains a full queue in %s",
		measured, megabits(carried), routerOf100Megabits.timeToDrainAFullQueue())
	if moved := measured.payloadBytesPerSecond(); moved > 1.01*carried || moved < 0.9*carried {
		t.Fatalf("an unpaced transfer moved at %.1f Mbit/s, want the %.1f Mbit/s the link carries", megabits(moved), megabits(carried))
	}
	measured.mustHaveTypedEnough(t)
	if late := measured.worstLaterThanUsual(); late <= keyLateByAtMost {
		t.Errorf("behind an unpaced transfer the worst key was only %s late, want more than %s: the queue does not hold keys back, so the paced test proves nothing",
			late, keyLateByAtMost)
	}
}

func TestKeysArriveAtMost50MillisecondsLaterWhile100MiBMoveThroughOneQueueOf50Megabits(t *testing.T) {
	measuredWithoutTheRaceDetector(t)

	measured := timeKeysBehindATransfer(t, Config{DataPace: newPaceThatFollowsTheDelay(defaultDataBytesPerSecond)}, routerOf50Megabits, sc002TransferBytes)

	measured.mustHaveTypedEnough(t)
	carried := routerOf50Megabits.payloadBytesPerSecond()
	t.Logf("SC-002, paced by the delay, 50 Mbit/s: %s; the link carries %.1f Mbit/s", measured, megabits(carried))
	if late := measured.worstLaterThanUsual(); late > keyLateByAtMost {
		t.Errorf("a key arrived %s later than usual while the transfer moved, want at most %s", late, keyLateByAtMost)
	}
	if moved := measured.payloadBytesPerSecond(); moved < 0.6*carried {
		t.Errorf("the transfer moved at %.1f Mbit/s, want at least 60%% of the %.1f Mbit/s the link carries", megabits(moved), megabits(carried))
	}
}

func TestTheFirst10MiBArriveWellWithin2SecondsBehindAQueueOf100Megabits(t *testing.T) {
	measuredWithoutTheRaceDetector(t)

	measured := timeKeysBehindATransfer(t, Config{DataPace: newPaceThatFollowsTheDelay(defaultDataBytesPerSecond)}, routerOf100Megabits, sc001TransferBytes)

	t.Logf("SC-001, behind a queue of 100 Mbit/s: 10 MiB in %s", measured.transfer)
	if measured.transfer > sc001AtMost {
		t.Errorf("10 MiB took %s behind a queue of 100 Mbit/s, want at most %s", measured.transfer, sc001AtMost)
	}
}

func TestTheFirst10MiBArriveWellWithin2SecondsWithoutAQueue(t *testing.T) {
	measuredWithoutTheRaceDetector(t)
	d := newDeskWithDataConnections(t, Config{DataWindow: testPatience, DataPace: newPaceThatFollowsTheDelay(defaultDataBytesPerSecond)}, "laptop")
	awaited := d.awaitDataFrom(context.Background(), "laptop")
	sideData := d.mustDialData("laptop", awaited.transfer)
	centralData := awaited.arrived()

	started := time.Now()
	sendAndReceive(t, centralData, sideData, make([]byte, sc001TransferBytes))
	took := time.Since(started)

	t.Logf("SC-001, without a queue: 10 MiB in %s", took)
	if took > sc001AtMost {
		t.Errorf("10 MiB took %s without a queue, want at most %s", took, sc001AtMost)
	}
}
