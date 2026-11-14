package macos

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/clipboard"
)

const (
	DragPollInterval = 20 * time.Millisecond
	shortestDragPoll = time.Millisecond
	longestDragPoll  = time.Second
	readsPerChange   = 6
	dragEventQueue   = 8

	escapeKeyCode   uint16 = 0x35
	leftMouseButton        = 0
	escapeToRelease        = 20 * time.Millisecond
)

var (
	ErrDragPollInterval = errors.New("macos: a drag watch polls at most every millisecond and at least every second")
	ErrNoDragToEnd      = errors.New("macos: the left button is not held, so there is no drag to end and nothing was posted")
	ErrCannotEndDrag    = errors.New("macos: this program is not allowed to post the events that end a drag, so nothing was posted")
)

type DragKind int

const (
	DragStarted DragKind = iota + 1
	DragEnded
)

type DragEvent struct {
	Kind  DragKind
	Paths []string
}

type filesBoard interface {
	Mark() int64
	ReadFiles() ([]string, error)
}

type dragSource struct {
	board    filesBoard
	leftHeld func() bool
}

type readKind int

const (
	readFiles readKind = iota + 1
	readNoFiles
	readRefused
)

type dragRead struct {
	kind  readKind
	paths []string
}

func readOf(paths []string, err error) dragRead {
	switch {
	case err == nil:
		return dragRead{kind: readFiles, paths: paths}
	case errors.Is(err, clipboard.ErrNotFiles):
		return dragRead{kind: readNoFiles}
	}
	return dragRead{kind: readRefused}
}

func (r dragRead) agrees(other dragRead) bool {
	return r.kind == other.kind && slices.Equal(r.paths, other.paths)
}

type dragTracker struct {
	mark      int64
	dragging  bool
	paths     []string
	candidate dragRead
	readsLeft int
}

func (d *dragTracker) poll(source dragSource) []DragEvent {
	mark := source.board.Mark()
	held := source.leftHeld()
	changed := mark != d.mark
	d.mark = mark
	if !held {
		d.readsLeft = 0
		if !d.dragging {
			return nil
		}
		d.dragging, d.paths = false, nil
		return []DragEvent{{Kind: DragEnded}}
	}
	if changed {
		d.readsLeft, d.candidate = readsPerChange, dragRead{}
	}
	if d.readsLeft == 0 {
		return nil
	}
	d.readsLeft--
	read := readOf(source.board.ReadFiles())
	if d.dragging {
		return d.duringTheDrag(read)
	}
	switch {
	case read.kind == readFiles && read.agrees(d.candidate):
		return []DragEvent{d.start(read.paths)}
	case read.kind == readFiles:
		d.candidate = read
	case read.kind == readNoFiles:
		d.candidate = dragRead{}
	default:
		d.readsLeft = 0
	}
	return nil
}

func (d *dragTracker) duringTheDrag(read dragRead) []DragEvent {
	if read.agrees(dragRead{kind: readFiles, paths: d.paths}) {
		d.readsLeft = 0
		return nil
	}
	if !read.agrees(d.candidate) {
		d.candidate = read
		return nil
	}
	d.dragging, d.paths = false, nil
	seen := []DragEvent{{Kind: DragEnded}}
	switch read.kind {
	case readFiles:
		seen = append(seen, d.start(read.paths))
	case readRefused:
		d.readsLeft = 0
	}
	return seen
}

func (d *dragTracker) start(paths []string) DragEvent {
	d.dragging, d.paths, d.readsLeft = true, paths, 0
	return DragEvent{Kind: DragStarted, Paths: slices.Clone(paths)}
}

func (d *dragTracker) reset(mark int64) {
	*d = dragTracker{mark: mark}
}

type DragWatch struct {
	source  dragSource
	tracker dragTracker
	events  chan DragEvent
	resets  chan struct{}
	stop    chan struct{}
	stopped chan struct{}
	closing sync.Once
}

func watchDrags(source dragSource, interval time.Duration) (*DragWatch, error) {
	if interval < shortestDragPoll || interval > longestDragPoll {
		return nil, fmt.Errorf("%w: %v", ErrDragPollInterval, interval)
	}
	watch := &DragWatch{
		source:  source,
		tracker: dragTracker{mark: source.board.Mark()},
		events:  make(chan DragEvent),
		resets:  make(chan struct{}, 1),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go watch.run(interval)
	return watch, nil
}

func (w *DragWatch) run(interval time.Duration) {
	defer close(w.stopped)
	defer close(w.events)
	tick := time.NewTicker(interval)
	defer tick.Stop()
	var pending []DragEvent
	told := false
	for {
		select {
		case <-w.resets:
			pending, told = w.forget(told)
		default:
		}
		var send chan<- DragEvent
		var next DragEvent
		if len(pending) > 0 {
			send, next = w.events, pending[0]
		}
		select {
		case <-w.stop:
			return
		case <-w.resets:
			pending, told = w.forget(told)
		case send <- next:
			pending, told = pending[1:], next.Kind == DragStarted
		case <-tick.C:
			if len(pending) < dragEventQueue {
				pending = append(pending, w.tracker.poll(w.source)...)
			}
		}
	}
}

func (w *DragWatch) forget(told bool) ([]DragEvent, bool) {
	w.tracker.reset(w.source.board.Mark())
	if told {
		return []DragEvent{{Kind: DragEnded}}, false
	}
	return nil, false
}

func (w *DragWatch) Events() <-chan DragEvent {
	return w.events
}

func (w *DragWatch) Reset() {
	select {
	case w.resets <- struct{}{}:
	default:
	}
}

func (w *DragWatch) Close() error {
	w.closing.Do(func() {
		close(w.stop)
		<-w.stopped
	})
	return nil
}

type dragEnder interface {
	ButtonDown(number int) bool
	Cursor() Point
	PostKey(key KeyPress)
	PostMouseAtSessionTap(mouse MouseEvent)
}

type replayedDragEnder interface {
	dragEnder
	PostMouse(mouse MouseEvent)
}

const hidReleaseAfterTheEnd = 150 * time.Millisecond

var endingSourceDrag sync.Mutex

func endReplayedSourceDrag(screen replayedDragEnder, canPost func() bool, pause func(time.Duration), under Point) error {
	endingSourceDrag.Lock()
	defer endingSourceDrag.Unlock()
	if !canPost() {
		return ErrCannotEndDrag
	}
	held := screen.ButtonDown(leftMouseButton)
	screen.PostKey(KeyPress{Code: escapeKeyCode, Down: true})
	screen.PostKey(KeyPress{Code: escapeKeyCode})
	pause(escapeToRelease)
	screen.PostMouseAtSessionTap(MouseEvent{Action: MouseUp, At: screen.Cursor(), Button: leftMouseButton, Clicks: 1})
	if held {
		pause(hidReleaseAfterTheEnd)
	}
	screen.PostMouse(MouseEvent{Action: MouseUp, At: under, Button: leftMouseButton, Clicks: 1})
	return nil
}

func endSourceDrag(screen dragEnder, canPost func() bool, pause func(time.Duration)) error {
	endingSourceDrag.Lock()
	defer endingSourceDrag.Unlock()
	if !canPost() {
		return ErrCannotEndDrag
	}
	if !screen.ButtonDown(leftMouseButton) {
		return ErrNoDragToEnd
	}
	screen.PostKey(KeyPress{Code: escapeKeyCode, Down: true})
	screen.PostKey(KeyPress{Code: escapeKeyCode})
	pause(escapeToRelease)
	screen.PostMouseAtSessionTap(MouseEvent{Action: MouseUp, At: screen.Cursor(), Button: leftMouseButton, Clicks: 1})
	return nil
}
