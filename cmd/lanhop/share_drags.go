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

const dragSwitchCheckInterval = time.Second

type dropsOfTheCentral interface {
	DragSeen(paths []string)
	DragGone()
	DragCrossed(origin, to string) error
	DragCancelled()
	DragOver()
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
	endHere func() error
	log     io.Writer
	central dropsOfTheCentral
	machine dragsOfTheSharer
	allowed func() bool
	reset   func()

	mutex        sync.Mutex
	ownDock      edge.Side
	toldNotTaken map[string]bool
}

func newDragsOfTheCentral(name string, drops clipshare.Drops, endHere func() error, log io.Writer) *dragsOfTheCentral {
	return &dragsOfTheCentral{name: name, drops: drops, endHere: endHere, log: log, reset: resetLocalDrags, toldNotTaken: map[string]bool{}}
}

func (d *dragsOfTheCentral) wire(machine *sharer.Sharer, central *clipshare.Central, allowed func() bool) {
	d.central, d.machine, d.allowed = central, machine, allowed
	central.OnOwnDragCounted(d.ownDragCounted)
	central.OnSideDrags(d.sideDragStarted, d.sideDragEnded)
	central.OnSideDragStayed(d.sideDragStayed)
	central.OnSideDock(d.sideDock)
	machine.OnDragCrossing(d.crossing)
	machine.OnDragEntered(d.entered)
	machine.OnDragEnded(d.dragEnded)
	machine.OnEscape(d.escape)
	machine.OnLeftButton(d.leftButton)
}

func (d *dragsOfTheCentral) watch(done context.Context) (stop func()) {
	if d.central == nil {
		return func() {}
	}
	follower := newLocalDragFollower(d, d.log)
	follower.describing(d.allowed())
	stopFollowing := runUntilStopped(done, follower.run)
	stopChecking := runUntilStopped(done, func(checking context.Context) {
		followTheDragSwitch(checking, d.allowed, follower.describing, dragSwitchCheckInterval)
	})
	return inTurn(stopChecking, stopFollowing)
}

func followTheDragSwitch(done context.Context, allowed func() bool, describing func(on bool), every time.Duration) {
	check := time.NewTicker(every)
	defer check.Stop()
	for {
		select {
		case <-done.Done():
			return
		case <-check.C:
			describing(allowed())
		}
	}
}

func (d *dragsOfTheCentral) DragSeen(paths []string, dock edge.Side) {
	d.mutex.Lock()
	d.ownDock = dock
	d.mutex.Unlock()
	d.central.DragSeen(paths)
}

func (d *dragsOfTheCentral) ownDragCounted(files int) {
	d.mutex.Lock()
	dock := d.ownDock
	d.mutex.Unlock()
	d.machine.DragStarted(sharer.Drag{Origin: d.name, Files: files, Dock: dock})
}

func (d *dragsOfTheCentral) DragGone() {
	d.machine.SourceDragEnded(d.name)
	d.central.DragGone()
}

func (d *dragsOfTheCentral) DockIs(dock edge.Side) {
	d.dockOf(d.name, dock)
}

func (d *dragsOfTheCentral) dockOf(machine string, dock edge.Side) {
	if dock != 0 {
		d.machine.SetDock(machine, dock)
	}
}

func (d *dragsOfTheCentral) sideDragStarted(drag clipshare.SideDrag) {
	d.machine.DragStarted(sharer.Drag(drag))
	d.dockOf(drag.Origin, drag.Dock)
}

func (d *dragsOfTheCentral) sideDragEnded(origin string) {
	d.machine.SourceDragEnded(origin)
}

func (d *dragsOfTheCentral) sideDragStayed(origin string) {
	fmt.Fprintf(d.log, "The file drag that left %q stayed there, so it is cancelled.\n", origin)
	d.machine.DragCancelled()
	d.central.DragCancelled()
}

func (d *dragsOfTheCentral) sideDock(origin string, dock edge.Side) {
	d.machine.SetDock(origin, dock)
}

func (d *dragsOfTheCentral) crossing(crossing sharer.DragCrossing) error {
	if crossing.From == crossing.Origin && crossing.Origin != d.name {
		if err := d.central.DragLeaves(crossing.Origin); err != nil {
			fmt.Fprintf(d.log, "The file drag on %q stays there, because lanhop could not tell it that the drag leaves: %s\n", crossing.Origin, forTheLog(err))
			return err
		}
	}
	if crossing.To == crossing.Origin {
		d.central.DragCancelled()
	}
	return nil
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
	if err := d.endHere(); err != nil {
		fmt.Fprintf(d.log, "The file drag that left this machine is cancelled, and nothing is dropped, because lanhop could not end it here: %s\n", forTheLog(err))
		d.machine.DragCancelled()
		d.central.DragCancelled()
		return false
	}
	return true
}

func (d *dragsOfTheCentral) dragEnded(sharer.Drag) {
	d.central.DragOver()
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
