package clipshare

import (
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
)

type describingChanges struct {
	mutex   sync.Mutex
	changes []bool
}

func (d *describingChanges) changed(describing bool) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.changes = append(d.changes, describing)
}

func (d *describingChanges) seen() []bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return slices.Clone(d.changes)
}

func docksTold(raw *rawCentral) []edge.Side {
	var docks []edge.Side
	for _, frame := range raw.withCode(event.ControlDragFiles) {
		if files, dock := event.SplitDragFiles(frame.Value); files == 0 {
			docks = append(docks, edge.Side(dock))
		}
	}
	return docks
}

func TestASideMachineTellsItsDockWithNoFilesOnlyWhenTheCentralTakesDraggedFiles(t *testing.T) {
	raw := newRawCentralWithASide(t)

	raw.runtime.DockIs(edge.Left)
	raw.announces(textAndFiles)
	time.Sleep(settling)
	if told := docksTold(raw); len(told) != 0 {
		t.Fatalf("a central without the drag bit was told the Dock at %v", told)
	}

	raw.announces(textFilesAndDrags)
	eventually(t, "the side machine telling its Dock", func() bool { return slices.Equal(docksTold(raw), []edge.Side{edge.Left}) })
	raw.runtime.DockIs(edge.Left)
	raw.runtime.DockIs(edge.Bottom)
	eventually(t, "the side machine telling that its Dock moved", func() bool {
		return slices.Equal(docksTold(raw), []edge.Side{edge.Left, edge.Bottom})
	})
	handledEverythingQueued(t, raw.runtime)
	time.Sleep(settling)
	if told := docksTold(raw); !slices.Equal(told, []edge.Side{edge.Left, edge.Bottom}) {
		t.Errorf("the side machine told the Dock at %v, want left then bottom, each once", told)
	}
}

func TestTheEndOfADragCarriesTheDockAndADockChangeWaitsForIt(t *testing.T) {
	raw := newRawCentralWithASide(t)
	file := writeFile(t, filepath.Join(t.TempDir(), "drag.txt"), []byte("dragged"))
	raw.runtime.DockIs(edge.Right)
	raw.announces(textFilesAndDrags)
	eventually(t, "the side machine telling its Dock", func() bool { return slices.Equal(docksTold(raw), []edge.Side{edge.Right}) })

	raw.runtime.DragSeen([]string{file}, edge.Right)
	eventually(t, "the side machine describing its drag", func() bool { return len(raw.withCode(event.ControlDragFilesSize)) == 1 })
	raw.runtime.DockIs(edge.Left)
	handledEverythingQueued(t, raw.runtime)
	time.Sleep(settling)
	if told := docksTold(raw); !slices.Equal(told, []edge.Side{edge.Right}) {
		t.Fatalf("the side machine sent %v during its drag, want no frame that ends it", told)
	}
	raw.runtime.DragGone()

	eventually(t, "the end of the drag with the Dock", func() bool { return slices.Equal(docksTold(raw), []edge.Side{edge.Right, edge.Left}) })
}

func TestTheSideMachineSaysWhenItCanDescribeItsDrags(t *testing.T) {
	raw := newRawCentralWithASide(t)
	changes := &describingChanges{}
	raw.runtime.OnDescribingDrags(changes.changed)

	raw.announces(textFilesAndDrags)
	eventually(t, "the side machine starting to describe drags", func() bool { return slices.Equal(changes.seen(), []bool{false, true}) })
	raw.announces(textAndFiles)

	eventually(t, "the side machine stopping", func() bool { return slices.Equal(changes.seen(), []bool{false, true, false}) })
}
