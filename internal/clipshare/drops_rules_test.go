package clipshare

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/filestream"
	"github.com/rodrigogaraujo/lanhop/internal/identity"
	"github.com/rodrigogaraujo/lanhop/internal/receiver"
	"github.com/rodrigogaraujo/lanhop/internal/transport"
	"github.com/rodrigogaraujo/lanhop/internal/trust"
)

type rawDragSide struct {
	*frameRecorder
	conn *transport.Conn
}

func (d *deskUnderTest) rawSideAnnouncing(name string, announced int32) *rawDragSide {
	d.t.Helper()
	raw := &rawDragSide{frameRecorder: &frameRecorder{}, conn: d.dialAs(name)}
	if err := raw.conn.Send(event.Features(announced)); err != nil {
		d.t.Fatalf("Send: %v", err)
	}
	go raw.record(raw.conn)
	d.waitUntilConnected(name)
	eventually(d.t, "the central hearing what "+name+" takes", func() bool {
		return askCentral(d.t, d.runtime, func() bool {
			machine := d.machineNamed(name)
			return machine != nil && machine.takes == clipboard.KindsIn(announced) && machine.drops == clipboard.DropsIn(announced)
		})
	})
	return raw
}

func TestTheCentralTellsWhichSideMachineDragsHowManyFilesAndWhereItsDockIs(t *testing.T) {
	d := newDropDesk(t, "left")
	left := d.sideJoins("left")
	d.waitUntilTakingDrops(left)
	source := t.TempDir()
	first := writeFile(t, filepath.Join(source, "a.txt"), []byte("a"))
	second := writeFile(t, filepath.Join(source, "b.txt"), []byte("b"))

	d.focusMovesTo(left)
	d.sideDrags(left, first, second)
	starts, ends := d.described.seen()
	if want := []SideDrag{{Origin: "left", Files: 2, Dock: edge.Bottom}}; !slices.Equal(starts, want) || len(ends) != 0 {
		t.Errorf("the central told the drags %+v and their ends %q, want %+v and no end", starts, ends, want)
	}

	left.runtime.DragGone()
	eventually(t, "the central telling that the drag on the left ended", func() bool {
		_, ends := d.described.seen()
		return slices.Equal(ends, []string{"left"})
	})
}

func TestACancelAskedBeforeTheDropDataIsOpenedFollowsTheOpening(t *testing.T) {
	hold := make(chan struct{})
	board := holding("what the central held before")
	d := newDeskWith(t, deskSetup{board: board, open: opening(takingFiles{board}), files: true, drops: true, holdData: hold}, "raw")
	raw := d.rawSideAnnouncing("raw", textFilesAndDrags)
	file := writeFile(t, filepath.Join(t.TempDir(), "held.txt"), []byte("held"))

	d.centralDrags(file)
	if err := d.crosses(onTheCentral, d.sides["raw"]); err != nil {
		t.Fatalf("DragCrossed: %v", err)
	}
	d.runtime.DragCancelled()
	askCentral(t, d.runtime, func() bool { return true })
	time.Sleep(settling)
	if opened := raw.withCode(event.ControlOpenDropData); len(opened) > 0 {
		t.Fatalf("the central opened the drop data %+v while the data was held", opened)
	}
	close(hold)

	eventually(t, "the raw side machine getting the drop data and its cancel", func() bool {
		return len(raw.withCode(event.ControlOpenDropData)) > 0 && len(raw.withCode(event.ControlCancelDrop)) > 0
	})
	opened := raw.withCode(event.ControlOpenDropData)[0]
	wire, sideSends := event.SplitOpenData(opened.Value)
	if sideSends {
		t.Errorf("the central asked the side machine it drops on to send")
	}
	frames := raw.all()
	cancel := event.CancelDrop(wire)
	if at, cancelAt := slices.Index(frames, opened), slices.Index(frames, cancel); cancelAt < at || cancelAt < 0 {
		t.Errorf("the cancel %+v came at %d and the opening %+v at %d, want the cancel after the opening", cancel, cancelAt, opened, at)
	}
	if paste := raw.withCode(event.ControlOpenData); len(paste) > 0 {
		t.Errorf("the central opened data for a paste %+v", paste)
	}
}

func TestTheCentralOpensNoDropDataTowardASideMachineWithoutTheDragBit(t *testing.T) {
	d := newDropDesk(t, "raw")
	raw := d.rawSideAnnouncing("raw", textAndFiles)
	rawSide := d.sides["raw"]
	file := writeFile(t, filepath.Join(t.TempDir(), "kept.txt"), []byte("kept"))

	if err := raw.conn.Send(event.DragFiles(1, int32(edge.Left)), event.DragFilesSize(1)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	time.Sleep(settling)
	if d.seesADragOn(rawSide) {
		t.Error("the central took the drag of a side machine without the drag bit")
	}
	if starts, _ := d.described.seen(); len(starts) > 0 {
		t.Errorf("the central told the drag %+v of a side machine without the drag bit", starts)
	}
	if err := d.crosses(rawSide, onTheCentral); !errors.Is(err, ErrNoFileDrag) {
		t.Errorf("a drag from a side machine without the drag bit = %v, want ErrNoFileDrag", err)
	}
	d.centralDrags(file)
	if err := d.crosses(onTheCentral, rawSide); !errors.Is(err, clipboard.ErrDropsNotTaken) {
		t.Errorf("a drag toward a side machine without the drag bit = %v, want ErrDropsNotTaken", err)
	}
	d.runtime.DragCancelled()

	time.Sleep(settling)
	for _, code := range []uint16{event.ControlOpenDropData, event.ControlCancelDrop, event.ControlOpenData} {
		if sent := raw.withCode(code); len(sent) > 0 {
			t.Errorf("a side machine without the drag bit got %+v", sent)
		}
	}
	expectNoDropStarted(t, "the central", d.drops)
}

type rawCentral struct {
	*frameRecorder
	t        *testing.T
	ctx      context.Context
	listener *transport.Listener
	conn     *transport.Conn
	side     identity.Fingerprint
	runtime  *Side
	drops    *fakeDrops
	log      *logBook
}

func newRawCentralWithASide(t *testing.T) *rawCentral {
	t.Helper()
	central, side := newIdentity(t), newIdentity(t)
	listener, err := transport.Listen("127.0.0.1:0", transportFor(central), pairedMachines{{Name: "laptop", Fingerprint: side.Fingerprint}})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	_, arrivals := openArrivals(t, 0)
	board := holding("a copy on the side machine")
	centralPeer := trust.Peer{Name: "desk", Fingerprint: central.Fingerprint, Address: listener.Addr().String()}
	dialing := transportFor(side)
	dialing.DataPace = transport.NewPace(0)
	drops := &fakeDrops{}
	log := &logBook{}
	runtime := NewSide(Config{
		Open:     opening(takingFiles{board}),
		FilesOn:  func() bool { return true },
		Arrivals: arrivals,
		DialData: func(ctx context.Context, transfer int32) (*transport.DataConn, error) {
			return transport.DialData(ctx, dialing, centralPeer, transfer)
		},
		SilenceLimit: testSilenceLimit,
		Log:          log,
		Drops:        drops,
	})
	ctx, stop := context.WithCancel(context.Background())
	var running sync.WaitGroup
	running.Add(2)
	go func() {
		defer running.Done()
		runtime.Run(ctx)
	}()
	go func() {
		defer running.Done()
		receiver.Run(ctx, receiver.Config{Transport: transportFor(side), Sharer: centralPeer, Replay: &timedReplay{}, Clipboard: runtime})
	}()
	t.Cleanup(func() {
		stop()
		stopsWithinPatience(t, &running, "the side machine")
	})
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	raw := &rawCentral{frameRecorder: &frameRecorder{}, t: t, ctx: ctx, listener: listener, conn: conn, side: side.Fingerprint, runtime: runtime, drops: drops, log: log}
	go raw.record(conn)
	return raw
}

func (r *rawCentral) announces(value int32) {
	r.t.Helper()
	if err := r.conn.Send(event.Features(value)); err != nil {
		r.t.Fatalf("Send: %v", err)
	}
	eventually(r.t, "the side machine hearing what the central takes", func() bool {
		return askSide(r.t, r.runtime, func() bool {
			return r.runtime.centralTakes == clipboard.KindsIn(value) && r.runtime.centralDrops == clipboard.DropsIn(value)
		})
	})
}

func (r *rawCentral) opensDropData(sideSends bool, stream func(data *transport.DataConn)) int32 {
	r.t.Helper()
	wires := make(chan int32, 1)
	go func() {
		data, err := r.listener.AwaitData(r.ctx, r.side, func(wire int32) error {
			wires <- wire
			return r.conn.Send(event.OpenDropData(wire, sideSends))
		})
		if err != nil {
			return
		}
		defer data.Close()
		stream(data)
	}()
	select {
	case wire := <-wires:
		return wire
	case <-time.After(testPatience):
		r.t.Fatal("the raw central opened no drop data")
		return 0
	}
}

func (r *rawCentral) dropsFiles(paths ...string) int32 {
	r.t.Helper()
	return r.opensDropData(false, func(data *transport.DataConn) {
		filestream.SendDrop(r.ctx, data, paths)
	})
}

func (r *rawCentral) waitForDrops(count int) *fakeDropSession {
	r.t.Helper()
	eventually(r.t, "a drop starting on the side machine", func() bool { return len(r.drops.started()) >= count })
	return r.drops.started()[count-1]
}

func TestASideMachineSendsDragFramesOnlyToACentralWithTheDragBit(t *testing.T) {
	raw := newRawCentralWithASide(t)
	file := writeFile(t, filepath.Join(t.TempDir(), "drag.txt"), []byte("dragged"))

	raw.announces(textAndFiles)
	raw.runtime.DragSeen([]string{file}, edge.Left)
	time.Sleep(settling)
	for _, code := range []uint16{event.ControlDragFiles, event.ControlDragFilesSize} {
		if sent := raw.withCode(code); len(sent) > 0 {
			t.Errorf("a central without the drag bit got %+v", sent)
		}
	}
	refused := raw.dropsFiles(file)
	eventually(t, "the side machine refusing drop data from a central without the drag bit", func() bool {
		return slices.Contains(raw.withCode(event.ControlDataStop), event.DataStop(refused))
	})
	raw.log.waitFor(t, "does not take dragged files now")
	time.Sleep(settling)
	expectNoDropStarted(t, "the side machine", raw.drops)

	raw.announces(textFilesAndDrags)
	raw.runtime.DragSeen([]string{file}, edge.Left)
	eventually(t, "the side machine describing its drag", func() bool {
		return len(raw.withCode(event.ControlDragFiles)) > 0 && len(raw.withCode(event.ControlDragFilesSize)) > 0
	})
	described := raw.withCode(event.ControlDragFiles)[0]
	if files, dock := event.SplitDragFiles(described.Value); files != 1 || edge.Side(dock) != edge.Left {
		t.Errorf("the side machine described %d files with the Dock at %v, want 1 with the Dock at %v", files, edge.Side(dock), edge.Left)
	}
	if sized := raw.withCode(event.ControlDragFilesSize)[0]; sized != event.DragFilesSize(1) {
		t.Errorf("the side machine sized its drag as %+v, want 1 KiB", sized)
	}

	taken := raw.dropsFiles(file)
	raw.waitForDrops(1).take()
	eventually(t, "the side machine saying that the drop was taken", func() bool {
		return slices.Contains(raw.withCode(event.ControlDropTaken), event.DropTaken(taken))
	})
	unheard := raw.dropsFiles(file)
	second := raw.waitForDrops(2)
	raw.announces(textAndFiles)
	second.take()
	handledEverythingQueued(t, raw.runtime)
	time.Sleep(settling)
	if said := raw.withCode(event.ControlDropTaken); slices.Contains(said, event.DropTaken(unheard)) || len(said) != 1 {
		t.Errorf("the side machine said %+v, want only the drop taken while the central had the drag bit", said)
	}
}

func TestAnnouncingKindsTurnsTheDragBitOnlyWithFilesAndADropSession(t *testing.T) {
	dragBit := int32(1) << features.Drag
	withDrops := Config{Drops: &fakeDrops{}}.withDefaults()
	if got := withDrops.announcing(clipboard.Text|clipboard.Files).Value() & dragBit; got == 0 {
		t.Error("a machine with files and a drop session did not announce the drag bit")
	}
	if got := withDrops.announcing(clipboard.Text).Value() & dragBit; got != 0 {
		t.Error("a machine with files off announced the drag bit")
	}
	without := Config{}.withDefaults()
	if got := without.announcing(clipboard.Text|clipboard.Files).Value() & dragBit; got != 0 {
		t.Error("a machine without a drop session announced the drag bit")
	}
}

func dropStreamStartingWith(top string) []byte {
	var stream bytes.Buffer
	stream.WriteByte(2)
	stream.Write(binary.BigEndian.AppendUint32(nil, 1))
	stream.Write(binary.BigEndian.AppendUint64(nil, 3))
	stream.Write(binary.BigEndian.AppendUint32(nil, 1))
	stream.WriteByte(1)
	stream.Write(binary.BigEndian.AppendUint16(nil, uint16(len(top))))
	stream.WriteString(top)
	return stream.Bytes()
}

func TestATopLevelNameThatIsNotAPlainNameStartsNoDropOnASideMachine(t *testing.T) {
	raw := newRawCentralWithASide(t)
	raw.announces(textFilesAndDrags)

	for _, top := range []string{"..", ".", "/etc/passwd", "report/a.txt"} {
		wire := raw.opensDropData(false, func(data *transport.DataConn) {
			data.Write(dropStreamStartingWith(top))
			data.AwaitAllArrived(raw.ctx)
		})
		eventually(t, "the side machine stopping the drop named "+top, func() bool {
			return slices.Contains(raw.withCode(event.ControlDataStop), event.DataStop(wire))
		})
	}

	time.Sleep(settling)
	expectNoDropStarted(t, "the side machine", raw.drops)
	if left := askSide(t, raw.runtime, func() int { return len(raw.runtime.drops) }); left != 0 {
		t.Errorf("the side machine kept %d drops", left)
	}
}

func TestASideMachineSendsOnlyTheFilesOfARecentDragItDescribed(t *testing.T) {
	raw := newRawCentralWithASide(t)
	raw.announces(textFilesAndDrags)
	file := writeFile(t, filepath.Join(t.TempDir(), "offered.txt"), []byte("offered"))
	nothingToSend := func(data *transport.DataConn) {}

	unoffered := raw.opensDropData(true, nothingToSend)
	eventually(t, "the side machine refusing to send a drag it never described", func() bool {
		return slices.Contains(raw.withCode(event.ControlDataStop), event.DataStop(unoffered))
	})
	raw.log.waitFor(t, "none are offered")

	raw.runtime.DragSeen([]string{file}, 0)
	eventually(t, "the side machine describing its drag", func() bool { return len(raw.withCode(event.ControlDragFiles)) > 0 })
	raw.runtime.DragGone()
	received := make(chan error, 1)
	raw.opensDropData(true, func(data *transport.DataConn) {
		received <- filestream.ReceiveDrop(raw.ctx, data, dropArrivalsInto(t))
	})
	select {
	case err := <-received:
		if err != nil {
			t.Errorf("the files of a drag that just ended did not arrive: %v", err)
		}
	case <-time.After(testPatience):
		t.Fatal("the side machine sent nothing for a drag that just ended")
	}

	raw.runtime.DragSeen([]string{filepath.Join(t.TempDir(), "missing.txt")}, 0)
	raw.log.waitFor(t, "The file drag on this machine cannot cross")
	replaced := raw.opensDropData(true, nothingToSend)
	eventually(t, "the side machine refusing to send the files of a drag that a new drag replaced", func() bool {
		return slices.Contains(raw.withCode(event.ControlDataStop), event.DataStop(replaced))
	})
}

type arrivalsOnly struct {
	arrivals
}

func (arrivalsOnly) Dropping([]filestream.TopEntry) error { return nil }

func dropArrivalsInto(t *testing.T) filestream.DropArrivals {
	t.Helper()
	_, folder := openArrivals(t, 0)
	return arrivalsOnly{arrivals{folder.Expecting(1)}}
}

func TestDraggedFilesAreOfferedForTwoMinutesAfterTheDragEndedHere(t *testing.T) {
	s := NewSide(Config{})
	now := time.Now()
	s.dragged = []string{"/drag/report.txt"}
	if !s.offersDraggedFiles(now) {
		t.Error("the files of a drag still under way are not offered")
	}
	s.dragEnded = now.Add(-draggedFilesKeptFor + time.Second)
	if !s.offersDraggedFiles(now) {
		t.Error("the files of a drag that ended a moment ago are not offered")
	}
	s.dragEnded = now.Add(-draggedFilesKeptFor)
	if s.offersDraggedFiles(now) || s.dragged != nil {
		t.Errorf("the files of a drag that ended %s ago are still offered: %q", draggedFilesKeptFor, s.dragged)
	}
}

func TestTheCentralRemembersTheLastEightDropsItDeliveredAndNoneAlreadyTaken(t *testing.T) {
	c := NewCentral(Config{})
	right := &link{name: "right"}
	delivered := func(transfer int, taken bool) *centralTransfer {
		return &centralTransfer{id: transfer, drop: true, to: right, halves: []dataHalf{{side: right, wire: int32(100 + transfer)}}, takenThere: taken}
	}
	for transfer := 1; transfer <= deliveredDropsRemembered+2; transfer++ {
		c.rememberDelivered(delivered(transfer, false))
	}
	c.rememberDelivered(delivered(deliveredDropsRemembered+3, true))

	if len(c.delivered) != deliveredDropsRemembered {
		t.Fatalf("the central remembers %d delivered drops, want %d", len(c.delivered), deliveredDropsRemembered)
	}
	for transfer := 1; transfer <= deliveredDropsRemembered+3; transfer++ {
		half, remembered := c.delivered[transfer]
		want := transfer > 2 && transfer <= deliveredDropsRemembered+2
		if remembered != want || remembered && half.wire != int32(100+transfer) {
			t.Errorf("drop %d remembered %t with %+v, want remembered %t", transfer, remembered, half, want)
		}
	}
}

func TestACrossingIsRefusedAtOnceWhenTheCentralIsNotRunning(t *testing.T) {
	c := NewCentral(Config{Name: "desk"})
	answered := make(chan error, 1)
	go func() { answered <- c.DragCrossed("left", "right") }()
	select {
	case err := <-answered:
		if !errors.Is(err, errNotRunning) {
			t.Errorf("DragCrossed before Run = %v, want errNotRunning", err)
		}
	case <-time.After(testPatience):
		t.Fatal("DragCrossed before Run never answered")
	}
}
