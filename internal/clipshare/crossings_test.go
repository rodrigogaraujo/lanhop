package clipshare

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
)

type reportedDock struct {
	machine string
	dock    edge.Side
}

type reportedDocks struct {
	mutex   sync.Mutex
	reports []reportedDock
}

func (r *reportedDocks) reported(machine string, dock edge.Side) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.reports = append(r.reports, reportedDock{machine, dock})
}

func (r *reportedDocks) seen() []reportedDock {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return slices.Clone(r.reports)
}

func (r *rawDragSide) drags(t *testing.T, files int32, dock edge.Side) {
	t.Helper()
	if err := r.conn.Send(event.DragFiles(files, int32(dock)), event.DragFilesSize(1)); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func (r *rawDragSide) dragEnds(t *testing.T) {
	t.Helper()
	if err := r.conn.Send(event.DragFiles(0, 0)); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func (d *deskUnderTest) centralEndOf(name string) Conn {
	d.t.Helper()
	d.runtime.linksMutex.Lock()
	defer d.runtime.linksMutex.Unlock()
	for _, l := range d.runtime.links {
		if l.name == name {
			return l.conn
		}
	}
	d.t.Fatalf("the central has no connection to %s", name)
	return nil
}

func TestTheCentralTellsOnlyTheSideMachineThatReportedTheDragThatItLeaves(t *testing.T) {
	d := newDropDesk(t, "raw", "other", "plain")
	raw := d.rawSideAnnouncing("raw", textFilesAndDrags)
	other := d.rawSideAnnouncing("other", textFilesAndDrags)
	plain := d.rawSideAnnouncing("plain", textAndFiles)

	if err := d.runtime.DragLeaves("raw"); !errors.Is(err, ErrNoFileDrag) {
		t.Errorf("DragLeaves before any drag = %v, want ErrNoFileDrag", err)
	}
	raw.drags(t, 2, edge.Left)
	plain.drags(t, 1, edge.Left)
	eventually(t, "the central seeing the drag on raw", func() bool { return d.seesADragOn(d.sides["raw"]) })

	if err := d.runtime.DragLeaves("raw"); err != nil {
		t.Fatalf("DragLeaves of the side machine that drags = %v", err)
	}
	for _, name := range []string{"other", "plain", "desk", "nobody"} {
		if err := d.runtime.DragLeaves(name); !errors.Is(err, ErrNoFileDrag) {
			t.Errorf("DragLeaves(%q) = %v, want ErrNoFileDrag", name, err)
		}
	}

	eventually(t, "raw hearing that its drag leaves", func() bool { return len(raw.withCode(event.ControlDragLeaves)) == 1 })
	time.Sleep(settling)
	if got := raw.withCode(event.ControlDragLeaves); !slices.Equal(got, []event.Event{event.DragLeaves()}) {
		t.Errorf("raw got %+v, want one drag leaves frame", got)
	}
	for name, side := range map[string]*rawDragSide{"other": other, "plain": plain} {
		if got := side.withCode(event.ControlDragLeaves); len(got) > 0 {
			t.Errorf("%s got %+v, though its drag did not leave", name, got)
		}
	}
}

func TestTheFrameThatADragLeavesIsSentBeforeDragLeavesReturns(t *testing.T) {
	d := newDropDesk(t, "raw")
	raw := d.rawSideAnnouncing("raw", textFilesAndDrags)
	raw.drags(t, 1, 0)
	eventually(t, "the central seeing the drag on raw", func() bool { return d.seesADragOn(d.sides["raw"]) })

	if err := d.runtime.DragLeaves("raw"); err != nil {
		t.Fatalf("DragLeaves = %v", err)
	}
	if err := d.centralEndOf("raw").Send(event.Leave()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	eventually(t, "raw hearing the leave", func() bool { return slices.Contains(raw.all(), event.Leave()) })
	frames := raw.all()
	if leaves, left := slices.Index(frames, event.DragLeaves()), slices.Index(frames, event.Leave()); leaves < 0 || leaves > left {
		t.Errorf("raw heard %+v, want the drag leaves frame before the leave", frames)
	}
}

func TestADragThatLeftItsSideMachineStaysCrossableAfterThatMachineEndsItsSourceDrag(t *testing.T) {
	d := newDropDesk(t, "raw")
	raw := d.rawSideAnnouncing("raw", textFilesAndDrags)
	rawSide := d.sides["raw"]

	raw.drags(t, 1, 0)
	eventually(t, "the central seeing the first drag", func() bool { return d.seesADragOn(rawSide) })
	raw.dragEnds(t)
	eventually(t, "the central forgetting the drag that ended where it started", func() bool { return !d.seesADragOn(rawSide) })
	if err := d.crosses(rawSide, onTheCentral); !errors.Is(err, ErrNoFileDrag) {
		t.Errorf("a drag that ended on its side machine crossed with %v, want ErrNoFileDrag", err)
	}

	raw.drags(t, 1, 0)
	eventually(t, "the central seeing the second drag", func() bool { return d.seesADragOn(rawSide) })
	if err := d.runtime.DragLeaves("raw"); err != nil {
		t.Fatalf("DragLeaves = %v", err)
	}
	raw.dragEnds(t)
	askCentral(t, d.runtime, func() bool { return true })
	time.Sleep(settling)

	if !d.seesADragOn(rawSide) {
		t.Fatal("the central forgot the drag that left raw when raw ended its source drag")
	}
	if err := d.crosses(rawSide, onTheCentral); err != nil {
		t.Errorf("the drag that left raw could not cross on: %v", err)
	}
	if _, ends := d.described.seen(); !slices.Equal(ends, []string{"raw"}) {
		t.Errorf("the central told the ends %q, want only the end of the first drag", ends)
	}

	raw.drags(t, 1, 0)
	eventually(t, "the central seeing the third drag", func() bool {
		starts, _ := d.described.seen()
		return len(starts) == 3
	})
	raw.dragEnds(t)
	eventually(t, "the central forgetting the third drag, which never left", func() bool { return !d.seesADragOn(rawSide) })
}

func TestTheCentralTellsTheDockOfASideMachineThatTakesDraggedFiles(t *testing.T) {
	d := newDropDesk(t, "raw", "plain")
	raw := d.rawSideAnnouncing("raw", textFilesAndDrags)
	plain := d.rawSideAnnouncing("plain", textAndFiles)
	docks := &reportedDocks{}
	askCentral(t, d.runtime, func() bool {
		d.runtime.OnSideDock(docks.reported)
		return true
	})

	for _, dock := range []edge.Side{edge.Left, edge.Bottom} {
		if err := raw.conn.Send(event.DragFiles(0, int32(dock))); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	if err := plain.conn.Send(event.DragFiles(0, int32(edge.Right))); err != nil {
		t.Fatalf("Send: %v", err)
	}
	raw.drags(t, 2, edge.Top)
	raw.dragEnds(t)

	want := []reportedDock{{"raw", edge.Left}, {"raw", edge.Bottom}, {"raw", 0}}
	eventually(t, "the central telling every dock raw reported", func() bool { return len(docks.seen()) >= len(want) })
	time.Sleep(settling)
	if got := docks.seen(); !slices.Equal(got, want) {
		t.Errorf("the central told the docks %+v, want %+v", got, want)
	}
	if starts, _ := d.described.seen(); !slices.Equal(starts, []SideDrag{{Origin: "raw", Files: 2, Dock: edge.Top}}) {
		t.Errorf("the central told the drags %+v, want the drag of raw with its dock at the top", starts)
	}
}
