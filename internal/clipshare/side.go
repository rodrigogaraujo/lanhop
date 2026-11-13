package clipshare

import (
	"context"
	"errors"
	"fmt"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/receiver"
)

const theCentral = "the machine with the keyboard"

type Side struct {
	config Config
	inbox  *mailbox[func()]
	keeper *keeper

	machine   *clipboard.Side
	on        bool
	reachable bool
	current   *link
}

func NewSide(config Config) *Side {
	s := &Side{config: config.withDefaults(), inbox: newMailbox[func()]()}
	s.keeper = newKeeper(s.config, func(reachable bool) {
		s.inbox.put(func() { s.clipboardReachable(reachable) })
	})
	return s
}

func (s *Side) Run(ctx context.Context) {
	reachedAtStart := make(chan bool, 1)
	keeperStopped := make(chan struct{})
	go func() {
		defer close(keeperStopped)
		s.keeper.run(ctx, reachedAtStart)
	}()
	defer func() { <-keeperStopped }()
	s.reachable = <-reachedAtStart
	s.on = s.reachable && s.config.SwitchedOn()
	s.machine = clipboard.NewSide(onlyText(s.on))
	defer s.lost()
	for {
		select {
		case <-ctx.Done():
			s.inbox.close()
			return
		case <-s.inbox.ready:
			for _, handle := range s.inbox.take() {
				handle()
			}
		}
	}
}

func (s *Side) Connected(conn receiver.Connection) func(event.Event) {
	l := startLink(conn, theCentral, s.config.SilenceLimit, s.config.Log)
	s.inbox.put(func() { s.connected(l) })
	return func(frame event.Event) { s.heardFrom(l, frame) }
}

func (s *Side) FocusArrives() {
	s.inbox.put(s.focusArrives)
}

func (s *Side) FocusLeaves() {
	s.inbox.put(s.focusLeaves)
}

func (s *Side) Lost() {
	s.inbox.put(s.lost)
}

func (s *Side) heardFrom(l *link, frame event.Event) {
	if !frame.IsControl() {
		return
	}
	switch frame.Code {
	case event.ControlFeatures:
		takes := takesCopies(frame)
		s.inbox.put(func() { s.centralSays(l, takes) })
	case event.ControlClipboard, event.ControlClipboardData:
		text, progress, err := l.take(frame)
		if progress == clipboard.Arrived {
			s.inbox.put(func() { s.copyFromCentral(l, text) })
		}
		if err != nil && !errors.Is(err, clipboard.ErrUnannounced) {
			fmt.Fprintf(s.config.Log, "A copy from %s was refused: %v\n", theCentral, err)
		}
	case event.ControlClipboardAck:
		l.acknowledged(frame.Value)
	}
}

func (s *Side) connected(l *link) {
	s.followTheSwitch()
	s.current = l
	s.do(s.machine.Connected())
}

func (s *Side) lost() {
	if s.current != nil {
		s.current.close()
		s.current = nil
	}
}

func (s *Side) focusArrives() {
	s.followTheSwitch()
	s.reachAgainIfLost()
	s.keeper.takeArrivalMark()
	s.machine.FocusArrives()
}

func (s *Side) focusLeaves() {
	s.followTheSwitch()
	s.reachAgainIfLost()
	s.do(s.machine.FocusLeaves())
}

func (s *Side) reachAgainIfLost() {
	if !s.reachable {
		s.keeper.tryToReach()
	}
}

func (s *Side) centralSays(l *link, takes bool) {
	if l == s.current {
		s.do(s.machine.CentralSays(onlyText(takes)))
	}
}

func (s *Side) copyFromCentral(l *link, text string) {
	if l == s.current {
		s.do(s.machine.CopyFromCentral(text))
	}
}

func (s *Side) clipboardReachable(reachable bool) {
	s.reachable = reachable
	s.followTheSwitch()
}

func (s *Side) followTheSwitch() {
	on := s.reachable && s.config.SwitchedOn()
	if on == s.on {
		return
	}
	s.on = on
	s.do(s.machine.Switch(onlyText(on)))
}

func (s *Side) readDone(l *link, result readResult) {
	if l != s.current {
		return
	}
	if result.unchanged {
		s.do(s.machine.ReadUnchanged())
		return
	}
	actions, err := s.machine.ReadGave(result.text, result.err)
	if err != nil {
		fmt.Fprintf(s.config.Log, "The copy on this machine is not shared: %v\n", err)
	}
	s.do(actions)
}

func (s *Side) do(actions []clipboard.Action[clipboard.ToCentral]) {
	for _, action := range actions {
		switch action.Do {
		case clipboard.Announce:
			s.send(s.config.announcement(action.Takes.Has(clipboard.Text)))
		case clipboard.Unchanged:
			s.send(event.ClipboardUnchanged())
		case clipboard.Withdraw:
			s.send(clipboard.Withdrawal())
		case clipboard.Send:
			if s.current != nil {
				s.current.sendCopy(action.Text)
			}
		case clipboard.Write:
			s.keeper.write(action.Text)
		case clipboard.Read:
			asked := s.current
			s.keeper.read(func(result readResult) { s.inbox.put(func() { s.readDone(asked, result) }) })
		case clipboard.Mark:
			s.keeper.takeReadMark()
		}
	}
}

func (s *Side) send(frame event.Event) {
	if s.current != nil {
		s.current.sendFrame(frame)
	}
}
