package clipshare

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
)

func TestACentralThatCannotDropRelaysADragFromOneSideMachineToAnother(t *testing.T) {
	d := newRelayingDesk(t, "l1", "l2")
	l1, l2 := d.sideJoins("l1"), d.sideJoins("l2")
	d.waitUntilTakingDrops(l1, l2)
	source := t.TempDir()
	report := writeFile(t, filepath.Join(source, "report.txt"), randomContent(t, 200<<10))
	photos := copiedFolder(t, filepath.Join(source, "photos"))

	d.dragCrosses(l1, l2, report, photos)
	session := waitForADrop(t, "l2", l2.drops)
	session.take()

	expectSameFiles(t, waitUntilHanded(t, "l2", session), l2.arrivalDir, report, photos)
	d.expectNoTransfersLeft(l1, l2)
	d.expectNoDropsLeft(l1, l2)
	expectNoDropStarted(t, "the central", d.drops)
	if kept := arrivalsLeftIn(t, d.arrivalDir); len(kept) > 0 {
		t.Errorf("the central kept %q of a drop that only passed through it", kept)
	}
}

func TestADragTowardACentralThatCannotDropIsStillRefused(t *testing.T) {
	d := newRelayingDesk(t, "l1")
	l1 := d.sideJoins("l1")
	d.waitUntilTakingDrops(l1)
	file := writeFile(t, filepath.Join(t.TempDir(), "plan.pdf"), randomContent(t, 4<<10))

	d.focusMovesTo(l1)
	d.sideDrags(l1, file)
	d.focusMovesTo(onTheCentral)
	if err := d.crosses(l1, onTheCentral); !errors.Is(err, clipboard.ErrDropsNotTaken) {
		t.Errorf("a drag toward a central that cannot drop = %v, want ErrDropsNotTaken", err)
	}

	time.Sleep(settling)
	d.expectNoTransfersLeft(l1)
	expectNoDropStarted(t, "the central", d.drops)
}

func TestACentralAnnouncesThatItRelaysDragsOnlyWhileFilesAreShared(t *testing.T) {
	drag, relays := int32(1)<<features.Drag, int32(1)<<features.RelaysDrags
	for _, c := range []struct {
		name  string
		drops Drops
		kinds clipboard.Kinds
		want  int32
	}{
		{"a central that can drop, with files", &fakeDrops{}, clipboard.Text | clipboard.Files, drag | relays},
		{"a central that cannot drop, with files", nil, clipboard.Text | clipboard.Files, relays},
		{"a central with files off", &fakeDrops{}, clipboard.Text, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			config := Config{Drops: c.drops}.withDefaults()
			if got := config.announcingAsCentral(c.kinds).Value() & (drag | relays); got != c.want {
				t.Errorf("the drag bits are %#x, want %#x", got, c.want)
			}
		})
	}
	if got := (Config{Drops: &fakeDrops{}}.withDefaults()).announcing(clipboard.Text|clipboard.Files).Value() & relays; got != 0 {
		t.Error("a side machine announced that it relays drags")
	}
}

func TestASideMachineSendsNoDragFramesToAnOlderCentralWithNeitherBit(t *testing.T) {
	raw := newRawCentralWithASide(t)
	file := writeFile(t, filepath.Join(t.TempDir(), "drag.txt"), []byte("dragged"))
	raw.announces(textAndFiles)

	raw.runtime.DockIs(edge.Top)
	raw.runtime.DragSeen([]string{file}, edge.Top)
	raw.runtime.DragGone()
	raw.runtime.DragStayed()
	handledEverythingQueued(t, raw.runtime)
	time.Sleep(settling)

	for _, frame := range raw.all() {
		if frame.IsControl() && frame.Code >= event.ControlDragFiles && frame.Code <= event.ControlDragStayed {
			t.Errorf("an older central was sent %+v", frame)
		}
	}
	if err := raw.conn.Send(event.OpenDropData(9, false)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	eventually(t, "the side machine refusing drop data from an older central", func() bool {
		return slices.Contains(raw.withCode(event.ControlDataStop), event.DataStop(9))
	})
	expectNoDropStarted(t, "the side machine", raw.drops)
}

func TestASideMachineDescribesItsDragToACentralThatOnlyRelaysDrags(t *testing.T) {
	raw := newRawCentralWithASide(t)
	file := writeFile(t, filepath.Join(t.TempDir(), "drag.txt"), []byte("dragged"))
	raw.announces(textAndFiles | 1<<features.RelaysDrags)

	raw.runtime.DragSeen([]string{file}, edge.Top)

	eventually(t, "the side machine describing its drag", func() bool {
		return len(raw.withCode(event.ControlDragFiles)) == 1 && len(raw.withCode(event.ControlDragFilesSize)) == 1
	})
	wire := raw.dropsFiles(file)
	raw.waitForDrops(1).take()
	eventually(t, "the side machine saying that the relayed drop was taken", func() bool {
		return slices.Contains(raw.withCode(event.ControlDropTaken), event.DropTaken(wire))
	})
}
