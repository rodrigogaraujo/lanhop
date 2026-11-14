//go:build linux

package x11

import (
	"context"
	"fmt"
	"time"
)

type DropOptions struct {
	URIs         []string
	StartNow     bool
	GrabPatience time.Duration
	StatusWait   time.Duration
	FinishWait   time.Duration
	Verbose      bool
	Say          func(line string)
	Grabbed      func()
}

func (o DropOptions) withDefaults() DropOptions {
	if o.GrabPatience <= 0 {
		o.GrabPatience = 2 * time.Second
	}
	if o.StatusWait <= 0 {
		o.StatusWait = 2 * time.Second
	}
	if o.FinishWait <= 0 {
		o.FinishWait = 10 * time.Second
	}
	if o.Say == nil {
		o.Say = func(string) {}
	}
	return o
}

func (o DropOptions) say(format string, args ...any) {
	o.Say(fmt.Sprintf(format, args...))
}

type DropReport struct {
	Grab           GrabStatus
	GrabTries      int
	Target         uint32
	Destination    uint32
	Version        int
	Enters         int
	Leaves         int
	Positions      int
	Statuses       int
	Accepted       bool
	Action         string
	Dropped        bool
	Finished       bool
	FinishAccepted bool
	FinishAction   string
	DropToFinish   time.Duration
	Served         []string
	Outcome        string
}

type xdndSource struct {
	d        *Drags
	o        DropOptions
	report   *DropReport
	target   dragTarget
	waiting  bool
	askedAt  time.Time
	pending  *PointerEvent
	answered bool
	accepted bool
	action   uint32
}

func (d *Drags) Drop(ctx context.Context, o DropOptions) (report DropReport, err error) {
	o = o.withDefaults()
	drain(d.pointer)
	drain(d.messages)
	drain(d.served)

	status, tries, err := d.GrabPointer(ctx, o.GrabPatience)
	report.Grab, report.GrabTries = status, tries
	if err != nil {
		report.Outcome = fmt.Sprintf("the pointer was not grabbed: %s after %d tries", status, tries)
		return report, err
	}
	defer d.UngrabPointer()
	o.say("pointer grabbed on the root window (%s after %d tries)", status, tries)
	if o.Grabbed != nil {
		o.Grabbed()
	}

	start, err := d.dragStart(ctx, o)
	if err != nil {
		report.Outcome = "no drag started"
		return report, err
	}
	list := URIList(o.URIs)
	if err := d.offerDrag(list, start.Time); err != nil {
		report.Outcome = "XdndSelection not owned"
		return report, err
	}
	o.say("lanhop owns XdndSelection since server time %d, offering text/uri-list (%d bytes)", start.Time, len(list))

	s := &xdndSource{d: d, o: o, report: &report}
	defer func() {
		report.Accepted = s.accepted
		report.Action = d.AtomName(s.action)
	}()
	if err := s.moveTo(start); err != nil {
		return report, err
	}
	for {
		select {
		case <-ctx.Done():
			s.leave()
			report.Outcome = "stopped before the button was released"
			return report, ctx.Err()
		case <-d.c.closed:
			return report, d.c.err()
		case event := <-d.pointer:
			switch {
			case event.Kind == PointerMoved:
				if err := s.moveTo(event); err != nil {
					return report, err
				}
			case event.Kind == ButtonReleased && event.Button == dragButton:
				o.say("button %d released at %d,%d", event.Button, event.X, event.Y)
				return report, s.release(ctx, event)
			}
		case message := <-d.messages:
			s.message(message)
		case served := <-d.served:
			s.served(served)
		}
	}
}

func (d *Drags) dragStart(ctx context.Context, o DropOptions) (PointerEvent, error) {
	if o.StartNow {
		state, err := d.Pointer()
		if err != nil {
			return PointerEvent{}, err
		}
		when, err := d.serverTime()
		if err != nil {
			return PointerEvent{}, err
		}
		if held := state.Held(); len(held) > 0 {
			o.say("starting at once at %d,%d with buttons %v held", state.X, state.Y, held)
		} else {
			o.say("starting at once at %d,%d; no button is held, so the drop happens at the next release of button 1", state.X, state.Y)
		}
		return PointerEvent{Kind: PointerMoved, X: state.X, Y: state.Y, Time: when}, nil
	}
	o.say("waiting for a press of button 1")
	for {
		select {
		case event := <-d.pointer:
			if event.Kind != ButtonPressed {
				continue
			}
			if event.Button != dragButton {
				o.say("button %d pressed at %d,%d; only button 1 starts the drag", event.Button, event.X, event.Y)
				continue
			}
			o.say("button 1 pressed at %d,%d", event.X, event.Y)
			return event, nil
		case <-ctx.Done():
			return PointerEvent{}, ctx.Err()
		case <-d.c.closed:
			return PointerEvent{}, d.c.err()
		}
	}
}

func (s *xdndSource) moveTo(at PointerEvent) error {
	found, err := s.d.targetUnderPointer()
	if err != nil {
		return err
	}
	if found.window != s.target.window {
		s.leave()
		s.target = found
		s.waiting, s.pending, s.answered, s.accepted, s.action = false, nil, false, false, 0
		if found.window != atomNone {
			s.enter()
		}
	}
	if s.target.window == atomNone {
		return nil
	}
	if s.waiting {
		s.pending = &at
		return nil
	}
	s.position(at)
	return nil
}

func (s *xdndSource) enter() {
	s.report.Enters++
	s.report.Target, s.report.Destination, s.report.Version = s.target.window, s.target.destination, s.target.version
	data := [5]uint32{s.d.window, uint32(s.target.version) << 24, s.d.atoms.uriList, 0, 0}
	s.d.sendMessage(s.target.destination, s.target.window, s.d.atoms.enter, data)
	proxy := ""
	if s.target.destination != s.target.window {
		proxy = fmt.Sprintf(" through proxy 0x%x", s.target.destination)
	}
	s.o.say("XdndEnter sent to window 0x%x%s, XDND version %d, type text/uri-list", s.target.window, proxy, s.target.version)
}

func (s *xdndSource) leave() {
	if s.target.window == atomNone {
		return
	}
	s.report.Leaves++
	s.d.sendMessage(s.target.destination, s.target.window, s.d.atoms.leave, [5]uint32{s.d.window})
	s.o.say("XdndLeave sent to window 0x%x", s.target.window)
	s.target = dragTarget{}
}

func (s *xdndSource) position(at PointerEvent) {
	s.report.Positions++
	data := [5]uint32{s.d.window, 0, uint32(uint16(at.X))<<16 | uint32(uint16(at.Y)), at.Time, s.d.atoms.actionCopy}
	s.d.sendMessage(s.target.destination, s.target.window, s.d.atoms.position, data)
	s.waiting, s.askedAt = true, time.Now()
	if s.o.Verbose || !s.answered {
		s.o.say("XdndPosition sent: %d,%d, action XdndActionCopy", at.X, at.Y)
	}
}

func (s *xdndSource) isFromTarget(message clientMessage) bool {
	return s.target.window != atomNone && (message.data[0] == s.target.window || message.data[0] == s.target.destination)
}

func (s *xdndSource) message(message clientMessage) {
	if message.kind != s.d.atoms.status || !s.isFromTarget(message) {
		return
	}
	s.report.Statuses++
	accepted := message.data[1]&1 != 0
	action := message.data[4]
	if s.o.Verbose || !s.answered || accepted != s.accepted || action != s.action {
		s.o.say("XdndStatus from 0x%x after %.1f ms: accepts %v, action %s", message.data[0], time.Since(s.askedAt).Seconds()*1000, accepted, s.d.AtomName(action))
	}
	s.waiting, s.answered, s.accepted, s.action = false, true, accepted, action
	if s.pending != nil {
		next := *s.pending
		s.pending = nil
		s.position(next)
	}
}

func (s *xdndSource) served(served Served) {
	verb := "refused"
	if served.Answered {
		verb = "served"
	}
	line := fmt.Sprintf("%s %s of XdndSelection to window 0x%x", verb, s.d.AtomName(served.Target), served.Requestor)
	s.report.Served = append(s.report.Served, line)
	s.o.say("%s", line)
}

func (s *xdndSource) release(ctx context.Context, at PointerEvent) error {
	if s.target.window == atomNone {
		s.report.Outcome = "released over no window that takes XDND drops"
		return nil
	}
	if s.waiting {
		s.o.say("waiting for the XdndStatus of the last XdndPosition")
		if err := s.awaitStatus(ctx); err != nil {
			return err
		}
		if s.waiting {
			s.leave()
			s.report.Outcome = fmt.Sprintf("the target did not answer XdndPosition within %v", s.o.StatusWait)
			return nil
		}
	}
	if !s.accepted {
		s.leave()
		s.report.Outcome = "the target refused the drop"
		return nil
	}
	if s.action != s.d.atoms.actionCopy {
		chosen := s.d.AtomName(s.action)
		s.o.say("the target accepts with action %s, not XdndActionCopy, so no drop", chosen)
		s.leave()
		s.report.Outcome = fmt.Sprintf("the target chose %s instead of XdndActionCopy, so nothing was dropped", chosen)
		return nil
	}
	s.d.sendMessage(s.target.destination, s.target.window, s.d.atoms.drop, [5]uint32{s.d.window, 0, at.Time})
	dropped := time.Now()
	s.report.Dropped = true
	s.o.say("XdndDrop sent to window 0x%x at server time %d", s.target.window, at.Time)
	timer := time.NewTimer(s.o.FinishWait)
	defer timer.Stop()
	for {
		select {
		case message := <-s.d.messages:
			if message.kind != s.d.atoms.finished || !s.isFromTarget(message) {
				s.message(message)
				continue
			}
			s.drainServed()
			s.report.Finished = true
			s.report.DropToFinish = time.Since(dropped)
			s.report.FinishAccepted = s.target.version < 5 || message.data[1]&1 != 0
			if s.target.version >= 5 {
				s.report.FinishAction = s.d.AtomName(message.data[2])
			}
			s.o.say("XdndFinished from 0x%x %.1f ms after XdndDrop: accepted %v, action %s", message.data[0], s.report.DropToFinish.Seconds()*1000, s.report.FinishAccepted, s.report.FinishAction)
			s.report.Outcome = "dropped, and the target finished"
			return nil
		case served := <-s.d.served:
			s.served(served)
		case <-timer.C:
			s.drainServed()
			s.report.Outcome = fmt.Sprintf("dropped, but no XdndFinished within %v", s.o.FinishWait)
			return nil
		case <-ctx.Done():
			s.report.Outcome = "stopped while waiting for XdndFinished"
			return ctx.Err()
		case <-s.d.c.closed:
			return s.d.c.err()
		}
	}
}

func (s *xdndSource) awaitStatus(ctx context.Context) error {
	timer := time.NewTimer(time.Until(s.askedAt.Add(s.o.StatusWait)))
	defer timer.Stop()
	for s.waiting {
		select {
		case message := <-s.d.messages:
			s.pending = nil
			s.message(message)
		case served := <-s.d.served:
			s.served(served)
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-s.d.c.closed:
			return s.d.c.err()
		}
	}
	return nil
}

func (s *xdndSource) drainServed() {
	for {
		select {
		case served := <-s.d.served:
			s.served(served)
		default:
			return
		}
	}
}
