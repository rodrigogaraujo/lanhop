package clipshare

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
)

type centralNews struct {
	endsAMoveOfFocus    bool
	evenWhileFocusMoves bool
	handle              func()
}

type sideMachine struct {
	takes    clipboard.Kinds
	drops    bool
	stale    int
	answers  []*answerDue
	dragging bool
	drag     clipboard.FileCopy
	dragLeft bool
}

type answerDue struct {
	timer  *time.Timer
	gaveUp bool
}

type Central struct {
	config Config
	inbox  *mailbox[centralNews]
	keeper *keeper

	linksMutex sync.Mutex
	links      map[sharer.Conn]*link

	desk           *clipboard.Desk[*link]
	on             clipboard.Kinds
	reachable      bool
	focused        *link
	focusHere      bool
	focusMoving    bool
	heldDuringMove []centralNews
	sides          map[*link]*sideMachine

	ctx         context.Context
	stopped     chan struct{}
	transfers   map[int]*centralTransfer
	moving      sync.WaitGroup
	ownFiles    []string
	filesNotice filesNotice

	dragWatch dragWatch
	dragging  bool
	drag      clipboard.FileCopy
	dragged   []string
	lastDrop  int
	drops     map[int]*dropRun
	delivered map[int]dataHalf

	sideDragStarted func(SideDrag)
	sideDragEnded   func(origin string)
	sideDock        func(machine string, dock edge.Side)
}

func NewCentral(config Config) *Central {
	c := &Central{
		config:    config.withDefaults(),
		inbox:     newMailbox[centralNews](),
		links:     make(map[sharer.Conn]*link),
		sides:     make(map[*link]*sideMachine),
		focusHere: true,
		stopped:   make(chan struct{}),
		transfers: make(map[int]*centralTransfer),
		drops:     make(map[int]*dropRun),
		delivered: make(map[int]dataHalf),
	}
	c.keeper = newKeeper(c.config, func(reachable bool) {
		c.tell(func() { c.clipboardReachable(reachable) })
	})
	return c
}

func (c *Central) Run(ctx context.Context) {
	reachedAtStart := make(chan bool, 1)
	keeperStopped := make(chan struct{})
	go func() {
		defer close(keeperStopped)
		c.keeper.run(ctx, reachedAtStart)
	}()
	defer func() { <-keeperStopped }()
	c.reachable = <-reachedAtStart
	c.on = c.kindsOn()
	c.desk = clipboard.NewDesk[*link](c.on)
	c.ctx = ctx
	c.do(c.desk.CentralTakesDrops(c.config.takesDrops(c.on)))
	defer c.closeLinks()
	defer c.endEveryTransfer()
	defer c.endEveryDrop()
	for {
		select {
		case <-ctx.Done():
			c.inbox.close()
			close(c.stopped)
			return
		case <-c.inbox.ready:
			for _, news := range c.inbox.take() {
				c.receive(news)
			}
		}
	}
}

func (c *Central) Connected(conn sharer.Conn) func(event.Event) {
	peer := conn.Peer()
	l := startLink(conn, peer.Name, c.config.SilenceLimit, c.config.Log)
	l.fingerprint = peer.Fingerprint
	c.linksMutex.Lock()
	c.links[conn] = l
	c.linksMutex.Unlock()
	c.tell(func() { c.connected(l) })
	return func(frame event.Event) { c.heardFrom(l, frame) }
}

func (c *Central) Lost(conn sharer.Conn) {
	c.linksMutex.Lock()
	l := c.links[conn]
	delete(c.links, conn)
	c.linksMutex.Unlock()
	if l != nil {
		l.close()
		c.tell(func() { c.lost(l) })
	}
}

func (c *Central) FocusLeaves(conn sharer.Conn) {
	l := c.linkOf(conn)
	if l == nil {
		return
	}
	copyCutShort := l.leave()
	c.tell(func() { c.left(l, copyCutShort) })
}

func (c *Central) FocusArrives(conn sharer.Conn) {
	toASide := conn != nil
	var l *link
	if toASide {
		l = c.linkOf(conn)
	}
	c.inbox.put(centralNews{endsAMoveOfFocus: true, handle: func() { c.arrived(toASide, l) }})
}

func (c *Central) DragSeen(paths []string) {
	paths = slices.Clone(paths)
	c.tell(func() { c.dragSeen(paths) })
}

func (c *Central) DragGone() {
	c.tell(c.dragGone)
}

func (c *Central) OnSideDrags(started func(SideDrag), ended func(origin string)) {
	c.sideDragStarted, c.sideDragEnded = started, ended
}

func (c *Central) DragCrossed(origin, to string) error {
	answer := make(chan error, 1)
	c.inbox.put(centralNews{evenWhileFocusMoves: true, handle: func() { answer <- c.dragCrossed(origin, to) }})
	select {
	case err := <-answer:
		return err
	case <-c.stopped:
		return errSharingStopped
	}
}

func (c *Central) DragCancelled() {
	c.tell(func() { c.do(c.desk.DragCancelled(c.lastDrop)) })
}

func (c *Central) linkOf(conn sharer.Conn) *link {
	c.linksMutex.Lock()
	defer c.linksMutex.Unlock()
	return c.links[conn]
}

func (c *Central) closeLinks() {
	c.linksMutex.Lock()
	defer c.linksMutex.Unlock()
	for _, l := range c.links {
		l.close()
	}
}

func (c *Central) tell(handle func()) {
	c.inbox.put(centralNews{handle: handle})
}

func (c *Central) receive(news centralNews) {
	if c.focusMoving && !news.endsAMoveOfFocus && !news.evenWhileFocusMoves {
		c.heldDuringMove = append(c.heldDuringMove, news)
		return
	}
	news.handle()
	if news.endsAMoveOfFocus {
		c.focusMoving = false
		held := c.heldDuringMove
		c.heldDuringMove = nil
		for _, late := range held {
			c.receive(late)
		}
	}
}

func (c *Central) heardFrom(l *link, frame event.Event) {
	if !frame.IsControl() {
		return
	}
	switch frame.Code {
	case event.ControlFeatures:
		takes := clipboard.KindsIn(frame.Value)
		drops := clipboard.DropsIn(frame.Value)
		c.tell(func() { c.says(l, takes, drops) })
	case event.ControlClipboard, event.ControlClipboardData:
		text, progress, err := l.take(frame)
		switch {
		case progress == clipboard.Arrived:
			c.tell(func() { c.copyFrom(l, text) })
		case progress == clipboard.Withdrawn:
			c.tell(func() { c.withdrawalFrom(l) })
		case err != nil && !errors.Is(err, clipboard.ErrUnannounced):
			fmt.Fprintf(c.config.Log, "A copy from %s was refused: %v\n", l.name, err)
			c.tell(func() { c.withdrawalFrom(l) })
		}
	case event.ControlClipboardAck:
		l.acknowledged(frame.Value)
	case event.ControlClipboardUnchanged:
		c.tell(func() { c.unchangedOn(l) })
	case event.ControlFileCopy:
		l.filesDescribed(frame.Value)
	case event.ControlFileCopySize:
		if files, described := l.filesSized(frame.Value); described {
			c.tell(func() { c.fileCopyFrom(l, files) })
		}
	case event.ControlDataStop:
		wire := frame.Value
		c.tell(func() { c.dataStopFrom(l, wire) })
	case event.ControlDragFiles:
		if l.dragDescribed(frame.Value) {
			_, dock := event.SplitDragFiles(frame.Value)
			c.tell(func() {
				c.dragGoneOn(l)
				c.dockOn(l, dockEdgeIn(dock))
			})
		}
	case event.ControlDragFilesSize:
		if files, dock, described := l.dragSized(frame.Value); described {
			c.tell(func() { c.dragOn(l, files, dock) })
		}
	case event.ControlDropTaken:
		wire := frame.Value
		c.tell(func() { c.dropTakenOn(l, wire) })
	}
}

func (c *Central) connected(l *link) {
	c.followTheSwitch()
	c.sides[l] = &sideMachine{}
	c.do(c.desk.Connected(l))
}

func (c *Central) lost(l *link) {
	if machine := c.sides[l]; machine != nil {
		c.forgetDragOn(l, machine)
	}
	c.forgetAnswersOf(l)
	delete(c.sides, l)
	l.close()
	c.do(c.desk.Lost(l))
	c.endTransfersWith(l)
	c.forgetDeliveredTo(l)
}

func (c *Central) left(l *link, copyCutShort bool) {
	l.leaveHandled()
	if copyCutShort {
		c.desk.SendStopped(l)
	}
	c.focusMoving = true
}

func (c *Central) arrived(toASide bool, l *link) {
	next := c.kindsOn()
	c.switchTo(c.on & next)
	if !c.reachable {
		c.keeper.tryToReach()
	}
	c.moveFocus(toASide, l)
	c.switchTo(next)
}

func (c *Central) moveFocus(toASide bool, l *link) {
	if !toASide {
		if c.focusHere {
			return
		}
		c.awaitTheAnswerOf(c.focused)
		c.focused, c.focusHere = nil, true
		c.keeper.takeArrivalMark()
		c.do(c.desk.FocusOnCentral())
		return
	}
	if !c.focusHere && c.focused == l {
		return
	}
	if !c.focusHere {
		c.awaitTheAnswerOf(c.focused)
	}
	c.focused, c.focusHere = l, false
	c.do(c.desk.FocusOn(l))
}

func (c *Central) awaitTheAnswerOf(l *link) {
	machine := c.sides[l]
	if machine == nil {
		return
	}
	if machine.takes&c.on == 0 {
		machine.stale++
		return
	}
	due := &answerDue{}
	due.timer = time.AfterFunc(c.config.SilenceLimit, func() {
		c.tell(func() { c.noAnswerIn(l, due) })
	})
	machine.answers = append(machine.answers, due)
}

func (c *Central) noAnswerIn(l *link, due *answerDue) {
	machine := c.sides[l]
	if machine == nil || due.gaveUp || !slices.Contains(machine.answers, due) {
		return
	}
	due.gaveUp = true
	c.do(c.desk.GaveUpOn(l))
}

func (c *Central) answerWasOwed(l *link) bool {
	machine := c.sides[l]
	switch {
	case machine == nil:
		return false
	case machine.stale > 0:
		machine.stale--
		return true
	case len(machine.answers) > 0:
		machine.answers[0].timer.Stop()
		machine.answers = machine.answers[1:]
		return true
	}
	return false
}

func stopWaitingFor(machine *sideMachine) {
	for _, due := range machine.answers {
		due.timer.Stop()
	}
	machine.stale += len(machine.answers)
	machine.answers = nil
}

func (c *Central) forgetAnswersOf(l *link) {
	machine := c.sides[l]
	if machine == nil {
		return
	}
	for _, due := range machine.answers {
		due.timer.Stop()
	}
	machine.answers = nil
}

func (c *Central) says(l *link, takes clipboard.Kinds, drops bool) {
	machine := c.sides[l]
	if machine == nil {
		return
	}
	machine.takes, machine.drops = takes, drops
	if !c.takesDraggedFilesOf(machine) {
		c.forgetDragOn(l, machine)
	}
	if takes&c.on == 0 {
		stopWaitingFor(machine)
	}
	if !takes.Has(clipboard.Text) {
		l.stopCopies()
	}
	c.do(c.desk.Says(l, takes))
	c.do(c.desk.SaysDrops(l, drops))
}

func (c *Central) unchangedOn(l *link) {
	if c.answerWasOwed(l) {
		c.do(c.desk.UnchangedOn(l))
	}
}

func (c *Central) copyFrom(l *link, text string) {
	if c.answerWasOwed(l) {
		c.do(c.desk.CopyFrom(l, text))
	}
}

func (c *Central) withdrawalFrom(l *link) {
	if c.answerWasOwed(l) {
		c.do(c.desk.WithdrawalFrom(l))
	}
}

func (c *Central) fileCopyFrom(l *link, files clipboard.FileCopy) {
	if !c.answerWasOwed(l) {
		return
	}
	actions, err := c.desk.FileCopyFrom(l, files)
	if err != nil {
		fmt.Fprintf(c.config.Log, "A file copy from %s is not taken: %s\n", l.name, forTheLog(err))
	}
	c.do(actions)
}

func (c *Central) clipboardReachable(reachable bool) {
	c.reachable = reachable
	c.followTheSwitch()
}

func (c *Central) kindsOn() clipboard.Kinds {
	return kindsOn(c.reachable, c.config.SwitchedOn(), c.config.FilesOn(), func() bool {
		return c.filesNotice.possible(c.config.Log, c.config.whyNoFiles(c.keeper.canHoldFiles(), c.config.AwaitData != nil))
	})
}

func (c *Central) followTheSwitch() {
	c.switchTo(c.kindsOn())
}

func (c *Central) switchTo(on clipboard.Kinds) {
	if on == c.on {
		return
	}
	textOff := c.on.Has(clipboard.Text) && !on.Has(clipboard.Text)
	c.on = on
	actions := c.desk.Switch(on)
	actions = append(actions, c.desk.CentralTakesDrops(c.config.takesDrops(on))...)
	for l, machine := range c.sides {
		if machine.takes&on == 0 {
			stopWaitingFor(machine)
		}
		if !c.takesDraggedFilesOf(machine) {
			c.forgetDragOn(l, machine)
		}
		if textOff && l.stopCopies() {
			c.desk.SendStopped(l)
		}
	}
	c.do(actions)
}

func (c *Central) sharedKinds() clipboard.Kinds {
	var shared clipboard.Kinds
	for _, machine := range c.sides {
		shared |= machine.takes & c.on
	}
	return shared
}

func (c *Central) readDone(result readResult) {
	if result.unchanged {
		c.do(c.desk.ReadUnchanged())
		return
	}
	if !result.holdsFiles {
		actions, err := c.desk.ReadGave(result.text, result.err)
		if err != nil {
			fmt.Fprintf(c.config.Log, "The copy on this machine is not shared: %s\n", forTheLog(err))
		}
		c.do(actions)
		return
	}
	actions, err := c.desk.ReadGaveFiles(result.files, result.err)
	if err != nil {
		fmt.Fprintf(c.config.Log, "The file copy on this machine is not sent: %s\n", forTheLog(err))
	} else {
		c.ownFiles = result.paths
		noteLeftOut(c.config.Log, result.skipped)
	}
	c.do(actions)
}

func (c *Central) do(actions []clipboard.Action[*link]) {
	var planned []*centralTransfer
	plan := func(transfer int) *centralTransfer {
		for _, t := range planned {
			if t.id == transfer {
				return t
			}
		}
		t := &centralTransfer{id: transfer}
		planned = append(planned, t)
		return t
	}
	for _, action := range actions {
		switch action.Do {
		case clipboard.Announce:
			action.To.announce(c.config.announcing(action.Takes))
		case clipboard.Send:
			if !action.To.sendNewestCopy(action.Text) {
				c.desk.SendStopped(action.To)
			}
		case clipboard.Write:
			c.keeper.write(action.Text)
		case clipboard.Read:
			c.keeper.read(listFilesBy(c.sharedKinds(), c.config.SilenceLimit), func(result readResult) {
				c.tell(func() { c.readDone(result) })
			})
		case clipboard.Mark:
			c.keeper.takeReadMark(action.Takes)
		case clipboard.Fetch:
			t := plan(action.Transfer)
			t.from, t.drop = action.To, t.drop || action.Drop
		case clipboard.Deliver:
			t := plan(action.Transfer)
			t.to, t.drop = action.To, t.drop || action.Drop
		case clipboard.Stop:
			c.stopTransfer(action.Transfer)
		case clipboard.WriteFiles:
			c.writeArrived(action.Transfer)
		case clipboard.StartDrop:
			c.startDrop(action.Transfer)
		case clipboard.FinishDrop:
			c.finishDrop(action.Transfer)
		case clipboard.CancelDrop:
			c.cancelDrop(action.To, action.Transfer)
		}
	}
	for _, t := range planned {
		c.start(t)
	}
}
