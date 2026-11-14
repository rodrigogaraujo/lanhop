package clipshare

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
)

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

func TestTheCentralTellsItsOwnDragOnlyOnceItsFilesAreCounted(t *testing.T) {
	d := newDropDesk(t)
	counted := make(chan int, 4)
	askCentral(t, d.runtime, func() bool {
		d.runtime.OnOwnDragCounted(func(files int) { counted <- files })
		return true
	})
	source := t.TempDir()
	first := writeFile(t, filepath.Join(source, "a.txt"), []byte("a"))
	second := writeFile(t, filepath.Join(source, "b.txt"), []byte("b"))

	d.runtime.DragSeen([]string{filepath.Join(source, "missing.txt")})
	askCentral(t, d.runtime, func() bool { return true })
	time.Sleep(settling)
	select {
	case files := <-counted:
		t.Fatalf("a drag whose files could not be counted was told with %d files", files)
	default:
	}

	d.runtime.DragSeen([]string{first, second})
	select {
	case files := <-counted:
		if files != 2 {
			t.Errorf("the drag was told with %d files, want 2", files)
		}
	case <-time.After(testPatience):
		t.Fatal("the counted drag was never told")
	}
	if !d.seesADragOn(onTheCentral) {
		t.Error("the central did not keep the drag it told")
	}
}

func TestTheEndOfTheCentralsOwnDragIsToldFromItsLoopAfterItsCount(t *testing.T) {
	d := newDropDesk(t)
	told := make(chan string, 8)
	askCentral(t, d.runtime, func() bool {
		d.runtime.OnOwnDragCounted(func(int) { told <- "counted" })
		d.runtime.OnOwnDragGone(func() { told <- "gone" })
		return true
	})
	file := writeFile(t, filepath.Join(t.TempDir(), "a.txt"), []byte("a"))

	d.runtime.DragSeen([]string{file})
	if got := nextTold(t, told); got != "counted" {
		t.Fatalf("first told %q, want the count", got)
	}
	d.runtime.DragGone()

	if got := nextTold(t, told); got != "gone" {
		t.Errorf("then told %q, want the end", got)
	}
}

func nextTold(t *testing.T, told <-chan string) string {
	t.Helper()
	select {
	case got := <-told:
		return got
	case <-time.After(testPatience):
		t.Fatal("the central told nothing")
		return ""
	}
}
