package clipshare

import (
	"fmt"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/filestream"
)

func (s *Side) dragSeen(paths []string, dock edge.Side) {
	s.dragWatch.seen(paths, dock, s.config.SilenceLimit/2, func(count dragCount) {
		s.inbox.put(func() { s.dragCounted(count) })
	})
}

func (s *Side) dragCounted(count dragCount) {
	if !s.dragWatch.isCurrent(count) {
		return
	}
	if count.err != nil {
		noteDragThatCannotCross(s.config.Log, count.err)
		s.describeDrag(clipboard.FileCopy{}, 0)
		return
	}
	s.dragged = count.paths
	s.describeDrag(count.files, count.dock)
}

func (s *Side) dragGone() {
	s.dragWatch.gone()
	s.describeDrag(clipboard.FileCopy{}, 0)
}

func (s *Side) takesDrops() bool {
	return s.config.takesDrops(s.on)
}

func (s *Side) describesDrags() bool {
	return s.current != nil && s.centralDrops && s.takesDrops() && (s.on & s.centralTakes).Has(clipboard.Files)
}

func (s *Side) describeDrag(files clipboard.FileCopy, dock edge.Side) {
	if !s.describesDrags() {
		s.dragDescribed = false
		return
	}
	if files.FilesAndFolders == 0 {
		if s.dragDescribed {
			s.dragDescribed = false
			s.send(event.DragFiles(0, 0))
		}
		return
	}
	s.dragDescribed = true
	s.send(event.DragFiles(int32(files.FilesAndFolders), int32(dock)), event.DragFilesSize(kibibytesRoundedUp(files.Bytes)))
}

func (s *Side) openDropData(l *link, wire int, sideSends bool) {
	if l != s.current {
		return
	}
	s.followTheSwitch()
	if !s.centralDrops || !s.takesDrops() {
		fmt.Fprintln(s.config.Log, "The machine with the keyboard asked to move dragged files, and they do not move, because this machine does not take dragged files now.")
		s.send(event.DataStop(int32(wire)))
		return
	}
	if sideSends {
		s.do(s.machine.CentralWantsDraggedFiles(wire))
		return
	}
	s.do(s.machine.CentralDropsFiles(wire, clipboard.FileCopy{}))
}

func (s *Side) centralCancelsDrop(l *link, wire int) {
	if l == s.current {
		s.do(s.machine.CentralCancelsDrop(wire))
	}
}

func (s *Side) startDrop(wire int) {
	if earlier := s.drops[wire]; earlier != nil {
		earlier.cancel()
	}
	s.drops[wire] = newDropRun()
}

func (s *Side) beginDrop(t *sideTransfer) func(tops []filestream.TopEntry) error {
	return func(tops []filestream.TopEntry) error {
		if t.run == nil {
			return errNoDropSession
		}
		return t.run.begin(s.ctx, s.config.Drops, tops,
			func() { s.inbox.put(func() { s.dropTaken(t.wire, t.run) }) },
			func() { s.inbox.put(func() { s.dropRefused(t.wire, t.run) }) })
	}
}

func (s *Side) keepForTheDrop(t *sideTransfer) {
	if t.run == nil || s.drops[t.wire] != t.run || t.arrival == nil {
		return
	}
	t.run.arrival = t.arrival
	t.written = true
}

func (s *Side) finishDrop(wire int) {
	run := s.drops[wire]
	if run == nil || run.arrival == nil {
		return
	}
	run.hand()
	if run.taken {
		delete(s.drops, wire)
		run.end(false)
	}
}

func (s *Side) dropTaken(wire int, run *dropRun) {
	if s.drops[wire] != run {
		return
	}
	run.taken = true
	s.do(s.machine.DropTaken(wire))
	if run.handed {
		delete(s.drops, wire)
		run.end(false)
	}
}

func (s *Side) dropRefused(wire int, run *dropRun) {
	if s.drops[wire] != run {
		return
	}
	delete(s.drops, wire)
	s.do(s.machine.DropRefused(wire))
	run.refused()
}

func (s *Side) cancelDrop(wire int) {
	run := s.drops[wire]
	if run == nil {
		return
	}
	delete(s.drops, wire)
	run.cancel()
}

func (s *Side) endEveryDrop() {
	for wire, run := range s.drops {
		delete(s.drops, wire)
		run.cancel()
	}
}
