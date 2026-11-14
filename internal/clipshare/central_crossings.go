package clipshare

import "github.com/rodrigogaraujo/lanhop/internal/event"

func (c *Central) OnOwnDragCounted(counted func(files int)) {
	c.ownDragCounted = counted
}

func (c *Central) DragLeaves(origin string) error {
	select {
	case <-c.started:
	default:
		return errNotRunning
	}
	answer := make(chan error, 1)
	c.inbox.put(centralNews{evenWhileFocusMoves: true, handle: func() { answer <- c.dragLeaves(origin) }})
	select {
	case err := <-answer:
		return err
	case <-c.stopped:
		return errNotRunning
	}
}

func (c *Central) dragLeaves(origin string) error {
	l := c.sideNamed(origin, func(machine *sideMachine) bool { return machine.dragging })
	machine := c.sides[l]
	if machine == nil || !machine.dragging {
		return ErrNoFileDrag
	}
	if err := l.conn.Send(event.DragLeaves()); err != nil {
		return err
	}
	machine.dragCrossed = true
	return nil
}
