package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/clipshare"
	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
)

const dockCheckInterval = 30 * time.Second

type dropsOfTheCentral interface {
	DragSeen(paths []string)
	DragGone()
	DragCrossed(origin, to string) error
	DragCancelled()
	DragLeaves(origin string) error
}

type dragsOfTheSharer interface {
	DragStarted(drag sharer.Drag)
	DragCancelled()
	SourceDragEnded(origin string)
	SetDock(machine string, dock edge.Side)
}

type dragsOfTheCentral struct {
	name    string
	drops   clipshare.Drops
	dock    func() edge.Side
	endHere func() error
	log     io.Writer
	central dropsOfTheCentral
	machine dragsOfTheSharer
	reset   func()

	mutex        sync.Mutex
	ownDragLeft  bool
	toldNotTaken map[string]bool
}

func newDragsOfTheCentral(name string, drops clipshare.Drops, dock func() edge.Side, endHere func() error, log io.Writer) *dragsOfTheCentral {
	return &dragsOfTheCentral{name: name, drops: drops, dock: dock, endHere: endHere, log: log, reset: resetLocalDrags, toldNotTaken: map[string]bool{}}
}

func (d *dragsOfTheCentral) wire(machine *sharer.Sharer, central *clipshare.Central) {
	d.central, d.machine = central, machine
	central.OnSideDrags(d.sideDragStarted, d.sideDragEnded)
	central.OnSideDock(d.dockOf)
	machine.OnDragCrossing(d.crossing)
	machine.OnDragEntered(d.entered)
	machine.OnEscape(d.escape)
	machine.OnLeftButton(d.leftButton)
}

func (d *dragsOfTheCentral) watch(done context.Context) (stop func()) {
	if d.central == nil {
		return func() {}
	}
	watching, stopWatching := context.WithCancel(done)
	var running sync.WaitGroup
	running.Add(1)
	go func() {
		defer running.Done()
		d.keepTheDockFresh(watching, dockCheckInterval)
	}()
	drags, stopDrags, err := watchLocalDrags(watching)
	if err != nil {
		fmt.Fprintf(d.log, "lanhop cannot see a file dragged on this machine, so such a drag cannot cross: %s\n", forTheLog(err))
	}
	if drags != nil {
		running.Add(1)
		go func() {
			defer running.Done()
			d.follow(watching, drags)
		}()
	}
	return func() {
		stopWatching()
		if stopDrags != nil {
			stopDrags()
		}
		running.Wait()
	}
}

func (d *dragsOfTheCentral) follow(done context.Context, drags <-chan localDrag) {
	for {
		select {
		case <-done.Done():
			return
		case drag, open := <-drags:
			if !open {
				return
			}
			d.localDrag(drag)
		}
	}
}

func (d *dragsOfTheCentral) keepTheDockFresh(done context.Context, every time.Duration) {
	d.ownDock()
	check := time.NewTicker(every)
	defer check.Stop()
	for {
		select {
		case <-done.Done():
			return
		case <-check.C:
			d.ownDock()
		}
	}
}

func (d *dragsOfTheCentral) ownDock() {
	d.dockOf(d.name, d.dock())
}

func (d *dragsOfTheCentral) dockOf(machine string, dock edge.Side) {
	if dock != 0 {
		d.machine.SetDock(machine, dock)
	}
}

func (d *dragsOfTheCentral) localDrag(drag localDrag) {
	if !drag.Started {
		d.machine.SourceDragEnded(d.name)
		if !d.ownDragHasLeft() {
			d.central.DragGone()
		}
		return
	}
	d.setOwnDragLeft(false)
	d.central.DragSeen(drag.Paths)
	d.machine.DragStarted(sharer.Drag{Origin: d.name, Files: len(drag.Paths), Dock: d.dock()})
}

func (d *dragsOfTheCentral) sideDragStarted(drag clipshare.SideDrag) {
	d.machine.DragStarted(sharer.Drag(drag))
	d.dockOf(drag.Origin, drag.Dock)
}

func (d *dragsOfTheCentral) sideDragEnded(origin string) {
	d.machine.SourceDragEnded(origin)
}

func (d *dragsOfTheCentral) crossing(crossing sharer.DragCrossing) {
	if crossing.From == crossing.Origin && crossing.Origin != d.name {
		if err := d.central.DragLeaves(crossing.Origin); err != nil {
			fmt.Fprintf(d.log, "lanhop could not tell %q to end the file drag that left it: %s\n", crossing.Origin, forTheLog(err))
		}
	}
	if crossing.To == crossing.Origin {
		d.central.DragCancelled()
	}
}

func (d *dragsOfTheCentral) entered(crossing sharer.DragCrossing) {
	if crossing.To == crossing.Origin {
		return
	}
	if crossing.From == crossing.Origin && crossing.Origin == d.name && !d.endTheDragHere() {
		return
	}
	err := d.central.DragCrossed(crossing.Origin, crossing.To)
	switch {
	case err == nil:
	case errors.Is(err, clipboard.ErrDropsNotTaken):
		d.tellDropsNotTaken(crossing.To)
	default:
		fmt.Fprintf(d.log, "The file dragged to %q is not dropped there: %s\n", crossing.To, forTheLog(err))
		d.machine.DragCancelled()
	}
}

func (d *dragsOfTheCentral) endTheDragHere() bool {
	d.setOwnDragLeft(true)
	if err := d.endHere(); err != nil {
		d.setOwnDragLeft(false)
		fmt.Fprintf(d.log, "The file drag on this machine does not cross, because lanhop could not end it here: %s\n", forTheLog(err))
		d.machine.DragCancelled()
		d.central.DragCancelled()
		return false
	}
	return true
}

func (d *dragsOfTheCentral) escape(duringADrag bool) {
	d.central.DragCancelled()
	if !duringADrag {
		return
	}
	d.resetLocalDrags()
	d.machine.DragCancelled()
}

func (d *dragsOfTheCentral) leftButton(bool) {
	d.resetLocalDrags()
}

func (d *dragsOfTheCentral) resetLocalDrags() {
	if d.reset != nil {
		d.reset()
	}
}

func (d *dragsOfTheCentral) tellDropsNotTaken(machine string) {
	d.mutex.Lock()
	told := d.toldNotTaken[machine]
	d.toldNotTaken[machine] = true
	d.mutex.Unlock()
	if !told {
		fmt.Fprintf(d.log, "A file dragged to %q is not dropped there, because that machine does not take dragged files now.\n", machine)
	}
}

func (d *dragsOfTheCentral) setOwnDragLeft(left bool) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.ownDragLeft = left
}

func (d *dragsOfTheCentral) ownDragHasLeft() bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.ownDragLeft
}
