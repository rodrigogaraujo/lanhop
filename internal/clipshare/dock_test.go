package clipshare

import (
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
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

func TestTheDragBitFollowsTheDragsSwitchOfTheMachine(t *testing.T) {
	dragBit := int32(1) << features.Drag
	off := Config{Drops: &fakeDrops{}, DragsOn: func() bool { return false }}.withDefaults()
	if got := off.announcing(clipboard.Text|clipboard.Files).Value() & dragBit; got != 0 {
		t.Error("a machine whose drags are off announced the drag bit")
	}
	on := Config{Drops: &fakeDrops{}, DragsOn: func() bool { return true }}.withDefaults()
	if got := on.announcing(clipboard.Text|clipboard.Files).Value() & dragBit; got == 0 {
		t.Error("a machine whose drags are on did not announce the drag bit")
	}
}

func TestASideMachineAnnouncesAgainWhenOnlyItsDragsSwitchMoves(t *testing.T) {
	var dragsOn atomic.Bool
	dragsOn.Store(true)
	raw := newRawCentralWithASideWhere(t, func(config *Config) { config.DragsOn = dragsOn.Load })
	changes := &describingChanges{}
	raw.runtime.OnDescribingDrags(changes.changed)
	raw.announces(textFilesAndDrags)
	eventually(t, "the side machine describing drags", func() bool { return slices.Equal(changes.seen(), []bool{false, true}) })

	dragsOn.Store(false)
	raw.runtime.FocusArrives()

	eventually(t, "the side machine announcing files without dragged files", func() bool {
		features := raw.withCode(event.ControlFeatures)
		return len(features) > 0 && features[len(features)-1].Value == textAndFiles
	})
	eventually(t, "the side machine stopping its drag descriptions", func() bool { return slices.Equal(changes.seen(), []bool{false, true, false}) })
}
