package clipshare

import (
	"fmt"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/filestream"
)

const draggedFilesKeptFor = 2 * time.Minute

func (s *Side) dragSeen(paths []string, dock edge.Side) {
	s.dragged, s.dragEnded = nil, time.Time{}
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
	if s.describeDrag(count.files, count.dock) {
		s.dragged = count.paths
	}
}

func (s *Side) dragGone() {
	s.dragWatch.gone()
	if s.dragged != nil && s.dragEnded.IsZero() {
		s.dragEnded = time.Now()
	}
	s.describeDrag(clipboard.FileCopy{}, 0)
}

func (s *Side) forgetDraggedFiles() {
	s.dragged, s.dragEnded, s.dragDescribed = nil, time.Time{}, false
}

func (s *Side) offersDraggedFiles(now time.Time) bool {
	if s.dragged != nil && !s.dragEnded.IsZero() && now.Sub(s.dragEnded) >= draggedFilesKeptFor {
		s.forgetDraggedFiles()
	}
	return s.dragged != nil
}

func (s *Side) takesDrops() bool {
	return s.config.takesDrops(s.on)
}

func (s *Side) describesDrags() bool {
	return s.current != nil && s.centralDrops && s.takesDrops() && (s.on & s.centralTakes).Has(clipboard.Files)
}

func (s *Side) describeDrag(files clipboard.FileCopy, dock edge.Side) (described bool) {
	if !s.describesDrags() {
		s.dragDescribed = false
		return false
	}
	if files.FilesAndFolders == 0 {
		if s.dragDescribed {
			s.dragDescribed = false
			s.dockTold = s.dock
			s.send(event.DragFiles(0, int32(s.dock)))
		}
		return false
	}
	s.dragDescribed, s.dockTold = true, dock
	s.send(event.DragFiles(int32(files.FilesAndFolders), int32(dock)), event.DragFilesSize(kibibytesRoundedUp(files.Bytes)))
	return true
}

func (s *Side) dockIs(dock edge.Side) {
	s.dock = dockEdgeIn(int32(dock))
	s.tellTheDock()
}

func (s *Side) tellTheDock() {
	if !s.describing || s.dragDescribed || s.dock == s.dockTold {
		return
	}
	s.dockTold = s.dock
	s.send(event.DragFiles(0, int32(s.dock)))
}

func (s *Side) noticeDescribing() {
	describing := s.describesDrags()
	if describing == s.describing {
		return
	}
	s.describing, s.dockTold = describing, 0
	s.tellTheDock()
	if s.onDescribing != nil {
		s.onDescribing(describing)
	}
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
	if sideSends && !s.offersDraggedFiles(time.Now()) {
		fmt.Fprintln(s.config.Log, "The machine with the keyboard asked for dragged files, and none are offered, because no file drag that crossed from this machine is recent.")
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
		earlier.cancel(&s.remover)
	}
	s.drops[wire] = newDropRun()
}

func (s *Side) beginDrop(t *sideTransfer) func(tops []filestream.TopEntry) error {
	return func(tops []filestream.TopEntry) error {
		if t.run == nil {
			return errNoDropSession
		}
		return t.run.begin(t.ctx, s.config.Drops, tops,
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
	run.refused(&s.remover)
}

func (s *Side) cancelDrop(wire int) {
	run := s.drops[wire]
	if run == nil {
		return
	}
	delete(s.drops, wire)
	run.cancel(&s.remover)
}

func (s *Side) endEveryDrop() {
	for wire, run := range s.drops {
		delete(s.drops, wire)
		run.cancel(&s.remover)
	}
}
