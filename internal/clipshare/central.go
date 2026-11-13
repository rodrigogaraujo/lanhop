package clipshare

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
)

type centralNews struct {
	endsAMoveOfFocus bool
	handle           func()
}

type sideMachine struct {
	takes   bool
	stale   int
	answers []*answerDue
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
	on             bool
	reachable      bool
	focused        *link
	focusHere      bool
	focusMoving    bool
	heldDuringMove []centralNews
	sides          map[*link]*sideMachine
}

func NewCentral(config Config) *Central {
	c := &Central{
		config:    config.withDefaults(),
		inbox:     newMailbox[centralNews](),
		links:     make(map[sharer.Conn]*link),
		sides:     make(map[*link]*sideMachine),
		focusHere: true,
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
	c.on = c.reachable && c.config.SwitchedOn()
	c.desk = clipboard.NewDesk[*link](onlyText(c.on))
	defer c.closeLinks()
	for {
		select {
		case <-ctx.Done():
			c.inbox.close()
			return
		case <-c.inbox.ready:
			for _, news := range c.inbox.take() {
				c.receive(news)
			}
		}
	}
}

func (c *Central) Connected(conn sharer.Conn) func(event.Event) {
	l := startLink(conn, conn.Peer().Name, c.config.SilenceLimit, c.config.Log)
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
	if c.focusMoving && !news.endsAMoveOfFocus {
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
		takes := takesCopies(frame)
		c.tell(func() { c.says(l, takes) })
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
	}
}

func (c *Central) connected(l *link) {
	c.followTheSwitch()
	c.sides[l] = &sideMachine{}
	c.do(c.desk.Connected(l))
}

func (c *Central) lost(l *link) {
	c.forgetAnswersOf(l)
	delete(c.sides, l)
	l.close()
	c.do(c.desk.Lost(l))
}

func (c *Central) left(l *link, copyCutShort bool) {
	l.leaveHandled()
	if copyCutShort {
		c.desk.SendStopped(l)
	}
	c.focusMoving = true
}

func (c *Central) arrived(toASide bool, l *link) {
	on := c.switchedOn()
	if !on {
		c.switchTo(false)
	}
	if !c.reachable {
		c.keeper.tryToReach()
	}
	c.moveFocus(toASide, l)
	if on {
		c.switchTo(true)
	}
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
	if !machine.takes || !c.on {
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

func (c *Central) says(l *link, takes bool) {
	machine := c.sides[l]
	if machine == nil {
		return
	}
	machine.takes = takes
	if !takes {
		stopWaitingFor(machine)
		l.stopCopies()
	}
	c.do(c.desk.Says(l, onlyText(takes)))
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

func (c *Central) clipboardReachable(reachable bool) {
	c.reachable = reachable
	c.followTheSwitch()
}

func (c *Central) switchedOn() bool {
	return c.reachable && c.config.SwitchedOn()
}

func (c *Central) followTheSwitch() {
	c.switchTo(c.switchedOn())
}

func (c *Central) switchTo(on bool) {
	if on == c.on {
		return
	}
	c.on = on
	actions := c.desk.Switch(onlyText(on))
	if !on {
		for l, machine := range c.sides {
			stopWaitingFor(machine)
			if l.stopCopies() {
				c.desk.SendStopped(l)
			}
		}
	}
	c.do(actions)
}

func (c *Central) readDone(result readResult) {
	if result.unchanged {
		c.do(c.desk.ReadUnchanged())
		return
	}
	actions, err := c.desk.ReadGave(result.text, result.err)
	if err != nil {
		fmt.Fprintf(c.config.Log, "The copy on this machine is not shared: %v\n", err)
	}
	c.do(actions)
}

func (c *Central) do(actions []clipboard.Action[*link]) {
	for _, action := range actions {
		switch action.Do {
		case clipboard.Announce:
			action.To.announce(c.config.turnTheClipboard(action.Takes.Has(clipboard.Text)))
		case clipboard.Send:
			if !action.To.sendNewestCopy(action.Text) {
				c.desk.SendStopped(action.To)
			}
		case clipboard.Write:
			c.keeper.write(action.Text)
		case clipboard.Read:
			c.keeper.read(func(result readResult) { c.tell(func() { c.readDone(result) }) })
		case clipboard.Mark:
			c.keeper.takeReadMark()
		}
	}
}
