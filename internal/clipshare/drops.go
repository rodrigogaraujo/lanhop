package clipshare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/arrival"
	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
	"github.com/rodrigogaraujo/lanhop/internal/edge"
	"github.com/rodrigogaraujo/lanhop/internal/filestream"
)

var (
	ErrNoFileDrag = errors.New("clipshare: no file is being dragged on the machine the drag left")

	errNotRunning         = errors.New("clipshare: clipboard sharing is not running")
	errNoDropSession      = errors.New("clipshare: this machine has no drop waiting for these files")
	errDropsNotTakenThere = errors.New("clipshare: the machine on the other end does not take dragged files")
)

type dropRun struct {
	stop chan struct{}

	mutex    sync.Mutex
	session  DropSession
	over     bool
	lastCall chan struct{}

	taken   bool
	handed  bool
	arrival *arrival.Arrival
}

func newDropRun() *dropRun {
	return &dropRun{stop: make(chan struct{})}
}

func (r *dropRun) begin(ctx context.Context, drops Drops, tops []filestream.TopEntry, taken, refused func()) error {
	if drops == nil {
		return errNoDropSession
	}
	if r.ended() {
		return errTransferStopped
	}
	session, err := drops.StartDrop(ctx, droppedFiles(tops))
	if err != nil {
		return fmt.Errorf("lanhop could not start the drop on this machine: %w", err)
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.over {
		r.callLater(session, DropSession.Cancel)
		return errTransferStopped
	}
	r.session = session
	go watchDropSession(session, r.stop, taken, refused)
	return nil
}

func (r *dropRun) ended() bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.over
}

func (r *dropRun) callLater(session DropSession, call func(DropSession)) {
	previous, done := r.lastCall, make(chan struct{})
	r.lastCall = done
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		call(session)
	}()
}

func (r *dropRun) hand() {
	r.handed = true
	paths := r.arrival.Paths()
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.session != nil && !r.over {
		r.callLater(r.session, func(session DropSession) { session.Arrived(paths) })
	}
}

func (r *dropRun) cancel(removal *remover) {
	removal.giveUp(r.arrival)
	r.end(true)
}

func (r *dropRun) refused(removal *remover) {
	if !r.taken {
		removal.giveUp(r.arrival)
	}
	r.end(false)
}

func (r *dropRun) end(cancelTheSession bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.over {
		return
	}
	r.over = true
	close(r.stop)
	if cancelTheSession && r.session != nil {
		r.callLater(r.session, DropSession.Cancel)
	}
}

type remover struct {
	removing sync.WaitGroup
}

func (r *remover) giveUp(arrived *arrival.Arrival) {
	if arrived == nil {
		return
	}
	r.removing.Add(1)
	go func() {
		defer r.removing.Done()
		arrived.GiveUp()
	}()
}

func (r *remover) wait(limit time.Duration) {
	waitAtMost(&r.removing, limit)
}

func droppedFiles(tops []filestream.TopEntry) []DroppedFile {
	files := make([]DroppedFile, len(tops))
	for i, top := range tops {
		files[i] = DroppedFile{Name: top.Name, Folder: top.Folder}
	}
	return files
}

func watchDropSession(session DropSession, stop <-chan struct{}, taken, refused func()) {
	select {
	case <-session.Taken():
		taken()
	case <-session.EndedWithoutDrop():
		select {
		case <-session.Taken():
			taken()
		default:
		}
		refused()
		return
	case <-stop:
		return
	}
	select {
	case <-session.EndedWithoutDrop():
		refused()
	case <-stop:
	}
}

type dropArrivals struct {
	arrivals
	begin func(tops []filestream.TopEntry) error
}

func (a dropArrivals) Dropping(tops []filestream.TopEntry) error {
	return a.begin(tops)
}

type dragCount struct {
	generation int
	paths      []string
	dock       edge.Side
	files      clipboard.FileCopy
	err        error
}

type dragWatch struct {
	generation int
	counting   sync.WaitGroup
}

func (w *dragWatch) seen(paths []string, dock edge.Side, within time.Duration, counted func(dragCount)) {
	w.generation++
	count := dragCount{generation: w.generation, paths: slices.Clone(paths), dock: dock}
	deadline := time.Now().Add(within)
	w.counting.Add(1)
	go func() {
		defer w.counting.Done()
		listing, err := filestream.Count(count.paths, deadline)
		count.files, count.err = listing.Copy, err
		counted(count)
	}()
}

func (w *dragWatch) gone() {
	w.generation++
}

func (w *dragWatch) isCurrent(count dragCount) bool {
	return count.generation == w.generation
}

func dockEdgeIn(value int32) edge.Side {
	switch side := edge.Side(value); side {
	case edge.Left, edge.Right, edge.Top, edge.Bottom:
		return side
	}
	return 0
}

func noteDragThatCannotCross(log io.Writer, err error) {
	fmt.Fprintf(log, "The file drag on this machine cannot cross: %s\n", forTheLog(err))
}
