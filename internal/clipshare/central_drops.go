package clipshare

import (
	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/filestream"
)

const deliveredDropsRemembered = 8

type SideDrag struct {
	Origin string
	Files  int
	Dock   edge.Side
}

func (c *Central) dragSeen(paths []string) {
	c.forgetOwnDrag()
	c.dragWatch.seen(paths, 0, c.config.SilenceLimit/2, func(count dragCount) {
		c.tell(func() { c.dragCounted(count) })
	})
}

func (c *Central) dragCounted(count dragCount) {
	if !c.dragWatch.isCurrent(count) {
		return
	}
	if count.err != nil {
		noteDragThatCannotCross(c.config.Log, count.err)
		return
	}
	c.dragging, c.drag, c.dragged = true, count.files, count.paths
	if c.ownDragCounted != nil {
		c.ownDragCounted(count.files.FilesAndFolders)
	}
}

func (c *Central) dragGone() {
	c.dragWatch.gone()
	if !c.ownDragCrossed {
		c.forgetOwnDrag()
	}
	if c.ownDragGone != nil {
		c.ownDragGone()
	}
}

func (c *Central) dragOver() {
	c.dragWatch.gone()
	c.forgetOwnDrag()
	for l, machine := range c.sides {
		c.forgetDragOn(l, machine)
	}
}

func (c *Central) forgetOwnDrag() {
	c.dragging, c.drag, c.dragged, c.ownDragCrossed = false, clipboard.FileCopy{}, nil, false
}

func (c *Central) takesDraggedFilesOf(machine *sideMachine) bool {
	return machine.drops && (machine.takes & c.on).Has(clipboard.Files)
}

func (c *Central) dragOn(l *link, files clipboard.FileCopy, dock edge.Side) {
	machine := c.sides[l]
	if machine == nil || !c.takesDraggedFilesOf(machine) {
		return
	}
	machine.dragging, machine.drag, machine.dragCrossed = true, files, false
	if c.sideDragStarted != nil {
		c.sideDragStarted(SideDrag{Origin: l.name, Files: files.FilesAndFolders, Dock: dock})
	}
}

func (c *Central) dockOn(l *link, dock edge.Side) {
	machine := c.sides[l]
	if machine == nil || !machine.drops || machine.dock == dock {
		return
	}
	machine.dock = dock
	if c.sideDock != nil {
		c.sideDock(l.name, dock)
	}
}

func (c *Central) dragStayedOn(l *link) {
	machine := c.sides[l]
	if machine == nil || !machine.drops || c.sideDragStayed == nil {
		return
	}
	c.sideDragStayed(l.name)
}

func (c *Central) dragGoneOn(l *link) {
	if machine := c.sides[l]; machine != nil && !machine.dragCrossed {
		c.forgetDragOn(l, machine)
	}
}

func (c *Central) forgetDragOn(l *link, machine *sideMachine) {
	if !machine.dragging {
		return
	}
	machine.dragging, machine.drag, machine.dragCrossed = false, clipboard.FileCopy{}, false
	if c.sideDragEnded != nil {
		c.sideDragEnded(l.name)
	}
}

func (c *Central) dragCrossed(origin, to string) error {
	source, files, err := c.dragOf(origin)
	if err != nil {
		return err
	}
	actions, err := c.desk.DragCrossed(source, c.placeNamed(to), files)
	if err != nil {
		return err
	}
	if source.OnASide {
		c.sides[source.Side].dragCrossed = true
	} else {
		c.ownDragCrossed = true
	}
	if opened := len(actions); opened > 0 {
		c.lastDrop = actions[opened-1].Transfer
	}
	c.do(actions)
	return nil
}

func (c *Central) dragOf(origin string) (clipboard.Place[*link], clipboard.FileCopy, error) {
	if origin == c.config.Name {
		if !c.dragging {
			return clipboard.Place[*link]{}, clipboard.FileCopy{}, ErrNoFileDrag
		}
		return clipboard.Place[*link]{}, c.drag, nil
	}
	l := c.sideNamed(origin, func(machine *sideMachine) bool { return machine.dragging })
	machine := c.sides[l]
	if machine == nil || !machine.dragging {
		return clipboard.OnSide(l), clipboard.FileCopy{}, ErrNoFileDrag
	}
	return clipboard.OnSide(l), machine.drag, nil
}

func (c *Central) placeNamed(name string) clipboard.Place[*link] {
	if name == c.config.Name {
		return clipboard.Place[*link]{}
	}
	return clipboard.OnSide(c.sideNamed(name, func(machine *sideMachine) bool { return machine.drops }))
}

func (c *Central) sideNamed(name string, preferred func(*sideMachine) bool) *link {
	var found *link
	for l, machine := range c.sides {
		if l.name != name {
			continue
		}
		if preferred(machine) {
			return l
		}
		found = l
	}
	return found
}

func (c *Central) takesDropsOn(l *link) bool {
	if l == nil {
		return true
	}
	machine := c.sides[l]
	return machine != nil && machine.drops
}

func (c *Central) startDrop(transfer int) {
	if earlier := c.drops[transfer]; earlier != nil {
		earlier.cancel(&c.remover)
	}
	c.drops[transfer] = newDropRun()
}

func (c *Central) beginDrop(t *centralTransfer) func(tops []filestream.TopEntry) error {
	return func(tops []filestream.TopEntry) error {
		if t.run == nil {
			return errNoDropSession
		}
		return t.run.begin(t.ctx, c.config.Drops, tops,
			func() { c.tell(func() { c.dropTaken(t.id, t.run) }) },
			func() { c.tell(func() { c.dropRefused(t.id, t.run) }) })
	}
}

func (c *Central) keepForTheDrop(t *centralTransfer) {
	if t.drop && t.to != nil {
		c.rememberDelivered(t)
	}
	if t.run == nil || c.drops[t.id] != t.run || t.arrival == nil {
		return
	}
	t.run.arrival = t.arrival
	t.written = true
}

func (c *Central) finishDrop(transfer int) {
	run := c.drops[transfer]
	if run == nil || run.arrival == nil {
		return
	}
	run.hand()
	if run.taken {
		delete(c.drops, transfer)
		run.end(false)
	}
}

func (c *Central) dropTaken(transfer int, run *dropRun) {
	if c.drops[transfer] != run {
		return
	}
	run.taken = true
	c.do(c.desk.DropTaken(transfer))
	if run.handed {
		delete(c.drops, transfer)
		run.end(false)
	}
}

func (c *Central) dropRefused(transfer int, run *dropRun) {
	if c.drops[transfer] != run {
		return
	}
	delete(c.drops, transfer)
	c.do(c.desk.DropRefused(transfer))
	run.refused(&c.remover)
}

func (c *Central) cancelDrop(to *link, transfer int) {
	if to != nil {
		c.askToCancel(to, transfer)
		return
	}
	run := c.drops[transfer]
	if run == nil {
		return
	}
	delete(c.drops, transfer)
	run.cancel(&c.remover)
}

func (c *Central) askToCancel(to *link, transfer int) {
	if t := c.transfers[transfer]; t != nil {
		if !c.takesDropsOn(to) {
			t.close(errDropsNotTakenThere)
			return
		}
		t.cancelOn(to)
		return
	}
	half, delivered := c.delivered[transfer]
	if !delivered || half.side != to {
		return
	}
	delete(c.delivered, transfer)
	if c.takesDropsOn(to) {
		to.sendFrame(event.CancelDrop(half.wire))
	}
}

func (c *Central) dropTakenOn(l *link, wire int32) {
	for _, t := range c.transfers {
		if t.drop && t.to == l && t.hasHalf(l, wire) {
			t.takenOnTheSide()
			c.do(c.desk.DropTaken(t.id))
			return
		}
	}
	for transfer, half := range c.delivered {
		if half.side == l && half.wire == wire {
			delete(c.delivered, transfer)
			c.do(c.desk.DropTaken(transfer))
			return
		}
	}
}

func (c *Central) rememberDelivered(t *centralTransfer) {
	half, cancellable := t.halfStillCancellable(t.to)
	if !cancellable {
		return
	}
	c.delivered[t.id] = half
	for len(c.delivered) > deliveredDropsRemembered {
		oldest := t.id
		for transfer := range c.delivered {
			oldest = min(oldest, transfer)
		}
		delete(c.delivered, oldest)
	}
}

func (c *Central) forgetDeliveredTo(l *link) {
	for transfer, half := range c.delivered {
		if half.side == l {
			delete(c.delivered, transfer)
		}
	}
}

func (c *Central) endEveryDrop() {
	for transfer, run := range c.drops {
		delete(c.drops, transfer)
		run.cancel(&c.remover)
	}
	waitAtMost(&c.dragWatch.counting, c.config.SilenceLimit)
}
