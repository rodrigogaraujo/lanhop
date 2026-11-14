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
	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/receiver"
)

const theCentral = "the machine with the keyboard"

type Side struct {
	config Config
	inbox  *mailbox[func()]
	keeper *keeper

	machine      *clipboard.Side
	on           clipboard.Kinds
	centralTakes clipboard.Kinds
	reachable    bool
	current      *link

	ctx         context.Context
	described   []string
	transfers   map[int]*sideTransfer
	moving      sync.WaitGroup
	filesNotice filesNotice

	centralDrops  bool
	dragWatch     dragWatch
	dragged       []string
	dragDescribed bool
	dragEnded     time.Time
	remover       remover
	drops         map[int]*dropRun
	dragsOn       bool
	describing    bool
	onDescribing  func(describing bool)
	dock          edge.Side
	dockTold      edge.Side
}

func NewSide(config Config) *Side {
	s := &Side{
		config:    config.withDefaults(),
		inbox:     newMailbox[func()](),
		transfers: make(map[int]*sideTransfer),
		drops:     make(map[int]*dropRun),
	}
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
	s.on, s.dragsOn = s.kindsOn(), s.config.dragsOn()
	s.machine = clipboard.NewSide(s.on)
	s.ctx = ctx
	defer s.stopEverything()
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

func (s *Side) DragSeen(paths []string, dock edge.Side) {
	paths = slices.Clone(paths)
	s.inbox.put(func() { s.dragSeen(paths, dock) })
}

func (s *Side) DragGone() {
	s.inbox.put(s.dragGone)
}

func (s *Side) DockIs(dock edge.Side) {
	s.inbox.put(func() { s.dockIs(dock) })
}

func (s *Side) OnDescribingDrags(changed func(describing bool)) {
	s.inbox.put(func() {
		s.onDescribing = changed
		changed(s.describing)
	})
}

func (s *Side) heardFrom(l *link, frame event.Event) {
	if !frame.IsControl() {
		return
	}
	switch frame.Code {
	case event.ControlFeatures:
		takes := clipboard.KindsIn(frame.Value)
		drops := clipboard.DropsIn(frame.Value)
		s.inbox.put(func() { s.centralSays(l, takes, drops) })
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
	case event.ControlOpenData:
		wire, sideSends := event.SplitOpenData(frame.Value)
		s.inbox.put(func() { s.openData(l, int(wire), sideSends) })
	case event.ControlDataStop:
		wire := int(frame.Value)
		s.inbox.put(func() { s.dataStop(l, wire) })
	case event.ControlOpenDropData:
		wire, sideSends := event.SplitOpenData(frame.Value)
		s.inbox.put(func() { s.openDropData(l, int(wire), sideSends) })
	case event.ControlCancelDrop:
		wire := int(frame.Value)
		s.inbox.put(func() { s.centralCancelsDrop(l, wire) })
	}
}

func (s *Side) connected(l *link) {
	s.followTheSwitch()
	s.current = l
	s.centralTakes, s.centralDrops = 0, false
	s.forgetDraggedFiles()
	s.do(s.machine.Connected())
	s.noticeDescribing()
}

func (s *Side) lost() {
	if s.current != nil {
		s.current.close()
		s.current = nil
	}
	s.forgetDraggedFiles()
	s.endEveryTransfer(errControlLost)
	s.endEveryDrop()
	s.noticeDescribing()
}

func (s *Side) stopEverything() {
	s.lost()
	waitAtMost(&s.moving, s.config.SilenceLimit)
	waitAtMost(&s.dragWatch.counting, s.config.SilenceLimit)
	s.remover.wait(s.config.SilenceLimit)
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

func (s *Side) centralSays(l *link, takes clipboard.Kinds, drops bool) {
	if l != s.current {
		return
	}
	s.centralTakes, s.centralDrops = takes, drops
	if !s.describesDrags() {
		s.forgetDraggedFiles()
	}
	s.do(s.machine.CentralSays(takes))
	s.noticeDescribing()
}

func (s *Side) copyFromCentral(l *link, text string) {
	if l == s.current {
		s.do(s.machine.CopyFromCentral(text))
	}
}

func (s *Side) openData(l *link, wire int, sideSends bool) {
	if l != s.current {
		return
	}
	s.followTheSwitch()
	if !sideSends {
		s.do(s.machine.CentralSendsFiles(wire))
		return
	}
	if !s.on.Has(clipboard.Files) {
		fmt.Fprintln(s.config.Log, "The machine with the keyboard asked for the file copy of this machine, and it is not sent, because file sharing is off on this machine.")
	}
	s.do(s.machine.CentralWantsFiles(wire))
}

func (s *Side) clipboardReachable(reachable bool) {
	s.reachable = reachable
	s.followTheSwitch()
}

func (s *Side) kindsOn() clipboard.Kinds {
	return kindsOn(s.reachable, s.config.SwitchedOn(), s.config.FilesOn(), func() bool {
		return s.filesNotice.possible(s.config.Log, s.config.whyNoFiles(s.keeper.canHoldFiles(), s.config.DialData != nil))
	})
}

func (s *Side) followTheSwitch() {
	on, dragsOn := s.kindsOn(), s.config.dragsOn()
	if on == s.on && dragsOn == s.dragsOn {
		return
	}
	kindsChanged := on != s.on
	s.on, s.dragsOn = on, dragsOn
	if kindsChanged {
		s.do(s.machine.Switch(on))
	} else {
		s.announce(s.config.announcing(on))
	}
	s.noticeDescribing()
}

func (s *Side) readDone(l *link, result readResult) {
	if l != s.current {
		return
	}
	if result.unchanged {
		s.do(s.machine.ReadUnchanged())
		return
	}
	if !result.holdsFiles {
		actions, err := s.machine.ReadGave(result.text, result.err)
		if err != nil {
			fmt.Fprintf(s.config.Log, "The copy on this machine is not shared: %s\n", forTheLog(err))
		}
		s.do(actions)
		return
	}
	actions, err := s.machine.ReadGaveFiles(result.files, result.err)
	if err != nil {
		fmt.Fprintf(s.config.Log, "The file copy on this machine is not sent: %s\n", forTheLog(err))
	} else {
		s.described = result.paths
		noteLeftOut(s.config.Log, result.skipped)
	}
	s.do(actions)
}

func (s *Side) do(actions []clipboard.Action[clipboard.ToCentral]) {
	for _, action := range actions {
		switch action.Do {
		case clipboard.Announce:
			s.announce(s.config.announcing(action.Takes))
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
			s.keeper.read(listFilesBy(s.on&s.centralTakes, s.config.SilenceLimit), func(result readResult) {
				s.inbox.put(func() { s.readDone(asked, result) })
			})
		case clipboard.Mark:
			s.keeper.takeReadMark(action.Takes)
		case clipboard.Describe:
			s.send(event.FileCopy(int32(action.FileCopy.FilesAndFolders)), event.FileCopySize(kibibytesRoundedUp(action.FileCopy.Bytes)))
		case clipboard.SendFiles:
			s.startTransfer(action.Transfer, false, action.Drop)
		case clipboard.ReceiveFiles:
			s.startTransfer(action.Transfer, true, action.Drop)
		case clipboard.WriteFiles:
			s.writeArrived(action.Transfer)
		case clipboard.Stop:
			s.send(event.DataStop(int32(action.Transfer)))
			s.endTransfer(action.Transfer, errStoppedByThisSide)
		case clipboard.StartDrop:
			s.startDrop(action.Transfer)
		case clipboard.FinishDrop:
			s.finishDrop(action.Transfer)
		case clipboard.CancelDrop:
			s.cancelDrop(action.Transfer)
		case clipboard.DropTaken:
			if s.centralDrops {
				s.send(event.DropTaken(int32(action.Transfer)))
			}
		}
	}
}

func (s *Side) announce(announced *features.Set) {
	if s.current != nil {
		s.current.announce(announced)
	}
}

func (s *Side) send(frames ...event.Event) {
	if s.current != nil {
		s.current.sendFrames(frames...)
	}
}
